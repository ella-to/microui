package renderer

import (
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ella.to/microui"
)

// DefaultBackground is the color empty cells are cleared to. Terminal.BG starts
// here and may be changed at any time (e.g. from a slider in the UI).
var DefaultBackground = microui.RGBA(40, 44, 52, 255)

// escTimeout is how long a lone ESC byte waits for the rest of an escape
// sequence before it is reported as the Escape key.
const escTimeout = 40 * time.Millisecond

// Terminal is the default microui Driver: it renders the command list to an
// ANSI terminal, treating each character cell as one "pixel" and drawing text
// in the terminal font, and reads keyboard, mouse and paste input. It uses
// only the standard library (no cgo, no dependencies) and works on Unix-like
// systems and on Windows 10+ consoles.
//
// Output is double-buffered: each Present writes only the cells that changed
// since the previous frame, in a single write wrapped in a synchronized
// update, so an idle UI costs almost nothing and redraws never tear.
//
// Create one with NewTerminal and drive it with Run. When stdin or stdout is
// not a terminal, NewTerminal returns a headless Terminal that paints to an
// in-memory surface and prints a single plain-text frame — handy for piping
// and tests.
type Terminal struct {
	// BG is the background color used to clear the surface each frame. Exported
	// so a UI can recolor the backdrop live; defaults to DefaultBackground.
	BG microui.Color

	back  *screen // the frame being painted
	front *screen // what the terminal currently shows
	clip  microui.Rect

	cfg         termConfig
	interactive bool
	out         io.Writer
	enc         encoder
	fullRedraw  bool
	framesDrawn int

	// input
	in       <-chan []byte
	pending  []byte
	parser   inputParser
	heldMods microui.Key // modifiers synthesized for a mouse click, released next Poll
	wake     chan struct{}
	escTimer *time.Timer

	quit      atomic.Bool // set by signals
	resized   atomic.Bool // set by SIGWINCH
	errMu     sync.Mutex
	err       error
	closeOnce sync.Once
	sys       sysState // platform-specific saved terminal state
}

// termConfig collects the TerminalOptions.
type termConfig struct {
	colors  ColorMode
	mouse   bool
	suspend bool
}

// TerminalOption configures NewTerminal.
type TerminalOption func(*termConfig)

// WithColorMode overrides color detection (see DetectColorMode).
func WithColorMode(m ColorMode) TerminalOption {
	return func(c *termConfig) { c.colors = m }
}

// WithMouse enables or disables mouse reporting (default on). Without it the
// terminal keeps its native text selection, and the UI is keyboard-only.
func WithMouse(on bool) TerminalOption {
	return func(c *termConfig) { c.mouse = on }
}

// WithSuspend controls whether Ctrl-Z suspends the program to the shell
// (default on, Unix only). Disable it to receive Ctrl-Z as an ordinary key.
func WithSuspend(on bool) TerminalOption {
	return func(c *termConfig) { c.suspend = on }
}

// NewTerminal creates a terminal renderer. If stdin and stdout are a real
// terminal it switches to raw mode, enables the alternate screen, mouse and
// bracketed-paste reporting, and reads live input. Otherwise it returns a
// headless 80x24 renderer that emits a single plain-text frame (see
// NewHeadless).
//
// The terminal is restored by Close (Run calls it, even on panic) and on
// SIGINT/SIGTERM/SIGHUP, which make Poll report quit.
func NewTerminal(opts ...TerminalOption) (*Terminal, error) {
	cfg := termConfig{mouse: true, suspend: true}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.colors == ColorAuto {
		cfg.colors = DetectColorMode()
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		t := NewHeadless(80, 24)
		t.cfg = cfg
		return t, nil
	}

	t := &Terminal{
		BG:          DefaultBackground,
		cfg:         cfg,
		interactive: true,
		out:         os.Stdout,
		wake:        make(chan struct{}, 1),
		fullRedraw:  true,
	}
	t.enc.mode = cfg.colors
	if err := t.enterRaw(); err != nil {
		return nil, err
	}
	cols, rows := t.size()
	t.back, t.front = newScreen(cols, rows), newScreen(cols, rows)
	t.clip = t.back.bounds()

	t.watchSignals()
	t.in = attachStdin(t)
	return t, nil
}

// NewHeadless creates a non-interactive terminal renderer drawing to an
// in-memory w*h surface. Present writes the surface to stdout as plain text
// and Poll quits after the first frame. Use it for tests (see Text) and for
// piping a single rendered frame.
func NewHeadless(w, h int) *Terminal {
	t := &Terminal{
		BG:    DefaultBackground,
		back:  newScreen(w, h),
		front: newScreen(w, h),
		out:   os.Stdout,
		cfg:   termConfig{colors: ColorTrue},
	}
	t.clip = t.back.bounds()
	return t
}

// ---- Driver / Renderer interface --------------------------------------------

// TextWidth returns the number of cells a string occupies (see StringWidth).
func (t *Terminal) TextWidth(_ microui.Font, s string) int { return StringWidth(s) }

// TextHeight returns the height of a line of text: one cell.
func (t *Terminal) TextHeight(_ microui.Font) int { return 1 }

// Size reports the surface size in cells.
func (t *Terminal) Size() (w, h int) { return t.back.w, t.back.h }

// Style returns a microui style with metrics tuned for character-cell units.
func (t *Terminal) Style() microui.Style { return TerminalStyle() }

// Background returns the current clear color.
func (t *Terminal) Background() microui.Color { return t.BG }

// Clear fills the whole surface with bg and resets the clip region.
func (t *Terminal) Clear(bg microui.Color) {
	t.back.clear(bg.WithAlpha(255))
	t.clip = t.back.bounds()
}

// SetClip restricts following draws to rect (intersected with the surface).
func (t *Terminal) SetClip(rect microui.Rect) {
	t.clip = rect.Intersect(t.back.bounds())
}

// FillRect paints rect with color as the cell background, alpha-blending
// translucent colors over what is already there.
func (t *Terminal) FillRect(rect microui.Rect, color microui.Color) {
	t.back.fillRectClipped(rect, t.clip, color)
}

// DrawText writes str starting at pos on top of whatever background is
// already there. Wide runes take two cells.
func (t *Terminal) DrawText(_ microui.Font, str string, pos microui.Vec2, color microui.Color) {
	t.back.drawTextClipped(str, pos, t.clip, color)
}

// DrawIcon draws a single glyph centered within rect.
func (t *Terminal) DrawIcon(id microui.Icon, rect microui.Rect, color microui.Color) {
	t.back.drawIconClipped(id, rect, t.clip, color)
}

// Text returns the current surface as plain text, one line per row. It is
// meant for tests: paint a frame into a NewHeadless terminal and assert on
// what a user would see.
func (t *Terminal) Text() string {
	t.back.normalize()
	return t.back.plainText()
}

// Present flushes the frame. Interactively it writes only the cells that
// changed since the previous frame; headless it prints the surface as plain
// text.
func (t *Terminal) Present() {
	t.framesDrawn++
	t.back.normalize()
	if !t.interactive {
		io.WriteString(t.out, t.back.plainText())
		return
	}

	e := &t.enc
	e.reset()
	e.str("\x1b[?2026h") // begin synchronized update
	mark := len(e.buf)
	if t.fullRedraw {
		e.str("\x1b[0m\x1b[2J")
	}
	e.encodeDiff(t.front, t.back, t.fullRedraw)
	if len(e.buf) == mark {
		// nothing changed: skip the write entirely
		t.back, t.front = t.front, t.back
		return
	}
	e.str("\x1b[0m\x1b[?2026l") // end synchronized update
	if _, err := t.out.Write(e.buf); err != nil {
		t.fail(err)
	}
	t.fullRedraw = false
	t.back, t.front = t.front, t.back
}

// Redraw forces the next Present to repaint every cell, e.g. after something
// else wrote to the terminal. Ctrl-L does this too.
func (t *Terminal) Redraw() { t.fullRedraw = true }

// Wake implements Waker: it fires whenever input arrives, so Run redraws
// immediately instead of waiting for its next tick.
func (t *Terminal) Wake() <-chan struct{} { return t.wake }

// Err implements Errer: it reports an input or output failure that ended the
// session, or nil after a normal quit.
func (t *Terminal) Err() error {
	t.errMu.Lock()
	defer t.errMu.Unlock()
	return t.err
}

func (t *Terminal) fail(err error) {
	t.errMu.Lock()
	if t.err == nil {
		t.err = err
	}
	t.errMu.Unlock()
	t.quit.Store(true)
	t.signalWake()
}

// signalWake nudges Run without blocking.
func (t *Terminal) signalWake() {
	if t.wake == nil {
		return
	}
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Poll drains pending input into ctx (handling resize) and reports whether to
// quit. Input is fed one coherent batch per frame: when the pending bytes hold
// events that one frame cannot represent faithfully (two key presses, a key
// after typed text, a click after a click), Poll stops at the boundary and
// wakes Run for another frame right away, so fast typing and pastes are never
// garbled. In headless mode it returns true once a single frame was drawn.
func (t *Terminal) Poll(ctx *microui.Context) bool {
	if !t.interactive {
		return t.framesDrawn > 0
	}
	if t.heldMods != 0 {
		ctx.InputKeyUp(t.heldMods)
		t.heldMods = 0
	}
	if t.quit.Load() {
		return true
	}
	t.checkResize()

	// drain everything the reader goroutine has queued, without blocking
	for drained := false; !drained; {
		select {
		case b, ok := <-t.in:
			if !ok {
				if err := stdinErr(); err != nil {
					t.fail(err)
				}
				t.quit.Store(true)
				return true
			}
			t.pending = append(t.pending, b...)
		default:
			drained = true
		}
	}

	res := t.parser.parse(ctx, t.pending, time.Now())
	t.pending = append(t.pending[:0], t.pending[res.n:]...)
	t.heldMods = res.heldMods

	switch {
	case res.quit:
		return true
	case res.redraw:
		t.Redraw()
	case res.suspend && t.cfg.suspend:
		t.suspend()
	}
	switch {
	case res.split:
		t.signalWake() // more complete input is waiting: run another frame now
	case res.escWait:
		t.armEscTimer()
	}
	return false
}

// armEscTimer wakes Run once a pending lone ESC has waited long enough to be
// reported as the Escape key, even at a very low frame rate.
func (t *Terminal) armEscTimer() {
	if t.escTimer == nil {
		t.escTimer = time.AfterFunc(escTimeout, t.signalWake)
		return
	}
	t.escTimer.Reset(escTimeout)
}

// checkResize applies a pending terminal size change.
func (t *Terminal) checkResize() {
	if !t.resized.Swap(false) && !t.pollResize() {
		return
	}
	cols, rows := t.size()
	if cols == t.back.w && rows == t.back.h {
		return
	}
	t.back.resize(cols, rows)
	t.front.resize(cols, rows)
	t.clip = t.back.bounds()
	t.fullRedraw = true
}

// Close restores the terminal to its original state. Safe to call repeatedly.
// Another Terminal may be opened afterwards. Note that stdin keeps being read
// in the background (a blocking read cannot be cancelled portably), so read
// further user input through a new Terminal rather than from os.Stdin.
func (t *Terminal) Close() error {
	if !t.interactive {
		return nil
	}
	t.closeOnce.Do(func() {
		t.stopSignals()
		if t.escTimer != nil {
			t.escTimer.Stop()
		}
		t.leaveRaw()
	})
	return nil
}

// stdinPump reads stdin on a single goroutine for the life of the process. A
// blocking read cannot be interrupted, so a per-Terminal reader would outlive
// Close and steal input from the next Terminal; one shared pump hands bytes to
// whichever Terminal is current instead.
var stdinPump struct {
	once sync.Once
	ch   chan []byte // closed on EOF or error
	mu   sync.Mutex
	err  error         // the read error that closed ch (nil for EOF)
	wake chan struct{} // the current Terminal's wake channel
}

// attachStdin makes t the receiver of stdin wake-ups and returns the shared
// input channel, starting the pump on first use.
func attachStdin(t *Terminal) <-chan []byte {
	stdinPump.mu.Lock()
	stdinPump.wake = t.wake
	stdinPump.mu.Unlock()
	stdinPump.once.Do(func() {
		stdinPump.ch = make(chan []byte, 64)
		go pumpStdin()
	})
	return stdinPump.ch
}

func pumpStdin() {
	notify := func() {
		stdinPump.mu.Lock()
		w := stdinPump.wake
		stdinPump.mu.Unlock()
		if w != nil {
			select {
			case w <- struct{}{}:
			default:
			}
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			stdinPump.ch <- append([]byte(nil), buf[:n]...)
			notify()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				stdinPump.mu.Lock()
				stdinPump.err = err
				stdinPump.mu.Unlock()
			}
			close(stdinPump.ch)
			notify()
			return
		}
	}
}

// stdinErr reports the error that ended stdin, if any.
func stdinErr() error {
	stdinPump.mu.Lock()
	defer stdinPump.mu.Unlock()
	return stdinPump.err
}

// Escape sequences toggling the terminal modes the renderer relies on.
const (
	seqEnter = "\x1b[?1049h" + // alternate screen
		"\x1b[?25l" + // hide cursor
		"\x1b[?7l" + // no autowrap
		"\x1b[?2004h" + // bracketed paste
		"\x1b[2J"
	seqLeave = "\x1b[0m" +
		"\x1b[?2004l" +
		"\x1b[?7h" +
		"\x1b[?25h" +
		"\x1b[?1049l"
	seqMouseOn  = "\x1b[?1003h\x1b[?1006h" // all-motion tracking, SGR encoding
	seqMouseOff = "\x1b[?1003l\x1b[?1006l"
)

// enterModes switches on the alternate screen and input reporting.
func (t *Terminal) enterModes() {
	s := seqEnter
	if t.cfg.mouse {
		s += seqMouseOn
	}
	io.WriteString(t.out, s)
	t.fullRedraw = true
}

// leaveModes undoes enterModes.
func (t *Terminal) leaveModes() {
	s := seqLeave
	if t.cfg.mouse {
		s = seqMouseOff + s
	}
	io.WriteString(t.out, s)
}

// TerminalStyle returns a microui style with metrics tuned for character-cell
// units: one-cell padding, spacing and title bar.
func TerminalStyle() microui.Style {
	st := microui.DefaultStyle()
	st.Size = microui.Vec2{X: 10, Y: 1}
	st.Padding = 1
	st.Spacing = 1
	st.Indent = 2
	st.TitleHeight = 1
	st.ScrollbarSize = 1
	st.ThumbSize = 1
	// In cell units microui's default 96x64 minimum is enormous; keep windows
	// resizable down to a small, usable size.
	st.MinWidth = 16
	st.MinHeight = 4
	// The title bar is 1 cell tall, so a title-height resize handle would be a
	// single corner cell — nearly impossible to grab. Use a larger grab zone.
	st.ResizeHandle = 3
	// Headers and tree nodes take a single row instead of the 3-row default
	// control height, so trees and sectioned windows stay compact.
	st.HeaderHeight = 1
	return st
}
