// Command modal shows a modal-style popup that contains a text field.
//
//	go run ella.to/microui/examples/modal
//
// Click "Edit name…" to open a popup with a textbox; type a value and press
// Enter or click Done to commit it (or click anywhere outside to dismiss).
// Press Ctrl-C to quit.
package main

import (
	"fmt"
	"os"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

const popupName = "edit-name"

// the value the modal edits, persistent across frames.
var name = "world"

func ui(ctx *microui.Context) {
	if !ctx.BeginWindow("Modal Demo", microui.Rect{X: 2, Y: 1, W: 46, H: 9}).Active() {
		return
	}
	defer ctx.EndWindow()

	ctx.LayoutRow([]int{-1}, 0)
	ctx.Label("Name: " + name)
	if ctx.Button("Edit name…").Submitted() {
		ctx.OpenPopup(popupName) // opens at the mouse on click
	}

	// The popup auto-closes when you click outside it; Enter or Done close it
	// explicitly with ClosePopup, which takes effect from the next frame.
	if ctx.BeginPopup(popupName).Active() {
		done := false
		ctx.LayoutRow([]int{6, 18}, 0)
		ctx.Label("Name:")
		if ctx.Textbox(&name).Submitted() {
			done = true
		}
		ctx.LayoutRow([]int{24}, 0)
		if ctx.Button("Done").Submitted() {
			done = true
		}
		if done {
			ctx.ClosePopup(popupName)
		}
		ctx.EndPopup()
	}
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "modal:", err)
		os.Exit(1)
	}
	if err := renderer.Run(term, ui); err != nil {
		fmt.Fprintln(os.Stderr, "modal:", err)
		os.Exit(1)
	}
}
