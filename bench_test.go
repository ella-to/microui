package microui

import (
	"strconv"
	"testing"
)

// benchState is the persistent state of the benchmark UI.
type benchState struct {
	checks [8]bool
	vals   [8]Real
	text   string
	log    string
}

// benchUI builds a representative frame: two windows, headers, a tree, a grid
// of buttons, sliders, a scrolling panel of wrapped text and a textbox.
func benchUI(c *Context, s *benchState) {
	if c.BeginWindow("Demo", Rect{X: 1, Y: 1, W: 60, H: 40}) != 0 {
		if c.HeaderEx("Buttons", OptExpanded) != 0 {
			c.LayoutRow([]int{12, 12, 12, -1}, 0)
			for i := range 16 {
				c.PushID([]byte(strconv.Itoa(i)))
				c.Button("Button")
				c.PopID()
			}
		}
		if c.HeaderEx("Tree", OptExpanded) != 0 {
			if c.BeginTreenodeEx("Options", OptExpanded) != 0 {
				for i := range s.checks {
					c.Checkbox("Check", &s.checks[i])
				}
				c.EndTreenode()
			}
		}
		if c.HeaderEx("Sliders", OptExpanded) != 0 {
			c.LayoutRow([]int{8, -1}, 0)
			for i := range s.vals {
				c.Label("Value:")
				c.Slider(&s.vals[i], 0, 255)
			}
		}
		c.EndWindow()
	}
	if c.BeginWindow("Log", Rect{X: 62, Y: 1, W: 40, H: 40}) != 0 {
		c.LayoutRow([]int{-1}, -4)
		c.BeginPanel("out")
		c.LayoutRow([]int{-1}, -1)
		c.Text(s.log)
		c.EndPanel()
		c.LayoutRow([]int{-1}, 0)
		c.Textbox(&s.text)
		c.EndWindow()
	}
}

// newBenchCtx returns a test context with compact, cell-sized metrics like the
// terminal renderer uses.
func newBenchCtx() *Context {
	c := newTestCtx()
	c.Style.Size = Vec2{X: 10, Y: 1}
	c.Style.Padding = 1
	c.Style.Spacing = 1
	c.Style.Indent = 2
	c.Style.TitleHeight = 1
	c.Style.ScrollbarSize = 1
	c.Style.ThumbSize = 1
	return c
}

func newBenchState() *benchState {
	s := &benchState{text: "hello, benchmark world"}
	for i := range 40 {
		s.log += "log line number " + strconv.Itoa(i) + " with some words to wrap\n"
	}
	return s
}

// BenchmarkFrame measures building one complete frame and iterating its
// command list, the work a renderer drives every frame.
func BenchmarkFrame(b *testing.B) {
	c := newBenchCtx()
	s := newBenchState()
	b.ReportAllocs()
	for b.Loop() {
		c.Begin()
		benchUI(c, s)
		c.End()
		for range c.Commands() {
		}
	}
}

// BenchmarkFrameFocusedTextbox is BenchmarkFrame with the textbox focused,
// which exercises the caret/selection path every frame.
func BenchmarkFrameFocusedTextbox(b *testing.B) {
	c := newBenchCtx()
	s := newBenchState()
	frame := func() {
		c.Begin()
		benchUI(c, s)
		c.End()
	}
	frame()
	// focus the textbox: it is the last control of the Log window
	var r Rect
	for cmd := range c.Commands() {
		if cmd.Type == CommandText && cmd.Str == s.text {
			r = Rect{X: cmd.Pos.X, Y: cmd.Pos.Y, W: 1, H: 1}
		}
	}
	c.InputMouseMove(r.X+1, r.Y)
	frame()
	frame()
	c.InputMouseDown(r.X+1, r.Y, MouseLeft)
	frame()
	c.InputMouseUp(r.X+1, r.Y, MouseLeft)
	frame()
	if c.Focus() == 0 {
		b.Fatal("textbox did not take focus")
	}
	b.ReportAllocs()
	for b.Loop() {
		frame()
		for range c.Commands() {
		}
	}
}
