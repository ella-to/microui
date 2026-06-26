package microui

import (
	"hash/fnv"
	"testing"
	"unicode/utf8"
)

// newTestCtx returns a context wired with a simple monospace text model:
// width == rune count, height == 1. This lets us reason about layout in
// "cell" units.
func newTestCtx() *Context {
	c := New()
	c.TextWidth = func(_ Font, s string) int { return utf8.RuneCountInString(s) }
	c.TextHeight = func(_ Font) int { return 1 }
	return c
}

func TestHashMatchesFNV1a(t *testing.T) {
	// microui uses standard 32-bit FNV-1a (offset 2166136261, prime 16777619)
	// seeded from the top of the id stack. With an empty stack the seed is the
	// FNV offset basis, so GetID must equal a plain FNV-1a of the data.
	for _, s := range []string{"", "a", "hello", "Demo Window", "!scrollbary"} {
		h := fnv.New32a()
		h.Write([]byte(s))
		want := ID(h.Sum32())

		c := newTestCtx()
		got := c.GetID([]byte(s))
		if got != want {
			t.Errorf("GetID(%q) = %#x, want FNV-1a %#x", s, got, want)
		}
	}
}

func TestHashStackScoping(t *testing.T) {
	c := newTestCtx()
	a := c.GetID([]byte("child"))
	c.PushID([]byte("parent"))
	b := c.GetID([]byte("child"))
	c.PopID()
	if a == b {
		t.Fatalf("expected scoped id to differ: %#x", a)
	}
	// after popping, scope returns to the original
	if got := c.GetID([]byte("child")); got != a {
		t.Fatalf("id after pop = %#x, want %#x", got, a)
	}
}

func TestEmptyFrame(t *testing.T) {
	c := newTestCtx()
	c.Begin()
	c.End()
	n := 0
	for range c.Commands() {
		n++
	}
	if n != 0 {
		t.Fatalf("empty frame produced %d commands, want 0", n)
	}
}

func TestUnbalancedStackPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on unbalanced container stack")
		}
	}()
	c := newTestCtx()
	c.Begin()
	c.BeginWindow("w", Rect{X: 0, Y: 0, W: 50, H: 50}) // never closed
	c.End()
}

func TestCommandsSkipJumps(t *testing.T) {
	c := newTestCtx()
	c.Begin()
	if c.BeginWindow("w", Rect{X: 0, Y: 0, W: 80, H: 60}) != 0 {
		c.Button("ok")
		c.EndWindow()
	}
	c.End()
	for cmd := range c.Commands() {
		if cmd.Type == CommandJump {
			t.Fatalf("Commands yielded an internal jump command")
		}
	}
}

// runFrame renders one frame's worth of the given UI closure.
func runFrame(c *Context, ui func()) {
	c.Begin()
	ui()
	c.End()
}

func TestButtonClick(t *testing.T) {
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll
	clicked := 0
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 50, H: 50}, opt) != 0 {
			if c.Button("ok")&ResSubmit != 0 {
				clicked++
			}
			c.EndWindow()
		}
	}

	// Frame 1: move the mouse over the button. hoverRoot lags one frame, so no
	// hover is established yet.
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	// Frame 2: hoverRoot is now the window, so hover is established.
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	if clicked != 0 {
		t.Fatalf("button submitted before any press")
	}
	// Frame 3: press the left button over the hovered control.
	c.InputMouseDown(10, 10, MouseLeft)
	runFrame(c, ui)
	if clicked != 1 {
		t.Fatalf("expected exactly one submit, got %d", clicked)
	}
}

func TestCheckboxToggle(t *testing.T) {
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll
	checked := false
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 50, H: 50}, opt) != 0 {
			c.Checkbox("on", &checked)
			c.EndWindow()
		}
	}
	c.InputMouseMove(8, 8)
	runFrame(c, ui) // establish hover root
	c.InputMouseMove(8, 8)
	runFrame(c, ui) // establish hover
	c.InputMouseDown(8, 8, MouseLeft)
	runFrame(c, ui) // click toggles
	if !checked {
		t.Fatalf("checkbox did not toggle to true")
	}
}

func TestLayoutNegativeWidthFillsBody(t *testing.T) {
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll | OptNoFrame
	var full, a, b Rect
	var body Rect
	runFrame(c, func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 100, H: 100}, opt) != 0 {
			body = c.GetCurrentContainer().Body
			c.LayoutRow([]int{-1}, 0)
			full = c.LayoutNext()
			c.LayoutRow([]int{60, -1}, 0)
			a = c.LayoutNext()
			b = c.LayoutNext()
			c.EndWindow()
		}
	})

	// padding shrinks the body; layout fills to the body's right edge.
	bodyRight := body.X + body.W - c.Style.Padding
	if full.X+full.W != bodyRight {
		t.Errorf("full-width cell right edge = %d, want %d (full=%+v body=%+v)",
			full.X+full.W, bodyRight, full, body)
	}
	if a.W != 60 {
		t.Errorf("first cell width = %d, want 60", a.W)
	}
	if b.X != a.X+a.W+c.Style.Spacing {
		t.Errorf("second cell X = %d, want %d", b.X, a.X+a.W+c.Style.Spacing)
	}
	if b.X+b.W != bodyRight {
		t.Errorf("second cell right edge = %d, want %d", b.X+b.W, bodyRight)
	}
}

// firstRectX returns the X of the first filled-rect command in z-order.
func firstRectX(c *Context) (int, bool) {
	for cmd := range c.Commands() {
		if cmd.Type == CommandRect {
			return cmd.Rect.X, true
		}
	}
	return 0, false
}

func TestZOrderAndBringToFront(t *testing.T) {
	c := newTestCtx()
	c.InputMouseMove(-10, -10) // keep mouse off both windows
	ui := func() {
		if c.BeginWindow("w1", Rect{X: 0, Y: 0, W: 80, H: 60}) != 0 {
			c.EndWindow()
		}
		if c.BeginWindow("w2", Rect{X: 200, Y: 0, W: 80, H: 60}) != 0 {
			c.EndWindow()
		}
	}

	runFrame(c, ui)
	// w1 created first => lowest z => drawn first (back-most).
	if x, ok := firstRectX(c); !ok || x != 0 {
		t.Fatalf("back-most window X = %d (ok=%v), want 0 (w1)", x, ok)
	}

	// Raise w1 above w2; now w2 is back-most and drawn first.
	c.BringToFront(c.GetContainer("w1"))
	runFrame(c, ui)
	if x, ok := firstRectX(c); !ok || x != 200 {
		t.Fatalf("back-most window X after BringToFront = %d (ok=%v), want 200 (w2)", x, ok)
	}
}

func TestTextWordWrap(t *testing.T) {
	c := newTestCtx()
	// Count the text commands emitted for wrapped text in a narrow column.
	var lines int
	runFrame(c, func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 40, H: 80}, OptNoTitle|OptNoResize|OptNoScroll) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.Text("one two three four five six seven eight")
			c.EndWindow()
		}
	})
	for cmd := range c.Commands() {
		if cmd.Type == CommandText {
			lines++
		}
	}
	if lines < 2 {
		t.Fatalf("expected wrapped text to span multiple lines, got %d", lines)
	}
}

func TestResizeRespectsMinSize(t *testing.T) {
	c := newTestCtx()
	// Compact title bar so the resize handle is a single cell at the corner,
	// and a small minimum size (the cell-unit analogue of microui's 96x64).
	c.Style.TitleHeight = 1
	c.Style.MinWidth = 10
	c.Style.MinHeight = 4

	var w, h int
	ui := func() {
		if c.BeginWindow("w", Rect{X: 0, Y: 0, W: 40, H: 22}) != 0 {
			// resize runs inside BeginWindow, so this reflects the new size
			cnt := c.GetCurrentContainer()
			w, h = cnt.Rect.W, cnt.Rect.H
			c.EndWindow()
		}
	}

	// Grab the resize handle at the bottom-right corner (39,21).
	c.InputMouseMove(39, 21)
	runFrame(c, ui) // establish hover root
	c.InputMouseMove(39, 21)
	runFrame(c, ui) // establish hover on the handle
	c.InputMouseDown(39, 21, MouseLeft)
	runFrame(c, ui) // press => focus => resize with zero delta
	if w != 40 || h != 22 {
		t.Fatalf("grabbing the resize handle jumped size to %dx%d, want 40x22", w, h)
	}

	// Drag far past the top-left to shrink below the minimum: size must clamp
	// to MinWidth/MinHeight, never collapse to zero or snap to a hardcoded min.
	c.InputMouseMove(0, 0)
	runFrame(c, ui)
	if w != c.Style.MinWidth || h != c.Style.MinHeight {
		t.Fatalf("resize did not clamp to min %dx%d, got %dx%d",
			c.Style.MinWidth, c.Style.MinHeight, w, h)
	}
}

// customButton is the README's "build your own control" example; this test
// proves the public accessors are sufficient to implement it.
func customButton(c *Context, label string) bool {
	id := c.GetID([]byte(label))
	r := c.LayoutNext()
	c.UpdateControl(id, r, 0)
	clicked := c.MousePressed()&MouseLeft != 0 && c.Focus() == id
	c.DrawControlFrame(id, r, ColorButton, 0)
	c.DrawControlText(label, r, ColorText, OptAlignCenter)
	return clicked
}

func TestCustomControl(t *testing.T) {
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll
	clicks := 0
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 50, H: 50}, opt) != 0 {
			if customButton(c, "go") {
				clicks++
			}
			c.EndWindow()
		}
	}
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	c.InputMouseDown(10, 10, MouseLeft)
	runFrame(c, ui)
	if clicks != 1 {
		t.Fatalf("custom button registered %d clicks, want 1", clicks)
	}
}

func TestResizeDrag(t *testing.T) {
	c := newTestCtx()
	c.Style.TitleHeight = 1
	c.Style.ResizeHandle = 2 // 2x2 grab zone at the bottom-right corner
	c.Style.MinWidth = 8
	c.Style.MinHeight = 3

	var w, h int
	ui := func() {
		if c.BeginWindow("w", Rect{X: 0, Y: 0, W: 30, H: 20}) != 0 {
			cnt := c.GetCurrentContainer()
			w, h = cnt.Rect.W, cnt.Rect.H
			c.EndWindow()
		}
	}

	// Window spans (0,0)-(29,19); the 2x2 handle covers x in [28,29], y in [18,19].
	c.InputMouseMove(29, 19)
	runFrame(c, ui) // hover root
	c.InputMouseMove(29, 19)
	runFrame(c, ui) // hover handle
	c.InputMouseDown(29, 19, MouseLeft)
	runFrame(c, ui) // grab (zero delta)
	if w != 30 || h != 20 {
		t.Fatalf("grab changed size to %dx%d, want 30x20", w, h)
	}

	// Drag the handle out by (5,3): the window grows by the same delta.
	c.InputMouseMove(34, 22)
	runFrame(c, ui)
	if w != 35 || h != 23 {
		t.Fatalf("drag-grow gave %dx%d, want 35x23", w, h)
	}

	// Drag far back up-left while still held: size clamps at the minimum.
	c.InputMouseMove(0, 0)
	runFrame(c, ui)
	if w != c.Style.MinWidth || h != c.Style.MinHeight {
		t.Fatalf("drag-shrink gave %dx%d, want %dx%d",
			w, h, c.Style.MinWidth, c.Style.MinHeight)
	}
}

func TestSliderClampAndChange(t *testing.T) {
	c := newTestCtx()
	v := Real(5)
	opt := OptNoTitle | OptNoResize | OptNoScroll
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 80, H: 40}, opt) != 0 {
			c.Slider(&v, 0, 10)
			c.EndWindow()
		}
	}
	// Without interaction the value must stay clamped within range and unchanged.
	runFrame(c, ui)
	if v != 5 {
		t.Fatalf("slider value changed without input: %v", v)
	}

	// Drive the slider to the far right by focusing and dragging.
	c.InputMouseMove(40, 15)
	runFrame(c, ui)
	c.InputMouseMove(40, 15)
	runFrame(c, ui)
	c.InputMouseDown(1000, 15, MouseLeft) // press far to the right
	runFrame(c, ui)
	if v != 10 {
		t.Fatalf("slider did not clamp to high bound, got %v", v)
	}
}
