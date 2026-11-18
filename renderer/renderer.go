// Package renderer decouples a microui UI from the backend that draws it.
//
// microui itself never draws anything: each frame it produces a list of draw
// commands (filled rectangles, text, icons, clip regions). This package turns
// that command list into calls on a small Renderer interface, so the same UI
// code runs unchanged on any backend — a terminal, a GPU canvas, a web canvas,
// and so on. Swapping the engine is swapping the Renderer.
//
// The flow is:
//
//	term, _ := renderer.NewTerminal()      // pick a backend (the engine)
//	renderer.Run(term, func(ctx *microui.Context) {
//	    // build the UI; it only ever sees *microui.Context
//	    if ctx.BeginWindow("Hi", microui.Rect{X: 1, Y: 1, W: 30, H: 8}) != 0 {
//	        ctx.Button("OK")
//	        ctx.EndWindow()
//	    }
//	})
//
// Run owns the frame loop: it pumps input through the Driver, calls your UI
// builder between Begin and End, then hands the finished command list to Paint,
// which dispatches every command to the Renderer. Because the UI builder takes
// only a *microui.Context, the exact same function works with any Driver.
package renderer

import (
	"time"

	"ella.to/microui"
)

// Renderer is a drawing backend for microui. Paint walks a frame's command list
// and calls these methods in order; a backend only has to know how to measure
// text and paint the four primitives onto its surface.
//
// Clipping is modelled as a scissor rectangle: SetClip sets the region that
// subsequent FillRect / DrawText / DrawIcon calls must stay within, and Clear
// resets it to the whole surface. (microui already pre-clips rectangles, but it
// relies on the scissor for text and icons.)
type Renderer interface {
	// TextWidth and TextHeight measure rendered text in the surface's own units
	// (pixels, character cells, ...). They are wired into the context as its
	// layout callbacks by Connect.
	TextWidth(font microui.Font, str string) int
	TextHeight(font microui.Font) int

	// Size reports the drawable area in surface units.
	Size() (w, h int)

	// Clear begins a frame: it fills the whole surface with bg and resets the
	// clip region to the full surface.
	Clear(bg microui.Color)

	// SetClip restricts following draws to rect (intersected with the surface).
	SetClip(rect microui.Rect)

	// FillRect fills rect with a solid color.
	FillRect(rect microui.Rect, color microui.Color)

	// DrawText draws str with its top-left corner at pos.
	DrawText(font microui.Font, str string, pos microui.Vec2, color microui.Color)

	// DrawIcon draws a built-in icon (microui.IconClose, IconCheck, ...) inside
	// rect.
	DrawIcon(id microui.Icon, rect microui.Rect, color microui.Color)

	// Present flushes the finished frame to the display.
	Present()
}

// Driver is a Renderer that can also run a live UI: it supplies the style and
// background to use, pumps platform input into the context, and tears itself
// down. Run drives it. Concrete drivers (e.g. Terminal) live in this package;
// adding a new engine means implementing this interface.
type Driver interface {
	Renderer

	// Style returns the style this backend wants the context to use (metrics
	// tuned for its units). Run installs it before the first frame.
	Style() microui.Style

	// Background returns the color the surface is cleared to each frame. It is
	// read every frame, so a UI may change it live.
	Background() microui.Color

	// Poll drains pending platform input into ctx and reports whether the user
	// asked to quit (closed the surface, pressed Ctrl-C, reached EOF). It must
	// not block, so Run can keep a steady frame rate.
	Poll(ctx *microui.Context) (quit bool)

	// Close restores any platform state. It is safe to call more than once.
	Close() error
}

// Connect wires a renderer's text metrics into ctx as its layout callbacks.
// Run does this for you; call it directly only when driving the loop yourself.
func Connect(ctx *microui.Context, r Renderer) {
	ctx.TextWidth = r.TextWidth
	ctx.TextHeight = r.TextHeight
}

// paint clears the surface to bg and dispatches one finished frame's draw
// commands to r. It does not Present, so callers (and tests) can inspect the
// surface first.
func paint(r Renderer, ctx *microui.Context, bg microui.Color) {
	r.Clear(bg)
	for cmd := range ctx.Commands() {
		switch cmd.Type {
		case microui.CommandClip:
			r.SetClip(cmd.Rect)
		case microui.CommandRect:
			if cmd.Color.A == 0 {
				continue // fully transparent (e.g. an empty panel background)
			}
			r.FillRect(cmd.Rect, cmd.Color)
		case microui.CommandText:
			r.DrawText(cmd.Font, cmd.Str, cmd.Pos, cmd.Color)
		case microui.CommandIcon:
			r.DrawIcon(cmd.Icon, cmd.Rect, cmd.Color)
		}
	}
}

// Paint renders one finished microui frame through r and presents it. Call it
// after ctx.End(). This is the bridge between microui's command list and a
// Renderer: every draw command becomes one method call on r.
func Paint(r Renderer, ctx *microui.Context, bg microui.Color) {
	paint(r, ctx, bg)
	r.Present()
}

// frameRate is the loop's target redraw rate. A fixed cadence keeps animations
// smooth without the UI builder needing a clock.
const frameRate = 30

// Run drives a Driver's frame loop until the user quits, then closes it.
//
// Each frame it polls input into a fresh context, calls ui to build the frame,
// then paints and presents it. The ui builder receives only a *microui.Context,
// so it is completely independent of which Driver is running — that is what
// makes the engine swappable.
func Run(d Driver, ui func(ctx *microui.Context)) error {
	ctx := microui.New()
	Connect(ctx, d)
	st := d.Style()
	ctx.Style = &st
	defer d.Close()

	ticker := time.NewTicker(time.Second / frameRate)
	defer ticker.Stop()

	for {
		if d.Poll(ctx) {
			return nil
		}
		ctx.Begin()
		ui(ctx)
		ctx.End()
		Paint(d, ctx, d.Background())
		<-ticker.C
	}
}
