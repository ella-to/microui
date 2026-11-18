package renderer

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// This file holds the terminal's raw-mode plumbing. Raw mode is set via the
// stty(1) program so the package stays dependency-free (no cgo, no termios
// bindings).

// stty runs stty(1) against the controlling terminal (os.Stdin) and returns its
// stdout.
func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return string(out), err
}

// isInteractive reports whether we appear to be attached to a real terminal.
func isInteractive() bool {
	_, err := stty("-g")
	return err == nil
}

// enterRaw switches the terminal into raw mode and turns on the alternate
// screen, hides the cursor and enables SGR mouse tracking.
func (t *Terminal) enterRaw() error {
	saved, err := stty("-g")
	if err != nil {
		return err
	}
	t.savedState = strings.TrimSpace(saved)
	if _, err := stty("raw", "-echo"); err != nil {
		return err
	}
	// alt screen, hide cursor, clear, enable all-motion + SGR mouse reporting
	os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1003h\x1b[?1006h")
	return nil
}

// leaveRaw restores the terminal to its original state.
func (t *Terminal) leaveRaw() {
	// disable mouse, show cursor, leave alt screen
	os.Stdout.WriteString("\x1b[?1003l\x1b[?1006l\x1b[?25h\x1b[?1049l")
	if t.savedState != "" {
		_, _ = stty(t.savedState)
		t.savedState = ""
	} else {
		_, _ = stty("sane")
	}
}

// termSize returns the terminal size in (cols, rows), falling back to 80x24.
func (t *Terminal) termSize() (cols, rows int) {
	cols, rows = 80, 24
	out, err := stty("size")
	if err != nil {
		return
	}
	fields := strings.Fields(out) // "rows cols"
	if len(fields) != 2 {
		return
	}
	if r, err := strconv.Atoi(fields[0]); err == nil && r > 0 {
		rows = r
	}
	if c, err := strconv.Atoi(fields[1]); err == nil && c > 0 {
		cols = c
	}
	return
}
