// Package renderer decouples a microui UI from the backend that draws it.
//
// microui itself never draws anything: each frame it produces a list of draw
// commands (filled rectangles, text, icons, clip regions). This package turns
// that command list into calls on a small Renderer interface, so the same UI
// code runs unchanged on any backend — a terminal, a GPU canvas, a web canvas,
// and so on. Swapping the engine is swapping the Driver.
//
// The flow is:
//
//	term, err := renderer.NewTerminal() // pick a backend (the engine)
//	if err != nil { ... }
//	err = renderer.Run(term, func(ctx *microui.Context) {
//	    // build the UI; it only ever sees *microui.Context
//	    if ctx.BeginWindow("Hi", microui.Rect{X: 1, Y: 1, W: 30, H: 8}).Active() {
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
	"context"
	"time"

	"ella.to/microui"
)

// Renderer is a drawing backend for microui. Paint walks a frame's command
// list and calls these methods in order; a backend only has to know how to
// measure text and paint the four primitives onto its surface.
//
// Clipping is modelled as a scissor rectangle: SetClip sets the region that
// subsequent FillRect / DrawText / DrawIcon calls must stay within, and Clear
// resets it to the whole surface. (microui already pre-clips rectangles, but
// it relies on the scissor for text and icons.)
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

	// FillRect fills rect with color. Colors with 0 < A < 255 should be
	// blended over what is already there when the backend can.
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
// down. Run drives it. Adding a new engine means implementing this interface.
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

// Waker is an optional Driver extension. Run selects on the returned channel
// alongside its frame ticker, so a driver that signals it whenever input
// arrives gets an immediate redraw instead of waiting for the next tick.
type Waker interface {
	Wake() <-chan struct{}
}

// Errer is an optional Driver extension reporting why Poll asked to quit.
// When Poll returns true, Run returns Err() (nil means a normal quit).
type Errer interface {
	Err() error
}

// Connect wires a renderer's text metrics into ctx as its layout callbacks.
// Run does this for you; call it directly only when driving the loop yourself.
func Connect(ctx *microui.Context, r Renderer) {
	ctx.TextWidth = r.TextWidth
	ctx.TextHeight = r.TextHeight
}

// Draw clears the surface to bg and dispatches one finished frame's draw
// commands to r, without presenting it. Use it to inspect the surface first,
// e.g. in tests: Draw into a NewHeadless terminal, then assert on its Text.
func Draw(r Renderer, ctx *microui.Context, bg microui.Color) {
	if t, ok := r.(*Terminal); ok {
		back := t.back
		back.clear(bg.WithAlpha(255))
		bounds := back.bounds()
		clip := bounds
		for cmd := range ctx.Commands() {
			switch cmd.Type {
			case microui.CommandClip:
				clip = cmd.Rect.Intersect(bounds)
			case microui.CommandRect:
				if cmd.Color.A == 0 {
					continue
				}
				back.fillRectClipped(cmd.Rect, clip, cmd.Color)
			case microui.CommandText:
				back.drawTextClipped(cmd.Str, cmd.Pos, clip, cmd.Color)
			case microui.CommandIcon:
				back.drawIconClipped(cmd.Icon, cmd.Rect, clip, cmd.Color)
			}
		}
		t.clip = clip
		return
	}
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
	Draw(r, ctx, bg)
	r.Present()
}

// DefaultFPS is the frame rate Run uses unless WithFPS overrides it.
const DefaultFPS = 30

// runConfig collects the RunOptions.
type runConfig struct {
	fps   int
	ctx   context.Context
	setup func(*microui.Context)
}

// RunOption configures Run.
type RunOption func(*runConfig)

// WithFPS sets the steady redraw rate (frames per second) Run uses to keep
// animations moving. Drivers implementing Waker also redraw as soon as input
// arrives, so a low rate does not make the UI feel sluggish.
func WithFPS(fps int) RunOption {
	return func(c *runConfig) { c.fps = max(fps, 1) }
}

// WithContext makes Run return when ctx is done. This is how a UI quits
// itself — cancel the context from a "Quit" button:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	renderer.Run(term, func(ui *microui.Context) {
//	    if ui.Button("Quit").Submitted() { cancel() }
//	}, renderer.WithContext(ctx))
func WithContext(ctx context.Context) RunOption {
	return func(c *runConfig) { c.ctx = ctx }
}

// WithSetup runs fn once on the new microui.Context, after the driver's style
// is installed and before the first frame: the place to tweak the style,
// replace DrawFrame, and so on.
func WithSetup(fn func(*microui.Context)) RunOption {
	return func(c *runConfig) { c.setup = fn }
}

// Run drives a Driver's frame loop until the user quits (or the WithContext
// context is done), then closes it. The driver is closed even if ui panics,
// so a terminal is never left in raw mode.
//
// Each frame it polls input, calls ui to build the frame between Begin and
// End, then paints and presents it. The ui builder receives only a
// *microui.Context, so it is completely independent of which Driver is
// running — that is what makes the engine swappable.
func Run(d Driver, ui func(ctx *microui.Context), opts ...RunOption) error {
	cfg := runConfig{fps: DefaultFPS, ctx: context.Background()}
	for _, opt := range opts {
		opt(&cfg)
	}
	defer d.Close()

	mu := microui.New()
	Connect(mu, d)
	st := d.Style()
	mu.Style = &st
	if cfg.setup != nil {
		cfg.setup(mu)
	}

	var wake <-chan struct{}
	if w, ok := d.(Waker); ok {
		wake = w.Wake()
	}
	ticker := time.NewTicker(time.Second / time.Duration(cfg.fps))
	defer ticker.Stop()

	for cfg.ctx.Err() == nil {
		if d.Poll(mu) {
			if e, ok := d.(Errer); ok {
				return e.Err()
			}
			return nil
		}
		mu.Begin()
		ui(mu)
		mu.End()
		Paint(d, mu, d.Background())

		select {
		case <-ticker.C:
		case <-wake:
		case <-cfg.ctx.Done():
		}
	}
	return nil
}
