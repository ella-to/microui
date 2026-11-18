// Command animation shows per-frame animation driven by hover state: three
// boxes that smoothly grow when the mouse is over them and shrink back when it
// leaves.
//
//	go run ella.to/microui/examples/animation
//
// renderer.Run redraws at a fixed frame rate, so the UI builder can ease each
// box's size toward a target every frame without needing a clock. Move the mouse
// over the boxes; press Ctrl-C to quit.
package main

import (
	"fmt"
	"os"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

const (
	minW, minH = 12, 3 // resting size
	maxW, maxH = 18, 5 // hovered size
	ease       = 0.30  // fraction of the remaining distance covered per frame
)

// box is one animated box. cx,cy is its fixed center; w,h is its current
// (animated) size. State persists across frames.
type box struct {
	cx, cy int
	w, h   float64
	label  string
}

var boxes = []*box{
	{cx: 16, cy: 9, w: minW, h: minH, label: "one"},
	{cx: 40, cy: 9, w: minW, h: minH, label: "two"},
	{cx: 64, cy: 9, w: minW, h: minH, label: "three"},
}

// approach moves cur a fraction of the way toward target, snapping when close
// enough so it settles exactly.
func approach(cur, target float64) float64 {
	cur += (target - cur) * ease
	if d := target - cur; d < 0.05 && d > -0.05 {
		return target
	}
	return cur
}

func ui(ctx *microui.Context) {
	const canvas = microui.OptNoTitle | microui.OptNoResize | microui.OptNoScroll | microui.OptNoFrame
	if !ctx.BeginWindowEx("canvas", microui.Rect{X: 0, Y: 0, W: 1000, H: 1000}, canvas).Active() {
		return
	}
	defer ctx.EndWindow()

	for _, b := range boxes {
		// Hit-test against the maximum size so the target doesn't flip-flop as
		// the box grows and shrinks under the cursor.
		hit := microui.Rect{X: b.cx - maxW/2, Y: b.cy - maxH/2, W: maxW, H: maxH}
		hovered := ctx.MouseOver(hit)

		tw, th := float64(minW), float64(minH)
		if hovered {
			tw, th = float64(maxW), float64(maxH)
		}
		b.w = approach(b.w, tw)
		b.h = approach(b.h, th)

		w, h := int(b.w+0.5), int(b.h+0.5)
		r := microui.Rect{X: b.cx - w/2, Y: b.cy - h/2, W: w, H: h}

		color := microui.RGBA(60, 120, 110, 255)
		if hovered {
			color = microui.RGBA(90, 200, 180, 255)
		}
		ctx.DrawRect(r, color)
		ctx.DrawControlText(b.label, r, microui.ColorText, microui.OptAlignCenter)
	}
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "animation:", err)
		os.Exit(1)
	}
	if err := renderer.Run(term, ui); err != nil {
		fmt.Fprintln(os.Stderr, "animation:", err)
		os.Exit(1)
	}
}
