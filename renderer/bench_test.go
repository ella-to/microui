package renderer

import (
	"strconv"
	"testing"

	"ella.to/microui"
)

// benchFrame builds a busy frame of 60 buttons whose labels vary with frame.
func benchFrame(c *microui.Context, frame int) {
	c.Begin()
	if c.BeginWindow("Bench", microui.Rect{X: 1, Y: 1, W: 100, H: 40}).Active() {
		c.LayoutRow([]int{12, 12, 12, 12, -1}, 0)
		for i := range 60 {
			c.PushID(i)
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
	term, out := newTestTerminal(200, 60, ColorTrue)
	c := testCtx(term)
	b.ReportAllocs()
	i, written := 0, 0
	for b.Loop() {
		out.Reset()
		benchFrame(c, i)
		Paint(term, c, microui.RGBA(0, 0, 0, 255))
		written += out.Len()
		i++
	}
	b.ReportMetric(float64(written)/float64(i), "out-B/frame")
}

// BenchmarkPresentIdle is the common case: nothing on screen changed.
func BenchmarkPresentIdle(b *testing.B) {
	term, out := newTestTerminal(200, 60, ColorTrue)
	c := testCtx(term)
	b.ReportAllocs()
	n, written := 0, 0
	for b.Loop() {
		out.Reset()
		benchFrame(c, 0)
		Paint(term, c, microui.RGBA(0, 0, 0, 255))
		written += out.Len()
		n++
	}
	b.ReportMetric(float64(written)/float64(n), "out-B/frame")
}
