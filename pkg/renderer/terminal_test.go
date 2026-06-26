package renderer

import (
	"strings"
	"testing"

	miniui "ella.to/microui"
)

// testCtx returns a context wired to a headless terminal renderer.
func testCtx(t *Terminal) *miniui.Context {
	c := miniui.New()
	Connect(c, t)
	st := t.Style()
	c.Style = &st
	return c
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
	bg := miniui.RGBA(0, 0, 0, 255)

	c.Begin()
	if c.BeginWindow("Hi", miniui.Rect{X: 2, Y: 1, W: 20, H: 8}) != 0 {
		c.LayoutRow([]int{-1}, 0)
		c.Button("Go")
		c.EndWindow()
	}
	c.End()
	paint(term, c, bg)
	scr := term.scr

	// The window background color should appear somewhere (distinct from bg).
	wbg := c.Style.Colors[miniui.ColorWindowBG]
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

	// The title text and button label should be present.
	for _, r := range "HiGo" {
		if !findRune(scr, r) {
			t.Errorf("expected rune %q on screen", r)
		}
	}

	// The close icon should be drawn.
	if !findRune(scr, iconGlyph(miniui.IconClose)) {
		t.Errorf("close icon glyph not found")
	}
}

func TestRenderANSINonEmpty(t *testing.T) {
	term := NewHeadless(30, 6)
	c := testCtx(term)
	c.Begin()
	if c.BeginWindow("W", miniui.Rect{X: 0, Y: 0, W: 16, H: 5}) != 0 {
		c.EndWindow()
	}
	c.End()
	paint(term, c, miniui.RGBA(0, 0, 0, 255))

	out := renderANSI(term.scr)
	if !strings.Contains(out, "\x1b[48;2;") {
		t.Errorf("ANSI output missing truecolor background sequences")
	}
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("ANSI output should end with a reset")
	}
}

func TestClipKeepsTextInsidePanel(t *testing.T) {
	// Text far longer than its column must not write past the screen bounds
	// and must be clipped to the window; this mainly asserts no panic and that
	// the clip path is exercised.
	term := NewHeadless(50, 16)
	c := testCtx(term)
	c.Begin()
	if c.BeginWindowEx("L", miniui.Rect{X: 0, Y: 0, W: 20, H: 12}, miniui.OptNoTitle) != 0 {
		c.LayoutRow([]int{-1}, -1)
		c.Text(strings.Repeat("word ", 200))
		c.EndWindow()
	}
	c.End()
	paint(term, c, miniui.RGBA(0, 0, 0, 255))
	scr := term.scr

	// No cell outside the window's right edge (plus border) should contain a
	// non-space glyph from the wrapped text.
	for y := 0; y < scr.h; y++ {
		for x := 25; x < scr.w; x++ {
			if ch := scr.at(x, y).ch; ch != ' ' && ch != 0 {
				t.Fatalf("text leaked outside clip at (%d,%d): %q", x, y, ch)
			}
		}
	}
}
