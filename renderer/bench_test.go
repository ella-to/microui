package renderer

import (
	"io"
	"strconv"
	"testing"

	"ella.to/microui"
)

// benchFrame builds a busy frame of 60 buttons whose labels vary with frame.
func benchFrame(c *microui.Context, frame int) {
	c.Begin()
	if c.BeginWindow("Bench", microui.Rect{X: 1, Y: 1, W: 100, H: 40}) != 0 {
		c.LayoutRow([]int{12, 12, 12, 12, -1}, 0)
		for i := range 60 {
			c.PushID([]byte(strconv.Itoa(i)))
			c.Button("btn " + strconv.Itoa((i+frame)%7))
			c.PopID()
		}
		c.EndWindow()
	}
	c.End()
}

// BenchmarkPresentFullRepaint paints and serializes a 200x60 frame whose
// content changes every iteration.
func BenchmarkPresentFullRepaint(b *testing.B) {
	term := NewHeadless(200, 60)
	c := testCtx(term)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		benchFrame(c, i)
		paint(term, c, microui.RGBA(0, 0, 0, 255))
		io.WriteString(io.Discard, renderANSI(term.scr, true))
		i++
	}
}

// BenchmarkPresentIdle is the common case: nothing on screen changed.
func BenchmarkPresentIdle(b *testing.B) {
	term := NewHeadless(200, 60)
	c := testCtx(term)
	b.ReportAllocs()
	for b.Loop() {
		benchFrame(c, 0)
		paint(term, c, microui.RGBA(0, 0, 0, 255))
		io.WriteString(io.Discard, renderANSI(term.scr, true))
	}
}
