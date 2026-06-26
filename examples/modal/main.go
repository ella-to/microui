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

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

const popupName = "edit-name"

// the value the modal edits, persistent across frames.
var name = "world"

func ui(ctx *miniui.Context) {
	if ctx.BeginWindow("Modal Demo", miniui.Rect{X: 2, Y: 1, W: 46, H: 9}) == 0 {
		return
	}
	defer ctx.EndWindow()

	ctx.LayoutRow([]int{-1}, 0)
	ctx.Label("Name: " + name)
	if ctx.Button("Edit name…") != 0 {
		ctx.OpenPopup(popupName) // opens at the mouse on click
	}

	// The popup auto-closes when you click outside it; Enter or Done close it
	// explicitly by marking its container closed for the next frame.
	if ctx.BeginPopup(popupName) != 0 {
		close := false
		ctx.LayoutRow([]int{6, 18}, 0)
		ctx.Label("Name:")
		if ctx.Textbox(&name)&miniui.ResSubmit != 0 {
			close = true
		}
		ctx.LayoutRow([]int{24}, 0)
		if ctx.Button("Done") != 0 {
			close = true
		}
		if close {
			ctx.GetContainer(popupName).Open = false
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
