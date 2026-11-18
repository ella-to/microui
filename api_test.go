package microui

import (
	"slices"
	"testing"
)

func TestIDKeysHashConsistently(t *testing.T) {
	c := newTestCtx()
	if c.GetID("box") != c.GetID([]byte("box")) {
		t.Fatal("string and []byte keys with equal content must hash equally")
	}
	type rowID int
	if c.GetID(rowID(7)) != c.GetID(7) {
		t.Fatal("a named integer type must hash like its underlying value")
	}
	type name string
	if c.GetID(name("x")) != c.GetID("x") {
		t.Fatal("a named string type must hash like its underlying value")
	}
	if c.GetID(7) == c.GetID("7") {
		t.Fatal("int 7 and string \"7\" should produce different ids")
	}
	if c.GetID(1) == c.GetID(2) {
		t.Fatal("distinct ints must produce distinct ids")
	}
}

func TestPushIDIntScopesLoopControls(t *testing.T) {
	c := newTestCtx()
	var ids []ID
	runFrame(c, func() {
		if c.BeginWindowEx("w", Rect{W: 50, H: 50}, OptNoTitle) != 0 {
			for i := range 3 {
				c.PushID(i)
				c.Button("Delete")
				ids = append(ids, c.LastID())
				c.PopID()
			}
			c.EndWindow()
		}
	})
	slices.Sort(ids)
	if len(slices.Compact(ids)) != 3 {
		t.Fatalf("PushID(i) did not give each row's button a unique id: %v", ids)
	}
}

// dragSlider focuses the slider at y=15 and presses at screen x.
func dragSlider(c *Context, ui func(), x int) {
	c.InputMouseMove(40, 15)
	runFrame(c, ui)
	c.InputMouseMove(40, 15)
	runFrame(c, ui)
	c.InputMouseDown(x, 15, MouseLeft)
	runFrame(c, ui)
	c.InputMouseUp(x, 15, MouseLeft)
	runFrame(c, ui)
}

func sliderUI[T Number](c *Context, v *T, lo, hi T) func() {
	return func() {
		if c.BeginWindowEx("w", Rect{W: 80, H: 40}, OptNoTitle|OptNoResize|OptNoScroll) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.Slider(v, lo, hi)
			c.EndWindow()
		}
	}
}

func TestSliderGenericTypes(t *testing.T) {
	t.Run("int snaps to whole numbers", func(t *testing.T) {
		c := newTestCtx()
		v := 0
		ui := sliderUI(c, &v, 0, 10)
		dragSlider(c, ui, 40) // roughly the middle
		if v < 4 || v > 6 {
			t.Fatalf("int slider at the middle = %d, want ~5", v)
		}
		dragSlider(c, ui, 1000)
		if v != 10 {
			t.Fatalf("int slider clamped high = %d, want 10", v)
		}
	})
	t.Run("float64", func(t *testing.T) {
		c := newTestCtx()
		v := 0.25
		ui := sliderUI(c, &v, 0, 1)
		runFrame(c, ui)
		if v != 0.25 {
			t.Fatalf("float64 slider changed without input: %v", v)
		}
		dragSlider(c, ui, -1000)
		if v != 0 {
			t.Fatalf("float64 slider clamped low = %v, want 0", v)
		}
	})
	t.Run("uint8 never wraps", func(t *testing.T) {
		c := newTestCtx()
		var v uint8 = 200
		ui := sliderUI(c, &v, 0, 255)
		dragSlider(c, ui, -1000)
		if v != 0 {
			t.Fatalf("uint8 slider clamped low = %d, want 0", v)
		}
	})
}

func TestSliderDefaultFormatByType(t *testing.T) {
	c := newTestCtx()
	n, f := 42, 1.5
	runFrame(c, func() {
		if c.BeginWindowEx("w", Rect{W: 200, H: 80}, OptNoTitle|OptNoScroll) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.Slider(&n, 0, 100)
			c.Number(&f, 0.1)
			c.EndWindow()
		}
	})
	var texts []string
	for cmd := range c.Commands() {
		if cmd.Type == CommandText {
			texts = append(texts, cmd.Str)
		}
	}
	if !slices.Contains(texts, "42") || !slices.Contains(texts, "1.50") {
		t.Fatalf("default formats: got %q, want \"42\" and \"1.50\"", texts)
	}
}

func TestResultHelpers(t *testing.T) {
	r := ResSubmit | ResChange
	if !r.Submitted() || !r.Changed() || r.Active() {
		t.Fatalf("Result helpers disagree with bits for %b", r)
	}
}

func TestRectHelpers(t *testing.T) {
	r := Rect{X: 2, Y: 3, W: 4, H: 5}
	if !r.Contains(Vec2{X: 2, Y: 3}) || r.Contains(Vec2{X: 6, Y: 3}) {
		t.Fatal("Contains must include the top-left and exclude the right edge")
	}
	if got := r.Expand(1); got != (Rect{X: 1, Y: 2, W: 6, H: 7}) {
		t.Fatalf("Expand(1) = %+v", got)
	}
	if got := r.Intersect(Rect{X: 100, Y: 100, W: 1, H: 1}); !got.Empty() {
		t.Fatalf("disjoint Intersect = %+v, want empty", got)
	}
	if got := Hex(0x102030); got != (Color{0x10, 0x20, 0x30, 255}) {
		t.Fatalf("Hex = %+v", got)
	}
	if got := Hex(0x000000).Lerp(Hex(0xffffff), 0.5); got.R != 128 {
		t.Fatalf("Lerp midpoint R = %d, want 128", got.R)
	}
}

func TestTextWrapsAtWordBoundaries(t *testing.T) {
	c := newTestCtx()
	c.Style.Padding = 0
	var lines []string
	runFrame(c, func() {
		if c.BeginWindowEx("w", Rect{W: 11, H: 50}, OptNoTitle|OptNoScroll|OptNoFrame) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.Text("aaa bbb ccc\ndddddddddddddd e")
			c.EndWindow()
		}
	})
	for cmd := range c.Commands() {
		if cmd.Type == CommandText {
			lines = append(lines, cmd.Str)
		}
	}
	want := []string{"aaa bbb ccc", "dddddddddddddd", "e"}
	if !slices.Equal(lines, want) {
		t.Fatalf("wrapped lines = %q, want %q", lines, want)
	}
}

func TestTextboxDoesNotRewriteUnchangedBuffer(t *testing.T) {
	buf := "abc"
	c, ui := newTextboxFixture(&buf)
	before := buf
	runFrame(c, ui)
	if c.Focus() == 0 {
		t.Fatal("textbox lost focus")
	}
	if buf != before {
		t.Fatalf("idle focused textbox modified buffer: %q", buf)
	}
	pressKey(c, ui, KeyEnd) // the fixture's click left the caret at index 0
	typeText(c, ui, "é")
	pressKey(c, ui, KeyLeft)
	typeText(c, ui, "→")
	if buf != "abc→é" {
		t.Fatalf("multibyte editing = %q, want %q", buf, "abc→é")
	}
}

func TestClosePopupFromInsideThePopup(t *testing.T) {
	c := newTestCtx()
	closeNow := false
	open := false
	ui := func() {
		if c.BeginWindow("w", Rect{W: 200, H: 200}).Active() {
			if c.BeginPopup("menu").Active() {
				open = true
				if closeNow {
					c.ClosePopup("menu")
				}
				c.EndPopup()
			}
			c.EndWindow()
		}
	}
	// OpenPopup must be called in the same scope as BeginPopup
	runFrame(c, func() {
		if c.BeginWindow("w", Rect{W: 200, H: 200}).Active() {
			c.OpenPopup("menu")
			c.EndWindow()
		}
	})
	open = false
	runFrame(c, ui)
	if !open {
		t.Fatal("popup did not open")
	}
	closeNow = true
	runFrame(c, ui) // closes from inside
	open = false
	runFrame(c, ui)
	if open {
		t.Fatal("ClosePopup called inside the popup did not close it")
	}
}
