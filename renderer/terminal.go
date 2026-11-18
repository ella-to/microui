package renderer

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"ella.to/microui"
)

// DefaultBackground is the color empty cells are cleared to. Terminal.BG starts
// here and may be changed at any time (e.g. from a slider in the UI).
var DefaultBackground = microui.RGBA(40, 44, 52, 255)

// Terminal is the default microui Driver: it renders the command list to an
// ANSI terminal (truecolor when advertised via COLORTERM, 256-color otherwise),
// treating each character cell as one "pixel", drawing
// text in the terminal font, and reading input from SGR mouse reporting plus
// the keyboard. It uses only the standard library (raw mode is set via stty),
// so there is no cgo and no third-party dependency.
//
// Create one with NewTerminal and drive it with Run. When stdout/stdin is not a
// terminal, NewTerminal returns a headless Terminal that paints to an in-memory
// surface and prints a single plain-text frame — handy for piping and tests.
type Terminal struct {
	// BG is the background color used to clear the surface each frame. Exported
	// so a UI can recolor the backdrop live; defaults to DefaultBackground.
	BG microui.Color

	scr  *screen
	clip microui.Rect

	interactive bool
	truecolor   bool   // emit 24-bit color; otherwise quantize to the 256-color palette
	savedState  string // `stty -g` output, restored on Close
	in          chan []byte
	pending     []byte
	winCh       chan os.Signal
	framesDrawn int
	closed      bool
	closeOnce   sync.Once
}

// NewTerminal creates a terminal renderer. If stdin/stdout is a real terminal
// it switches to raw mode, enables the alternate screen and mouse reporting,
// and reads live input. Otherwise it returns a headless 80x24 renderer that
// emits a single plain-text frame (see Run / Present).
func NewTerminal() (*Terminal, error) {
	if !isInteractive() {
		return NewHeadless(80, 24), nil
	}
	t := &Terminal{interactive: true, truecolor: supportsTruecolor(), BG: DefaultBackground}
	if err := t.enterRaw(); err != nil {
		return nil, err
	}
	cols, rows := t.termSize()
	t.scr = newScreen(cols, rows)
	t.clip = t.scr.bounds()

	t.in = make(chan []byte, 64)
	go t.readInput()

	t.winCh = make(chan os.Signal, 1)
	signal.Notify(t.winCh, syscall.SIGWINCH)
	t.installExitSignals()
	return t, nil
}

// NewHeadless creates a non-interactive terminal renderer drawing to an
// in-memory w*h surface. Present writes the surface as plain text. It is used
// for tests and for piping a single rendered frame.
func NewHeadless(w, h int) *Terminal {
	return &Terminal{
		scr:  newScreen(w, h),
		clip: microui.Rect{X: 0, Y: 0, W: w, H: h},
		BG:   DefaultBackground,
	}
}

// ---- Driver / Renderer interface --------------------------------------------

// TextWidth returns the number of cells a string occupies (one per rune).
func (t *Terminal) TextWidth(_ microui.Font, s string) int {
	return utf8.RuneCountInString(s)
}

// TextHeight returns the height of a line of text: one cell.
func (t *Terminal) TextHeight(_ microui.Font) int { return 1 }

// Size reports the surface size in cells.
func (t *Terminal) Size() (w, h int) { return t.scr.w, t.scr.h }

// Style returns a microui style with metrics tuned for character-cell units.
func (t *Terminal) Style() microui.Style { return terminalStyle() }

// Background returns the current clear color.
func (t *Terminal) Background() microui.Color { return t.BG }

// Clear fills the whole surface with bg and resets the clip region.
func (t *Terminal) Clear(bg microui.Color) {
	for i := range t.scr.cells {
		t.scr.cells[i] = cell{ch: ' ', fg: bg, bg: bg}
	}
	t.clip = t.scr.bounds()
}

// SetClip restricts following draws to rect (intersected with the surface).
func (t *Terminal) SetClip(rect microui.Rect) {
	t.clip = rectIntersect(rect, t.scr.bounds())
}

// FillRect paints rect with color as the cell background.
func (t *Terminal) FillRect(rect microui.Rect, color microui.Color) {
	fillRect(t.scr, rect, t.clip, color)
}

// DrawText writes str starting at pos, one rune per cell, on top of whatever
// background is already there.
func (t *Terminal) DrawText(_ microui.Font, str string, pos microui.Vec2, color microui.Color) {
	drawText(t.scr, str, pos, t.clip, color)
}

// DrawIcon draws a single glyph centered within rect.
func (t *Terminal) DrawIcon(id int, rect microui.Rect, color microui.Color) {
	drawIcon(t.scr, id, rect, t.clip, color)
}

// Present flushes the frame: an ANSI string in interactive mode (truecolor
// when the terminal supports it, 256-color otherwise), or a plain-text dump.
func (t *Terminal) Present() {
	if t.interactive {
		os.Stdout.WriteString(renderANSI(t.scr, t.truecolor))
	} else {
		os.Stdout.WriteString(plainText(t.scr))
	}
	t.framesDrawn++
}

// Poll drains pending input into ctx (handling resize) and reports whether to
// quit. In headless mode it returns true once a single frame has been drawn.
func (t *Terminal) Poll(ctx *microui.Context) bool {
	if !t.interactive {
		return t.framesDrawn > 0
	}

	// apply a pending resize
	select {
	case <-t.winCh:
		cols, rows := t.termSize()
		t.scr.resize(cols, rows)
	default:
	}

	// drain everything available without blocking
drain:
	for {
		select {
		case b, ok := <-t.in:
			if !ok {
				t.closed = true
				break drain
			}
			t.pending = append(t.pending, b...)
		default:
			break drain
		}
	}
	if t.closed {
		return true
	}

	quit := false
	consumed := parseInput(ctx, t.pending, &quit)
	t.pending = append(t.pending[:0], t.pending[consumed:]...)
	return quit
}

// Close restores the terminal to its original state. Safe to call repeatedly.
func (t *Terminal) Close() error {
	if !t.interactive {
		return nil
	}
	t.closeOnce.Do(t.leaveRaw)
	return nil
}

// readInput pumps stdin into the input channel until EOF/error.
func (t *Terminal) readInput() {
	buf := make([]byte, 512)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			t.in <- b
		}
		if err != nil {
			close(t.in)
			return
		}
	}
}

// installExitSignals restores the terminal and exits on SIGINT/SIGTERM (so the
// terminal is not left in raw mode if the process is killed). In raw mode
// Ctrl-C arrives as a byte instead, handled by parseInput.
func (t *Terminal) installExitSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		t.Close()
		os.Exit(0)
	}()
}

// ---- screen surface ---------------------------------------------------------

// cell is one character cell of the virtual terminal screen.
type cell struct {
	ch rune
	fg microui.Color
	bg microui.Color
}

// screen is a w*h grid of cells that draw commands are composited into.
type screen struct {
	w, h  int
	cells []cell
}

func newScreen(w, h int) *screen {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return &screen{w: w, h: h, cells: make([]cell, w*h)}
}

func (s *screen) resize(w, h int) {
	if w == s.w && h == s.h {
		return
	}
	ns := newScreen(w, h)
	s.w, s.h, s.cells = ns.w, ns.h, ns.cells
}

func (s *screen) at(x, y int) *cell { return &s.cells[y*s.w+x] }

func (s *screen) inBounds(x, y int) bool { return x >= 0 && x < s.w && y >= 0 && y < s.h }

func (s *screen) bounds() microui.Rect { return microui.Rect{X: 0, Y: 0, W: s.w, H: s.h} }

// rectIntersect returns the overlap of two rectangles.
func rectIntersect(a, b microui.Rect) microui.Rect {
	x1 := max(a.X, b.X)
	y1 := max(a.Y, b.Y)
	x2 := min(a.X+a.W, b.X+b.W)
	y2 := min(a.Y+a.H, b.Y+b.H)
	if x2 < x1 {
		x2 = x1
	}
	if y2 < y1 {
		y2 = y1
	}
	return microui.Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

// contains reports whether (x,y) lies within rect.
func contains(rect microui.Rect, x, y int) bool {
	return x >= rect.X && x < rect.X+rect.W && y >= rect.Y && y < rect.Y+rect.H
}

// iconGlyph maps a microui icon id to a single display rune.
func iconGlyph(id int) rune {
	switch id {
	case microui.IconClose:
		return '✕'
	case microui.IconCheck:
		return '✓'
	case microui.IconCollapsed:
		return '▶'
	case microui.IconExpanded:
		return '▼'
	default:
		return '?'
	}
}

// fillRect paints rect (clipped to clip and the screen) with color as the cell
// background.
func fillRect(s *screen, rect, clip microui.Rect, color microui.Color) {
	r := rectIntersect(rectIntersect(rect, clip), s.bounds())
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			c := s.at(x, y)
			c.bg = color
			c.fg = color
			c.ch = ' '
		}
	}
}

// drawText writes str starting at pos, one rune per cell, keeping each cell's
// existing background so text appears on top of frames.
func drawText(s *screen, str string, pos microui.Vec2, clip microui.Rect, color microui.Color) {
	x := pos.X
	for _, r := range str {
		if contains(clip, x, pos.Y) && s.inBounds(x, pos.Y) {
			c := s.at(x, pos.Y)
			c.ch = r
			c.fg = color
		}
		x++
	}
}

// drawIcon draws a single glyph centered within rect.
func drawIcon(s *screen, id int, rect, clip microui.Rect, color microui.Color) {
	x := rect.X + (rect.W-1)/2
	y := rect.Y + (rect.H-1)/2
	if contains(clip, x, y) && s.inBounds(x, y) {
		c := s.at(x, y)
		c.ch = iconGlyph(id)
		c.fg = color
	}
}

// supportsTruecolor reports whether the terminal advertises 24-bit color via
// COLORTERM (the de-facto convention set by iTerm2, Ghostty, Kitty, WezTerm,
// VS Code, ...). Terminals that don't set it — notably Apple's Terminal.app —
// get the 256-color fallback; exporting COLORTERM=truecolor forces 24-bit.
func supportsTruecolor() bool {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	return strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit")
}

// ansi256 returns the xterm-256 palette index nearest to c, choosing between
// the 6x6x6 color cube (16-231) and the grayscale ramp (232-255).
func ansi256(c microui.Color) int {
	// Nearest cube channel: levels are 0, 95, 135, 175, 215, 255.
	cubeIdx := func(v uint8) int {
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return (int(v) - 35) / 40
	}
	cubeVal := func(i int) int {
		if i == 0 {
			return 0
		}
		return 55 + 40*i
	}
	qr, qg, qb := cubeIdx(c.R), cubeIdx(c.G), cubeIdx(c.B)
	cr, cg, cb := cubeVal(qr), cubeVal(qg), cubeVal(qb)

	// Nearest gray ramp entry: levels are 8, 18, ..., 238.
	avg := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi := (avg - 3) / 10
	if gi < 0 {
		gi = 0
	} else if gi > 23 {
		gi = 23
	}
	gv := 8 + 10*gi

	distSq := func(r, g, b int) int {
		dr, dg, db := r-int(c.R), g-int(c.G), b-int(c.B)
		return dr*dr + dg*dg + db*db
	}
	if distSq(gv, gv, gv) < distSq(cr, cg, cb) {
		return 232 + gi
	}
	return 16 + 36*qr + 6*qg + qb
}

// renderANSI serializes the screen to an ANSI string — 24-bit color when
// truecolor is set, nearest xterm-256 colors otherwise — positioning the
// cursor explicitly at the start of each row to avoid auto-wrap artifacts.
func renderANSI(s *screen, truecolor bool) string {
	var b strings.Builder
	var fg, bg microui.Color
	haveColor := false
	for y := 0; y < s.h; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H", y+1)
		for x := 0; x < s.w; x++ {
			c := s.at(x, y)
			if !haveColor || c.fg != fg || c.bg != bg {
				if truecolor {
					fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm",
						c.fg.R, c.fg.G, c.fg.B, c.bg.R, c.bg.G, c.bg.B)
				} else {
					fmt.Fprintf(&b, "\x1b[38;5;%dm\x1b[48;5;%dm", ansi256(c.fg), ansi256(c.bg))
				}
				fg, bg, haveColor = c.fg, c.bg, true
			}
			ch := c.ch
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// plainText renders the screen as rows of characters with no styling.
func plainText(s *screen) string {
	var b strings.Builder
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			ch := s.at(x, y).ch
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// terminalStyle tunes microui's metrics for character-cell units.
func terminalStyle() microui.Style {
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
	return st
}
