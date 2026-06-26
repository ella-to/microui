// Command boxdrag is the smallest possible miniui interaction demo: a single
// box you can drag around the terminal with the mouse.
//
//	go run ella.to/microui/examples/boxdrag
//
// It shows how to build a custom, draggable control from miniui's primitives
// (GetID, UpdateControl, the input accessors and DrawRect) inside a chrome-less
// full-screen "canvas" window — and how the whole thing is driven by a swappable
// renderer.Driver. Press Ctrl-C to quit.
package main

import (
	"fmt"
	"os"

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

// box position, persistent across frames.
var box = miniui.Vec2{X: 8, Y: 4}

const boxW, boxH = 18, 5

func ui(ctx *miniui.Context) {
	// A canvas: a full-screen window with no title/border/scroll, just somewhere
	// to draw and receive mouse input. It is clipped to the real screen by the
	// renderer, so making it oversized is fine.
	const canvas = miniui.OptNoTitle | miniui.OptNoResize | miniui.OptNoScroll | miniui.OptNoFrame
	if ctx.BeginWindowEx("canvas", miniui.Rect{X: 0, Y: 0, W: 1000, H: 1000}, canvas) == 0 {
		return
	}
	defer ctx.EndWindow()

	// One draggable control, identified by a stable id.
	id := ctx.GetID([]byte("box"))
	r := miniui.Rect{X: box.X, Y: box.Y, W: boxW, H: boxH}
	ctx.UpdateControl(id, r, 0)

	// While focused (mouse pressed on it) and the button is held, follow the
	// mouse. Focus is retained even when the cursor briefly leaves the box, so a
	// fast drag does not drop it.
	if ctx.Focus() == id && ctx.MouseDown()&miniui.MouseLeft != 0 {
		d := ctx.MouseDelta()
		box.X = max(0, box.X+d.X)
		box.Y = max(0, box.Y+d.Y)
	}

	color := miniui.RGBA(70, 130, 180, 255)
	switch {
	case ctx.Focus() == id:
		color = miniui.RGBA(120, 180, 230, 255)
	case ctx.Hover() == id:
		color = miniui.RGBA(95, 155, 205, 255)
	}
	ctx.DrawRect(r, color)
	ctx.DrawControlText("drag me", r, miniui.ColorText, miniui.OptAlignCenter)
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "boxdrag:", err)
		os.Exit(1)
	}
	if err := renderer.Run(term, ui); err != nil {
		fmt.Fprintln(os.Stderr, "boxdrag:", err)
		os.Exit(1)
	}
}
