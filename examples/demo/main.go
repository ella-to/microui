// Command demo is the full miniui showcase: two windows exercising most of the
// built-in controls — headers, buttons, a popup, a tree of checkboxes, RGB
// sliders that recolor the background live, and a scrolling log with a textbox.
//
//	go run ella.to/microui/examples/demo
//
// It is the most complete reference for wiring a UI to a renderer.Driver, and it
// shows that the background color (owned by the driver) can be changed from the
// UI. Press Ctrl-C to quit.
package main

import (
	"fmt"
	"os"

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

// demo state, persistent across frames.
var (
	bg         = [3]miniui.Real{40, 44, 52}
	logbuf     string
	logUpdated bool
	textbuf    string
	checks     = [3]bool{true, false, true}
)

func writeLog(s string) {
	if logbuf != "" {
		logbuf += "\n"
	}
	logbuf += s
	logUpdated = true
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
	// The UI builder closes over term so the RGB sliders can recolor the
	// driver's background. Everything else only touches *miniui.Context.
	err = renderer.Run(term, func(ctx *miniui.Context) {
		testWindow(ctx)
		logWindow(ctx)
		term.BG = miniui.RGBA(int(bg[0]), int(bg[1]), int(bg[2]), 255)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

func testWindow(ctx *miniui.Context) {
	if ctx.BeginWindow("Demo", miniui.Rect{X: 1, Y: 1, W: 40, H: 22}) == 0 {
		return
	}
	win := ctx.GetCurrentContainer()
	win.Rect.W = max(win.Rect.W, 24)
	win.Rect.H = max(win.Rect.H, 8)

	if ctx.Header("Window Info") != 0 {
		ctx.LayoutRow([]int{10, -1}, 0)
		ctx.Label("Position:")
		ctx.Label(fmt.Sprintf("%d, %d", win.Rect.X, win.Rect.Y))
		ctx.Label("Size:")
		ctx.Label(fmt.Sprintf("%d, %d", win.Rect.W, win.Rect.H))
	}

	if ctx.HeaderEx("Buttons", miniui.OptExpanded) != 0 {
		ctx.LayoutRow([]int{12, 12}, 0)
		if ctx.Button("Button 1") != 0 {
			writeLog("Pressed Button 1")
		}
		if ctx.Button("Button 2") != 0 {
			writeLog("Pressed Button 2")
		}
		if ctx.Button("Popup") != 0 {
			ctx.OpenPopup("Popup!")
		}
		if ctx.BeginPopup("Popup!") != 0 {
			if ctx.Button("Hello") != 0 {
				writeLog("Hello")
			}
			if ctx.Button("World") != 0 {
				writeLog("World")
			}
			ctx.EndPopup()
		}
	}

	if ctx.HeaderEx("Tree", miniui.OptExpanded) != 0 {
		if ctx.BeginTreenode("Options") != 0 {
			ctx.Checkbox("Check 1", &checks[0])
			ctx.Checkbox("Check 2", &checks[1])
			ctx.Checkbox("Check 3", &checks[2])
			ctx.EndTreenode()
		}
	}

	if ctx.HeaderEx("Background", miniui.OptExpanded) != 0 {
		ctx.LayoutRow([]int{7, -1}, 0)
		ctx.Label("Red:")
		ctx.SliderEx(&bg[0], 0, 255, 0, "%.0f", miniui.OptAlignCenter)
		ctx.Label("Green:")
		ctx.SliderEx(&bg[1], 0, 255, 0, "%.0f", miniui.OptAlignCenter)
		ctx.Label("Blue:")
		ctx.SliderEx(&bg[2], 0, 255, 0, "%.0f", miniui.OptAlignCenter)
	}

	ctx.EndWindow()
}

func logWindow(ctx *miniui.Context) {
	if ctx.BeginWindow("Log", miniui.Rect{X: 42, Y: 1, W: 36, H: 22}) == 0 {
		return
	}

	// scrollable output panel that fills all but the bottom input row
	ctx.LayoutRow([]int{-1}, -4)
	ctx.BeginPanel("Log Output")
	panel := ctx.GetCurrentContainer()
	ctx.LayoutRow([]int{-1}, -1)
	ctx.Text(logbuf)
	ctx.EndPanel()
	if logUpdated {
		panel.Scroll.Y = panel.ContentSize.Y
		logUpdated = false
	}

	// input textbox + submit button
	submitted := false
	ctx.LayoutRow([]int{-9, -1}, 0)
	if ctx.Textbox(&textbuf)&miniui.ResSubmit != 0 {
		ctx.SetFocus(ctx.LastID())
		submitted = true
	}
	if ctx.Button("Submit") != 0 {
		submitted = true
	}
	if submitted && textbuf != "" {
		writeLog(textbuf)
		textbuf = ""
	}

	ctx.EndWindow()
}
