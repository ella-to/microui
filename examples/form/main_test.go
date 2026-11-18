package main

import (
	"testing"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// The tests run at the smallest terminal the form is designed for.
const screenW, screenH = 80, 24

// harness drives a UI function headlessly, frame by frame.
type harness struct {
	t   *testing.T
	ctx *microui.Context
	ui  func(*microui.Context)
}

func newHarness(t *testing.T, ui func(*microui.Context)) *harness {
	term := renderer.NewHeadless(screenW, screenH)
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

func newTestForm(t *testing.T) (*harness, *Form) {
	f := newForm(func() (int, int) { return screenW, screenH })
	return newHarness(t, f.UI), f
}

func TestTabCyclesFocus(t *testing.T) {
	h, f := newTestForm(t)
	if len(f.fields) != 3 || h.ctx.Focus() != f.fields[0] {
		t.Fatal("first field should be autofocused")
	}
	for _, want := range []int{1, 2, 0} {
		h.key(microui.KeyTab)
		if h.ctx.Focus() != f.fields[want] {
			t.Fatalf("Tab: focus not on field %d", want)
		}
	}
	h.key(microui.KeyShift | microui.KeyTab)
	if h.ctx.Focus() != f.fields[2] {
		t.Fatal("Shift+Tab from the first field should wrap to the last")
	}
}

func TestEnterAdvancesFocus(t *testing.T) {
	h, f := newTestForm(t)
	h.typeText("Ada")
	h.key(microui.KeyReturn)
	if f.Name != "Ada" || h.ctx.Focus() != f.fields[1] {
		t.Fatalf("Enter: name=%q, focus on email=%v", f.Name, h.ctx.Focus() == f.fields[1])
	}
}

func TestInvalidEmailBlocksSubmitThenModal(t *testing.T) {
	h, f := newTestForm(t)
	h.typeText("Ada Lovelace")
	h.key(microui.KeyTab)
	h.typeText("nope")
	h.key(microui.KeyTab)
	h.typeText("correct horse")
	f.Agree = true
	h.frame()

	if f.problems() != 1 {
		t.Fatalf("problems = %d, want 1 (the email)", f.problems())
	}
	h.click(h.find("Submit"))
	if f.modal || f.submits != 0 {
		t.Fatal("an invalid form must not submit")
	}

	// fix the email: click into it, select all, retype
	h.click(h.find("nope"))
	h.key(microui.KeySelectAll)
	h.typeText("ada@example.com")
	if f.problems() != 0 {
		t.Fatalf("problems = %d after fixing the email", f.problems())
	}
	h.click(h.find("Submit"))
	if !f.modal {
		t.Fatal("a valid form should open the modal")
	}

	// the backdrop is a semi-transparent rect over the whole screen
	backdropSeen := false
	for cmd := range h.ctx.Commands() {
		if cmd.Type == microui.CommandRect && cmd.Color == backdrop {
			backdropSeen = true
		}
	}
	if !backdropSeen {
		t.Fatal("modal backdrop not drawn")
	}

	h.key(microui.KeyEscape)
	if f.modal {
		t.Fatal("Esc should close the modal")
	}
}

func TestFitsSmallTerminal(t *testing.T) {
	h, _ := newTestForm(t)
	cnt := h.ctx.GetContainer("Sign up")
	if r := cnt.Rect; r.X < 0 || r.Y < 0 || r.X+r.W > screenW || r.Y+r.H > screenH {
		t.Fatalf("window %+v does not fit %dx%d", r, screenW, screenH)
	}
	if need := cnt.ContentSize.Y + 2*h.ctx.Style.Padding; need > cnt.Body.H {
		t.Fatalf("form needs %d rows but the window body has %d: it would scroll", need, cnt.Body.H)
	}
}
