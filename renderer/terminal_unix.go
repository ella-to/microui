//go:build unix

package renderer

import (
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// This file holds the Unix raw-mode plumbing. Raw mode is set via the stty(1)
// program so the package stays dependency-free (no cgo, no termios bindings).

// sysState is the Unix terminal state saved by enterRaw.
type sysState struct {
	saved string         // `stty -g` output, restored by leaveRaw
	sig   chan os.Signal // SIGWINCH plus the quit signals
}

// isTerminal reports whether f is a tty: a character device other than
// /dev/null (which IDEs and service managers often attach to stdin).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null)
}

// stty runs stty(1) against the controlling terminal (os.Stdin) and returns
// its stdout.
func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return string(out), err
}

// enterRaw saves the tty state, switches to raw mode and enables the screen
// and input modes the renderer needs.
func (t *Terminal) enterRaw() error {
	saved, err := stty("-g")
	if err != nil {
		return err
	}
	t.sys.saved = strings.TrimSpace(saved)
	if _, err := stty("raw", "-echo"); err != nil {
		return err
	}
	t.enterModes()
	return nil
}

// leaveRaw restores the terminal to the state enterRaw saved.
func (t *Terminal) leaveRaw() {
	t.leaveModes()
	if t.sys.saved != "" {
		_, _ = stty(t.sys.saved)
	} else {
		_, _ = stty("sane")
	}
}

// size returns the terminal size in (cols, rows), falling back to 80x24.
func (t *Terminal) size() (cols, rows int) {
	cols, rows = 80, 24
	out, err := stty("size")
	if err != nil {
		return
	}
	r, c, ok := strings.Cut(strings.TrimSpace(out), " ") // "rows cols"
	if !ok {
		return
	}
	if n, err := strconv.Atoi(r); err == nil && n > 0 {
		rows = n
	}
	if n, err := strconv.Atoi(c); err == nil && n > 0 {
		cols = n
	}
	return
}

// pollResize is unused on Unix: SIGWINCH reports resizes.
func (t *Terminal) pollResize() bool { return false }

// watchSignals turns SIGWINCH into a resize and SIGINT/SIGTERM/SIGHUP into a
// quit request, waking Run either way. (In raw mode Ctrl-C arrives as a byte
// instead, handled by the input parser.) Quitting through Poll lets Run
// return normally, so the caller's deferred cleanup runs.
func (t *Terminal) watchSignals() {
	ch := make(chan os.Signal, 4)
	t.sys.sig = ch
	signal.Notify(ch, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for s := range ch {
			if s == syscall.SIGWINCH {
				t.resized.Store(true)
			} else {
				t.quit.Store(true)
			}
			t.signalWake()
		}
	}()
}

// stopSignals undoes watchSignals.
func (t *Terminal) stopSignals() {
	if t.sys.sig != nil {
		signal.Stop(t.sys.sig)
		close(t.sys.sig)
		t.sys.sig = nil
	}
}

// suspend implements Ctrl-Z: restore the terminal, stop the process group
// like a shell would, and on resume (fg) re-enter raw mode and repaint.
func (t *Terminal) suspend() {
	t.leaveRaw()
	_ = syscall.Kill(0, syscall.SIGTSTP)
	// execution continues here after SIGCONT
	if err := t.enterRaw(); err != nil {
		t.fail(err)
		return
	}
	t.resized.Store(true)
}
