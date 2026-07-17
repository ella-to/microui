package microui

import (
	"testing"
	"time"
)

// newTextboxFixture returns a context plus a ui closure drawing one textbox
// bound to buf, and focuses the textbox by hovering and clicking it (three
// frames: hover root, hover, press).
func newTextboxFixture(buf *string) (*Context, func()) {
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 50, H: 50}, opt) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.Textbox(buf)
			c.EndWindow()
		}
	}
	c.InputMouseMove(10, 10)
	runFrame(c, ui) // establish hover root
	c.InputMouseMove(10, 10)
	runFrame(c, ui) // establish hover
	c.InputMouseDown(10, 10, MouseLeft)
	runFrame(c, ui) // click focuses the textbox
	c.InputMouseUp(10, 10, MouseLeft)
	runFrame(c, ui)
	return c, ui
}

// pressKey feeds one key press (down for a frame, then up).
func pressKey(c *Context, ui func(), keys ...int) {
	for _, k := range keys {
		c.InputKeyDown(k)
	}
	runFrame(c, ui)
	for _, k := range keys {
		c.InputKeyUp(k)
	}
}

func typeText(c *Context, ui func(), s string) {
	c.InputText(s)
	runFrame(c, ui)
}

func TestTextboxCaretInsertAndDelete(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)

	typeText(c, ui, "helo")
	if buf != "helo" {
		t.Fatalf("typed value = %q, want %q", buf, "helo")
	}

	// caret starts at the end; move left once and insert the missing 'l'
	pressKey(c, ui, KeyLeft)
	typeText(c, ui, "l")
	if buf != "hello" {
		t.Fatalf("after mid-string insert = %q, want %q", buf, "hello")
	}

	// Home + Delete removes the first rune; End + Backspace the last
	pressKey(c, ui, KeyHome)
	pressKey(c, ui, KeyDelete)
	if buf != "ello" {
		t.Fatalf("after Home+Delete = %q, want %q", buf, "ello")
	}
	pressKey(c, ui, KeyEnd)
	pressKey(c, ui, KeyBackspace)
	if buf != "ell" {
		t.Fatalf("after End+Backspace = %q, want %q", buf, "ell")
	}
}

func TestTextboxShiftArrowSelectionReplace(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)
	typeText(c, ui, "hello")

	// select the last two runes and type over them
	pressKey(c, ui, KeyShift, KeyLeft)
	pressKey(c, ui, KeyShift, KeyLeft)
	typeText(c, ui, "p!")
	if buf != "help!" {
		t.Fatalf("after selection replace = %q, want %q", buf, "help!")
	}
}

func TestTextboxSelectAll(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)
	typeText(c, ui, "old value")

	pressKey(c, ui, KeySelectAll)
	typeText(c, ui, "new")
	if buf != "new" {
		t.Fatalf("typing over select-all = %q, want %q", buf, "new")
	}

	pressKey(c, ui, KeySelectAll)
	pressKey(c, ui, KeyBackspace)
	if buf != "" {
		t.Fatalf("backspace over select-all = %q, want empty", buf)
	}
}

func TestTextboxCtrlArrowsJumpToEnds(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)
	typeText(c, ui, "abc")

	pressKey(c, ui, KeyCtrl, KeyLeft) // jump to start
	typeText(c, ui, ">")
	if buf != ">abc" {
		t.Fatalf("after Ctrl+Left insert = %q, want %q", buf, ">abc")
	}
	pressKey(c, ui, KeyCtrl, KeyRight) // jump to end
	typeText(c, ui, "<")
	if buf != ">abc<" {
		t.Fatalf("after Ctrl+Right insert = %q, want %q", buf, ">abc<")
	}
}

func TestTextboxDoubleClickSelectsWord(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)
	typeText(c, ui, "hello world")

	// stub the clock so the two clicks land within the double-click window
	base := time.Unix(1000, 0)
	defer func() { now = time.Now }()
	now = func() time.Time { return base }

	// with the test's rune-count text model each rune is 1 unit wide, so
	// clicking a few units into the textbox lands inside a word
	click := func(px int) {
		c.InputMouseMove(px, 10)
		runFrame(c, ui)
		c.InputMouseDown(px, 10, MouseLeft)
		runFrame(c, ui)
		c.InputMouseUp(px, 10, MouseLeft)
		runFrame(c, ui)
	}
	click(10)
	now = func() time.Time { return base.Add(100 * time.Millisecond) }
	click(10)

	// the word under the caret is selected: typing replaces exactly it
	typeText(c, ui, "bye")
	if buf != "bye world" && buf != "hello bye" {
		t.Fatalf("double-click word replace = %q, want a single word replaced", buf)
	}
}

func TestTextboxPasswordMasksCommands(t *testing.T) {
	buf := ""
	c := newTestCtx()
	opt := OptNoTitle | OptNoResize | OptNoScroll
	ui := func() {
		if c.BeginWindowEx("w", Rect{X: 0, Y: 0, W: 50, H: 50}, opt) != 0 {
			c.LayoutRow([]int{-1}, 0)
			c.TextboxPassword(&buf)
			c.EndWindow()
		}
	}
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	c.InputMouseMove(10, 10)
	runFrame(c, ui)
	c.InputMouseDown(10, 10, MouseLeft)
	runFrame(c, ui)
	c.InputMouseUp(10, 10, MouseLeft)
	c.InputText("secret")
	runFrame(c, ui)

	if buf != "secret" {
		t.Fatalf("password value = %q, want %q", buf, "secret")
	}
	// the emitted draw commands must never contain the real value
	for cmd := range c.Commands() {
		if cmd.Type != CommandText {
			continue
		}
		if cmd.Str == "secret" {
			t.Fatalf("plaintext password leaked into draw commands")
		}
		if cmd.Str == "******" {
			return // masked text found
		}
	}
	t.Fatalf("masked text not found in draw commands")
}

func TestTextboxFocusLossResetsEditState(t *testing.T) {
	buf := ""
	c, ui := newTextboxFixture(&buf)
	typeText(c, ui, "abc")
	pressKey(c, ui, KeyHome)

	// clicking outside the textbox drops focus and discards the edit state
	c.InputMouseMove(45, 45)
	runFrame(c, ui)
	c.InputMouseDown(45, 45, MouseLeft)
	runFrame(c, ui)
	c.InputMouseUp(45, 45, MouseLeft)
	runFrame(c, ui)
	if c.textEdit.id != 0 {
		t.Fatalf("edit state not released on focus loss (id=%d)", c.textEdit.id)
	}
}
