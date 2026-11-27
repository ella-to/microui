package renderer

import (
	"bytes"
	"strings"
	"testing"
	"unsafe"

	"ella.to/microui"
)

// testCtx returns a context wired to a headless terminal renderer.
func testCtx(t *Terminal) *microui.Context {
	c := microui.New()
	Connect(c, t)
	st := t.Style()
	c.Style = &st
	return c
}

// newTestTerminal returns a terminal that takes the interactive (ANSI diff)
// output path but writes into a buffer instead of a tty.
func newTestTerminal(w, h int, mode ColorMode) (*Terminal, *bytes.Buffer) {
	var out bytes.Buffer
	t := NewHeadless(w, h)
	t.interactive = true
	t.out = &out
	t.enc.mode = mode
	t.fullRedraw = true
	return t, &out
}

// findRune reports whether r appears anywhere on the screen.
func findRune(s *screen, r rune) bool {
	for i := range s.cells {
		if s.cells[i].ch == r {
			return true
		}
	}
	return false
}

func TestPaintWindow(t *testing.T) {
	term := NewHeadless(40, 12)
	c := testCtx(term)
	bg := microui.RGBA(0, 0, 0, 255)

	c.Begin()
	if c.BeginWindow("Hi", microui.Rect{X: 2, Y: 1, W: 20, H: 8}).Active() {
		c.LayoutRow([]int{-1}, 0)
		c.Button("Go")
		c.EndWindow()
	}
	c.End()
	Draw(term, c, bg)
	scr := term.back

	wbg := c.Style.Colors[microui.ColorWindowBG]
	foundWBG := false
	for i := range scr.cells {
		if scr.cells[i].bg == wbg {
			foundWBG = true
			break
		}
	}
	if !foundWBG {
		t.Errorf("window background color %+v not found on screen", wbg)
	}
	for _, r := range "HiGo" {
		if !findRune(scr, r) {
			t.Errorf("expected rune %q on screen", r)
		}
	}
	if !findRune(scr, iconGlyph(microui.IconClose)) {
		t.Errorf("close icon glyph not found")
	}
	if !strings.Contains(term.Text(), "Go") {
		t.Errorf("Text() snapshot missing the button label:\n%s", term.Text())
	}
}

func TestColorModesEmitMatchingSequences(t *testing.T) {
	cases := []struct {
		mode      ColorMode
		want, not string
	}{
		{ColorTrue, "48;2;", "48;5;"},
		{Color256, "48;5;", "48;2;"},
		{Color16, "\x1b[3", "48;"},
	}
	for _, tc := range cases {
		term, out := newTestTerminal(30, 6, tc.mode)
		c := testCtx(term)
		c.Begin()
		if c.BeginWindow("W", microui.Rect{W: 16, H: 5}).Active() {
			c.EndWindow()
		}
		c.End()
		Paint(term, c, microui.RGBA(0, 0, 0, 255))
		s := out.String()
		if !strings.Contains(s, tc.want) || strings.Contains(s, tc.not) {
			t.Errorf("%v mode: output should contain %q and not %q", tc.mode, tc.want, tc.not)
		}
		if !strings.HasPrefix(s, "\x1b[?2026h") || !strings.HasSuffix(s, "\x1b[0m\x1b[?2026l") {
			t.Errorf("%v mode: frame not wrapped in a synchronized update", tc.mode)
		}
	}
}

func TestPresentWritesOnlyChanges(t *testing.T) {
	term, out := newTestTerminal(40, 10, ColorTrue)
	label := "hello"
	frame := func() {
		term.Clear(microui.RGBA(0, 0, 0, 255))
		term.DrawText(nil, label, microui.Vec2{X: 3, Y: 4}, microui.RGBA(255, 255, 255, 255))
		out.Reset()
		term.Present()
	}

	frame()
	if out.Len() < 40*10 {
		t.Fatalf("first frame wrote %d bytes, want a full repaint", out.Len())
	}

	frame()
	if out.Len() != 0 {
		t.Fatalf("identical frame wrote %q, want nothing", out.String())
	}

	label = "help!"
	frame()
	s := out.String()
	if !strings.Contains(s, "p!") || strings.Contains(s, "hel") {
		t.Fatalf("one-word change wrote %q, want just the changed cells", s)
	}
	if !strings.Contains(s, "\x1b[5;7H") {
		t.Fatalf("changed cells not positioned at row 5 col 7: %q", s)
	}
}

func TestRedrawRepaintsEverything(t *testing.T) {
	term, out := newTestTerminal(20, 4, Color256)
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.Present()
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.Redraw()
	out.Reset()
	term.Present()
	if !strings.Contains(out.String(), "\x1b[2J") || out.Len() < 20*4 {
		t.Fatalf("Redraw did not force a full repaint: %q", out.String())
	}
}

func TestAnsi256(t *testing.T) {
	cases := []struct {
		c    microui.Color
		want int
	}{
		{microui.RGBA(0, 0, 0, 255), 16},        // cube black
		{microui.RGBA(255, 255, 255, 255), 231}, // cube white
		{microui.RGBA(255, 0, 0, 255), 196},     // pure red
		{microui.RGBA(0, 255, 0, 255), 46},      // pure green
		{microui.RGBA(0, 0, 255, 255), 21},      // pure blue
		{microui.RGBA(8, 8, 8, 255), 232},       // darkest gray ramp entry
		{microui.RGBA(128, 128, 128, 255), 244}, // mid gray -> ramp, not cube
	}
	for _, tc := range cases {
		if got := ansi256(tc.c); got != tc.want {
			t.Errorf("ansi256(%d,%d,%d) = %d, want %d", tc.c.R, tc.c.G, tc.c.B, got, tc.want)
		}
	}
}

func TestAnsi16(t *testing.T) {
	if got := ansi16(microui.Hex(0xff0000)); got != 9 {
		t.Errorf("bright red = %d, want 9", got)
	}
	if got := ansi16(microui.Hex(0x101010)); got != 0 {
		t.Errorf("near black = %d, want 0", got)
	}
}

func TestClipKeepsTextInsidePanel(t *testing.T) {
	// Text far longer than its column must not write past the screen bounds
	// and must be clipped to the window.
	term := NewHeadless(50, 16)
	c := testCtx(term)
	c.Begin()
	if c.BeginWindowEx("L", microui.Rect{W: 20, H: 12}, microui.OptNoTitle).Active() {
		c.LayoutRow([]int{-1}, -1)
		c.Text(strings.Repeat("word ", 200))
		c.EndWindow()
	}
	c.End()
	Draw(term, c, microui.RGBA(0, 0, 0, 255))
	scr := term.back
	for y := range scr.h {
		for x := 25; x < scr.w; x++ {
			if ch := scr.at(x, y).ch; ch != ' ' && ch != 0 {
				t.Fatalf("text leaked outside clip at (%d,%d): %q", x, y, ch)
			}
		}
	}
}

func TestStringWidth(t *testing.T) {
	cases := map[string]int{
		"":         0,
		"abc":      3,
		"日本語":      6,
		"héllo":    5,
		"é":       1, // combining acute accent
		"🙂":        2,
		"a\tb":     2, // control characters take no columns
		"✓ ✕ ▶ ▼":  7,
		"한국어 text": 11,
	}
	for s, want := range cases {
		if got := StringWidth(s); got != want {
			t.Errorf("StringWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestWideTextOccupiesTwoCells(t *testing.T) {
	term, out := newTestTerminal(10, 1, ColorTrue)
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.DrawText(nil, "a日b", microui.Vec2{X: 0, Y: 0}, microui.RGBA(255, 255, 255, 255))
	if got := term.Text(); got != "a日b      \n" {
		t.Fatalf("Text() = %q", got)
	}
	term.Present()
	if n := strings.Count(out.String(), "日"); n != 1 {
		t.Fatalf("wide rune written %d times, want once", n)
	}
	// overwriting the right half of the wide glyph must blank its left half
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.DrawText(nil, "a日b", microui.Vec2{X: 0, Y: 0}, microui.RGBA(255, 255, 255, 255))
	term.DrawText(nil, "X", microui.Vec2{X: 2, Y: 0}, microui.RGBA(255, 255, 255, 255))
	if got := term.Text(); got != "a Xb      \n" {
		t.Fatalf("half-overwritten wide glyph: Text() = %q", got)
	}
	// replacing a wide glyph frame with a narrow frame must repaint the freed
	// second column cleanly
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.DrawText(nil, "a日b", microui.Vec2{X: 0, Y: 0}, microui.RGBA(255, 255, 255, 255))
	term.Present()
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.DrawText(nil, "aXYb", microui.Vec2{X: 0, Y: 0}, microui.RGBA(255, 255, 255, 255))
	out.Reset()
	term.Present()
	if !strings.Contains(out.String(), "XY") {
		t.Fatalf("wide-to-narrow transition did not emit replacement cells: %q", out.String())
	}
}

func TestWideTextClippedAtEdge(t *testing.T) {
	term := NewHeadless(10, 1)
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.SetClip(microui.Rect{W: 2, H: 1})
	term.DrawText(nil, "a日", microui.Vec2{}, microui.RGBA(255, 255, 255, 255))
	if got := term.Text(); got != "a         \n" {
		t.Fatalf("a wide glyph straddling the clip edge must be dropped: %q", got)
	}
}

func TestTranslucentFillBlends(t *testing.T) {
	term := NewHeadless(4, 1)
	term.Clear(microui.RGBA(0, 0, 0, 255))
	term.DrawText(nil, "ab", microui.Vec2{}, microui.RGBA(200, 200, 200, 255))
	term.FillRect(microui.Rect{W: 4, H: 1}, microui.RGBA(255, 255, 255, 128))
	c := term.back.at(0, 0)
	if c.ch != 'a' {
		t.Fatalf("translucent fill erased the glyph: %q", c.ch)
	}
	if c.bg.R < 120 || c.bg.R > 135 {
		t.Fatalf("background after 50%% white over black = %d, want ~128", c.bg.R)
	}
	if c.fg.R <= 200 {
		t.Fatalf("glyph color should be tinted toward white, got %d", c.fg.R)
	}
}

func TestCellHasNoPadding(t *testing.T) {
	// cellBytes compares rows as raw memory, which is only sound if cell
	// has no padding bytes.
	if got := unsafe.Sizeof(cell{}); got != 12 {
		t.Fatalf("cell is %d bytes, want 12 (no padding)", got)
	}
}

func TestPresentRewritesShortGapsInsteadOfMoving(t *testing.T) {
	term, out := newTestTerminal(20, 1, ColorTrue)
	frame := func(s string) {
		term.Clear(microui.RGBA(0, 0, 0, 255))
		term.DrawText(nil, s, microui.Vec2{}, microui.RGBA(255, 255, 255, 255))
		out.Reset()
		term.Present()
	}
	frame("abcdef")
	frame("Xbcdeg") // first and last change, four unchanged cells between
	s := out.String()
	if strings.Count(s, "H") != 1 || !strings.Contains(s, "Xbcdeg") {
		t.Fatalf("expected one cursor move and the gap rewritten, got %q", s)
	}
}

func TestDrawingOffScreenIsSafe(t *testing.T) {
	term := NewHeadless(10, 4)
	term.Clear(microui.RGBA(0, 0, 0, 255))
	white := microui.RGBA(255, 255, 255, 255)
	for _, r := range []microui.Rect{
		{X: 12, Y: 1, W: 5, H: 2},    // past the right edge
		{X: 12, Y: 3, W: 5, H: 1},    // past the right edge on the last row
		{X: -9, Y: 1, W: 5, H: 2},    // past the left edge
		{X: 2, Y: 7, W: 5, H: 2},     // below
		{X: 2, Y: -5, W: 5, H: 2},    // above
		{X: 2, Y: 1, W: -3, H: 2},    // negative size
		{X: 0, Y: 0, W: 100, H: 100}, // covers everything
	} {
		term.FillRect(r, white)
		term.FillRect(r, white.WithAlpha(100))
		term.DrawText(nil, "日本語テキスト", microui.Vec2{X: r.X, Y: r.Y}, white)
		term.DrawIcon(microui.IconCheck, r, white)
	}
}
