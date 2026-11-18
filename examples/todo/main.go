// Command todo is a small but complete todo-list app: add items, tick them
// off, filter them, and drive the list from the keyboard.
//
//	go run ella.to/microui/examples/todo
//
// It demonstrates the patterns most real microui apps need:
//
//   - all state lives in an App struct; the UI is a method on it
//   - a textbox that keeps focus after Enter so you can add items in a row
//   - a scrollable list where every row is scoped with PushID(i), so the
//     repeated "x" delete buttons stay distinct
//   - mutations requested inside the row loop are deferred until after it
//   - filter tabs built as a custom control from LayoutNext/UpdateControl
//   - keyboard selection (↑/↓, PgUp/PgDn, Enter, Del) that scrolls the panel
//     to keep the selected row visible
//   - a Quit button that ends renderer.Run through a cancelled context
//
// Press Esc to leave the text field and use the keyboard on the list; click
// the text field to type again. Ctrl-C also quits.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// item is one todo entry.
type item struct {
	Text string
	Done bool
}

// filter selects which items the list shows.
type filter int

const (
	showAll filter = iota
	showActive
	showDone
)

var filterNames = [...]string{"All", "Active", "Done"}

func (f filter) match(it item) bool {
	switch f {
	case showActive:
		return !it.Done
	case showDone:
		return it.Done
	}
	return true
}

var (
	selectedBG = microui.Hex(0x3a4f6b) // keyboard-selected row
	doneText   = microui.Hex(0x7f8794) // label of a completed item
	accent     = microui.Hex(0x4f7cac) // active filter tab
)

// App holds all state for the todo list, persistent across frames.
type App struct {
	input    string
	items    []item
	filter   filter
	selected int        // position of the highlighted row in the filtered view
	inputID  microui.ID // the textbox's id, captured with LastID
	started  bool
	quit     func()
}

func newApp(quit func()) *App {
	return &App{
		quit: quit,
		items: []item{
			{Text: "Read the GUIDE"},
			{Text: "Build a terminal UI", Done: true},
			{Text: "Ship it"},
		},
	}
}

// add appends the typed text as a new item.
func (a *App) add() {
	text := strings.TrimSpace(a.input)
	if text == "" {
		return
	}
	a.items = append(a.items, item{Text: text})
	a.input = ""
}

// view returns the indices (into a.items) of the items the filter shows.
func (a *App) view() []int {
	var idx []int
	for i, it := range a.items {
		if a.filter.match(it) {
			idx = append(idx, i)
		}
	}
	return idx
}

// UI builds one frame. It only needs a *microui.Context, so it runs on any
// renderer and in tests without a terminal.
func (a *App) UI(ctx *microui.Context) {
	if !ctx.BeginWindowEx("Todo", microui.Rect{X: 2, Y: 1, W: 56, H: 22}, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()

	// Keyboard routing: Esc releases the textbox; while nothing is focused,
	// the list owns the arrow keys. Focus() still reflects last frame here.
	keys := ctx.KeyPressed()
	if keys&microui.KeyEscape != 0 {
		ctx.SetFocus(0)
	}
	listHasKeys := ctx.Focus() == 0

	// --- input row -----------------------------------------------------------
	ctx.LayoutRow([]int{-9, -1}, 1)
	add := ctx.Textbox(&a.input).Submitted()
	a.inputID = ctx.LastID()
	if add {
		// Enter drops focus by default; take it back so typing can continue.
		ctx.SetFocus(a.inputID)
	}
	if ctx.Button("Add").Submitted() {
		add = true
	}
	if add {
		a.add()
	}
	if !a.started {
		a.started = true
		ctx.SetFocus(a.inputID) // autofocus the field on the first frame
	}

	// --- filter tabs ---------------------------------------------------------
	ctx.LayoutRow([]int{8, 8, 8}, 1)
	ctx.PushID("tabs") // scope the tab ids so "Done" cannot clash with anything
	for f, name := range filterNames {
		if tab(ctx, name, a.filter == filter(f)) {
			a.filter, a.selected = filter(f), 0
		}
	}
	ctx.PopID()

	// --- keyboard selection --------------------------------------------------
	view := a.view()
	a.selected = clampSel(a.selected, len(view))
	remove := -1 // index into a.items to delete once the loop is done
	moved := false
	if listHasKeys && len(view) > 0 {
		switch {
		case keys&microui.KeyUp != 0:
			a.selected, moved = clampSel(a.selected-1, len(view)), true
		case keys&microui.KeyDown != 0:
			a.selected, moved = clampSel(a.selected+1, len(view)), true
		case keys&microui.KeyPageUp != 0:
			a.selected, moved = clampSel(a.selected-5, len(view)), true
		case keys&microui.KeyPageDown != 0:
			a.selected, moved = clampSel(a.selected+5, len(view)), true
		case keys&microui.KeyReturn != 0:
			it := &a.items[view[a.selected]]
			it.Done = !it.Done
		case keys&(microui.KeyDelete|microui.KeyBackspace) != 0:
			remove = view[a.selected]
		}
	}

	// --- the list ------------------------------------------------------------
	// The panel fills the window except the footer's two rows (plus spacing).
	ctx.LayoutRow([]int{-1}, -5)
	ctx.BeginPanel("items")
	panel := ctx.GetCurrentContainer()
	var selRow microui.Rect
	for pos, i := range view {
		it := &a.items[i] // bind to the element, never to a range copy
		ctx.PushID(i)     // every row's "x" button gets its own id

		// Claim the whole row, paint the highlight behind it, then place the
		// controls inside it with absolute LayoutSetNext rectangles.
		ctx.LayoutRow([]int{-1}, 1)
		row := ctx.LayoutNext()
		if pos == a.selected {
			ctx.DrawRect(row, selectedBG)
			selRow = row
		}

		ctx.LayoutSetNext(microui.Rect{X: row.X, Y: row.Y, W: row.W - 4, H: row.H}, false)
		saved := ctx.Style.Colors[microui.ColorText]
		if it.Done {
			ctx.Style.Colors[microui.ColorText] = doneText // restyle just this control
		}
		if ctx.Checkbox(it.Text, &it.Done).Changed() {
			a.selected = pos
		}
		ctx.Style.Colors[microui.ColorText] = saved

		ctx.LayoutSetNext(microui.Rect{X: row.X + row.W - 3, Y: row.Y, W: 3, H: row.H}, false)
		if ctx.Button("x").Submitted() {
			// Don't delete here: removing from a.items mid-loop would shift
			// every later row (and the pointers bound to them). Record it.
			remove = i
		}
		ctx.PopID()
	}
	if len(view) == 0 {
		ctx.LayoutRow([]int{-1}, 1)
		ctx.Label("Nothing here — type above and press Enter.")
	}
	ctx.EndPanel()

	// Keep the keyboard selection visible: nudge the panel's scroll so the
	// selected row lies inside its body (applied on the next frame).
	if moved && !selRow.Empty() {
		body := panel.Body
		switch {
		case selRow.Y < body.Y:
			panel.Scroll.Y -= body.Y - selRow.Y
		case selRow.Y+selRow.H > body.Y+body.H:
			panel.Scroll.Y += selRow.Y + selRow.H - (body.Y + body.H)
		}
	}

	// The loop is over: now it is safe to mutate the slice.
	if remove >= 0 {
		a.items = slices.Delete(a.items, remove, remove+1)
	}

	// --- footer --------------------------------------------------------------
	left := 0
	for _, it := range a.items {
		if !it.Done {
			left++
		}
	}
	ctx.LayoutRow([]int{-1}, 1)
	ctx.Label(fmt.Sprintf("%d item%s left", left, plural(left)))

	ctx.LayoutRow([]int{12, 8, -1}, 1)
	if ctx.Button("Clear done").Submitted() {
		a.items = slices.DeleteFunc(a.items, func(it item) bool { return it.Done })
	}
	if ctx.Button("Quit").Submitted() {
		a.quit()
	}
	ctx.Label("Esc: list keys  ↑↓ Enter Del")
}

// tab is a custom control built from the same primitives as the built-in
// ones: claim a cell, run hover/focus logic, draw, then report a click.
func tab(ctx *microui.Context, label string, active bool) bool {
	id := ctx.GetID(label)
	r := ctx.LayoutNext()
	ctx.UpdateControl(id, r, 0)
	if active {
		ctx.DrawRect(r, accent)
	} else {
		ctx.DrawControlFrame(id, r, microui.ColorButton, 0)
	}
	ctx.DrawControlText(label, r, microui.ColorText, microui.OptAlignCenter)
	return ctx.MousePressed()&microui.MouseLeft != 0 && ctx.Focus() == id
}

// clampSel clamps a selection position to a view of n rows (0 when empty).
func clampSel(sel, n int) int { return max(0, min(sel, n-1)) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		os.Exit(1)
	}
	// Cancelling this context (the Quit button) makes Run return cleanly, so
	// deferred cleanup runs and the terminal is restored.
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := newApp(cancel)
	if err := renderer.Run(term, app.UI, renderer.WithContext(runCtx)); err != nil {
		fmt.Fprintln(os.Stderr, "todo:", err)
		os.Exit(1)
	}
}
