// Command textinput is a minimal text-entry demo: type into a textbox and the
// window echoes what you typed.
//
//	go run ella.to/microui/examples/textinput
//
// It shows the textbox control, the row layout, and how the same UI builder runs
// on any renderer.Driver. Click the textbox to focus it, type, and press Ctrl-C
// to quit.
package main

import (
	"fmt"
	"os"

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

// edited text, persistent across frames.
var name string

func ui(ctx *miniui.Context) {
	if ctx.BeginWindow("Text Input", miniui.Rect{X: 2, Y: 1, W: 46, H: 11}) == 0 {
		return
	}
	defer ctx.EndWindow()

	// A label in a fixed-width column, then a textbox filling the rest.
	ctx.LayoutRow([]int{8, -1}, 0)
	ctx.Label("Name:")
	ctx.Textbox(&name)

	// A full-width line that reacts to what was typed.
	ctx.LayoutRow([]int{-1}, 0)
	if name == "" {
		ctx.Label("(click the field and type)")
	} else {
		ctx.Label("Hello, " + name + "!")
	}
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "textinput:", err)
		os.Exit(1)
	}
	if err := renderer.Run(term, ui); err != nil {
		fmt.Fprintln(os.Stderr, "textinput:", err)
		os.Exit(1)
	}
}
