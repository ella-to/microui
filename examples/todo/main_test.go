package main

import (
	"testing"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// harness drives a UI function headlessly, frame by frame.
type harness struct {
	t   *testing.T
	ctx *microui.Context
	ui  func(*microui.Context)
}

func newHarness(t *testing.T, ui func(*microui.Context)) *harness {
	term := renderer.NewHeadless(100, 30)
	ctx := microui.New()
	renderer.Connect(ctx, term)
	st := term.Style()
	ctx.Style = &st
	h := &harness{t: t, ctx: ctx, ui: ui}
	h.frame()
	h.frame()
	return h
}

func (h *harness) frame() {
	h.ctx.Begin()
	h.ui(h.ctx)
	h.ctx.End()
}

// key taps k (which may combine modifier bits) for one frame.
func (h *harness) key(k microui.Key) {
	h.ctx.InputKeyDown(k)
	h.ctx.InputKeyUp(k)
	h.frame()
}

func (h *harness) typeText(s string) {
	h.ctx.InputText(s)
	h.frame()
}

// find returns the position of the first text command drawing s.
func (h *harness) find(s string) microui.Vec2 {
	h.t.Helper()
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandText && cmd.Str == s {
			return cmd.Pos
		}
	}
	h.t.Fatalf("text %q not on screen", s)
	return microui.Vec2{}
}

// click hovers p (hover needs two frames to settle), then presses and
// releases the left button.
func (h *harness) click(p microui.Vec2) {
	h.ctx.InputMouseMove(p.X, p.Y)
	h.frame()
	h.frame()
	h.ctx.InputMouseDown(p.X, p.Y, microui.MouseLeft)
	h.frame()
	h.ctx.InputMouseUp(p.X, p.Y, microui.MouseLeft)
	h.frame()
}

func TestEnterAddsItemAndKeepsFocus(t *testing.T) {
	app := newApp(func() {})
	app.items = nil
	h := newHarness(t, app.UI)

	h.typeText("buy milk")
	h.key(microui.KeyReturn)
	if len(app.items) != 1 || app.items[0].Text != "buy milk" {
		t.Fatalf("items = %+v, want one \"buy milk\"", app.items)
	}
	if app.input != "" {
		t.Fatalf("input not cleared: %q", app.input)
	}
	if h.ctx.Focus() != app.inputID {
		t.Fatal("textbox lost focus after Enter")
	}
}

func TestKeyboardSelectToggleDelete(t *testing.T) {
	app := newApp(func() {})
	h := newHarness(t, app.UI)

	h.key(microui.KeyEscape) // leave the textbox; the list takes the keys
	if h.ctx.Focus() != 0 {
		t.Fatal("Esc did not release focus")
	}
	h.key(microui.KeyDown)
	if app.selected != 1 {
		t.Fatalf("selected = %d after Down, want 1", app.selected)
	}
	h.key(microui.KeyReturn) // item 1 starts done
	if app.items[1].Done {
		t.Fatal("Enter did not toggle the selected item")
	}
	h.key(microui.KeyDelete)
	if len(app.items) != 2 || app.items[1].Text != "Ship it" {
		t.Fatalf("after Delete items = %+v", app.items)
	}
}

func TestDeleteButtonRemovesItsRow(t *testing.T) {
	app := newApp(func() {})
	h := newHarness(t, app.UI)

	h.click(h.find("x")) // the first row's delete button
	if len(app.items) != 2 || app.items[0].Text == "Read the GUIDE" {
		t.Fatalf("delete removed the wrong row: %+v", app.items)
	}
}

func TestFilterTabsAndQuit(t *testing.T) {
	quit := false
	app := newApp(func() { quit = true })
	h := newHarness(t, app.UI)

	h.click(h.find("Done"))
	if app.filter != showDone || len(app.view()) != 1 {
		t.Fatalf("filter = %d, view = %v", app.filter, app.view())
	}
	h.click(h.find("Quit"))
	if !quit {
		t.Fatal("Quit button did not call quit")
	}
}
