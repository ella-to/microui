//go:build js && wasm

// Command wasm runs a microui UI in the browser, drawing onto an HTML
// <canvas> through a renderer.Driver built on syscall/js alone.
//
//	GOOS=js GOARCH=wasm go build -o examples/wasm/main.wasm ./examples/wasm
//
// See README.md in this directory for the full build-and-serve steps.
//
// The UI function below is an ordinary func(*microui.Context); the same code
// would run unchanged on the terminal driver. Only the driver (canvas.go) and
// the frame loop are browser-specific.
package main

import (
	"fmt"
	"strconv"
	"syscall/js"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// demo is the application state, persistent across frames.
type demo struct {
	clicks  int
	enabled bool
	volume  float64 // bound to a float64 slider
	level   int     // bound to an int slider: snaps to whole numbers
	gain    float32 // dragged with Number
	name    string
	log     []string
	bg      [3]uint8 // background color sliders, bound directly as uint8
}

func (d *demo) logf(format string, args ...any) {
	d.log = append(d.log, fmt.Sprintf(format, args...))
	if len(d.log) > 50 {
		d.log = d.log[len(d.log)-50:]
	}
}

func (d *demo) ui(ctx *microui.Context, canvas *Canvas) {
	if ctx.BeginWindow("microui in the browser", microui.Rect{X: 20, Y: 20, W: 340, H: 420}).Active() {
		if ctx.HeaderEx("Controls", microui.OptExpanded).Active() {
			ctx.LayoutRow([]int{100, -1}, 0)
			if ctx.Button("Click me").Submitted() {
				d.clicks++
				d.logf("clicked %d time(s)", d.clicks)
			}
			ctx.Label("clicks: " + strconv.Itoa(d.clicks))

			ctx.LayoutRow([]int{-1}, 0)
			if ctx.Checkbox("Enabled", &d.enabled).Changed() {
				d.logf("enabled = %v", d.enabled)
			}

			ctx.LayoutRow([]int{80, -1}, 0)
			ctx.Label("Volume")
			ctx.Slider(&d.volume, 0, 1)
			ctx.Label("Level")
			ctx.Slider(&d.level, 1, 10)
			ctx.Label("Gain")
			ctx.Number(&d.gain, 0.05)
			ctx.Label("Name")
			if ctx.Textbox(&d.name).Submitted() {
				d.logf("hello, %s!", d.name)
			}
		}

		if ctx.HeaderEx("Background", microui.OptExpanded).Active() {
			ctx.LayoutRow([]int{80, -1}, 0)
			for i, ch := range []string{"Red", "Green", "Blue"} {
				ctx.Label(ch)
				ctx.Slider(&d.bg[i], 0, 255)
			}
			canvas.BG = microui.RGBA(int(d.bg[0]), int(d.bg[1]), int(d.bg[2]), 255)
		}

		if ctx.Header("Tree").Active() {
			if ctx.BeginTreenode("Fruits").Active() {
				for _, f := range []string{"Apple", "Banana", "Cherry"} {
					ctx.Label(f)
				}
				ctx.EndTreenode()
			}
			if ctx.BeginTreenode("Veggies").Active() {
				ctx.Label("Carrot")
				ctx.EndTreenode()
			}
		}
		ctx.EndWindow()
	}

	if ctx.BeginWindow("Log", microui.Rect{X: 380, Y: 20, W: 300, H: 260}).Active() {
		ctx.LayoutRow([]int{-1}, -1)
		ctx.BeginPanel("log")
		ctx.LayoutRow([]int{-1}, 0)
		for _, line := range d.log {
			ctx.Label(line) // labels have no id, so no PushID is needed
		}
		ctx.EndPanel()
		ctx.EndWindow()
	}
}

// main drives frames from requestAnimationFrame rather than renderer.Run.
//
// renderer.Run would also work: when every goroutine is blocked (Run waits on
// its ticker), Go's js/wasm runtime returns control to the browser event loop,
// so DOM callbacks still fire. requestAnimationFrame is the better fit for a
// browser, though: frames are synced to the display refresh, and the browser
// pauses them in background tabs instead of a ticker spinning for nothing.
func main() {
	canvas, err := NewCanvas("screen")
	if err != nil {
		js.Global().Get("console").Call("error", "microui:", err.Error())
		return
	}
	// The page lives as long as the program, so canvas.Close (which detaches
	// the DOM listeners) is never needed here; call it when embedding the
	// canvas in a larger app that tears the UI down.

	ctx := microui.New()
	renderer.Connect(ctx, canvas)
	st := canvas.Style()
	ctx.Style = &st

	d := &demo{volume: 0.5, level: 3, gain: 1, bg: [3]uint8{40, 44, 52}}
	d.logf("ready")

	var frame js.Func
	frame = js.FuncOf(func(js.Value, []js.Value) any {
		canvas.Poll(ctx)
		ctx.Begin()
		d.ui(ctx, canvas)
		ctx.End()
		renderer.Paint(canvas, ctx, canvas.Background())
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	js.Global().Call("requestAnimationFrame", frame)

	select {} // keep the Go program (and its callbacks) alive
}
