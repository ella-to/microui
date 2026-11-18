package main

import (
	"strings"
	"testing"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// The smallest terminal the gallery is designed for.
const screenW, screenH = 80, 24

// newCtx returns a context wired to a headless terminal's metrics and style.
func newCtx(t *testing.T) (*microui.Context, *renderer.Terminal) {
	t.Helper()
	term := renderer.NewHeadless(screenW, screenH)
	ctx := microui.New()
	renderer.Connect(ctx, term)
	st := term.Style()
	ctx.Style = &st
	return ctx, term
}

// runner builds frames of body inside a full-size test window.
type runner struct {
	ctx  *microui.Context
	body func()
}

func (r *runner) frame() {
	r.ctx.Begin()
	if r.ctx.BeginWindowEx("test", microui.Rect{W: screenW, H: screenH}, microui.OptNoTitle|microui.OptNoClose).Active() {
		r.ctx.LayoutRow([]int{30}, 0)
		r.body()
		r.ctx.EndWindow()
	}
	r.ctx.End()
}

// find returns the position of the first text command containing s.
func find(ctx *microui.Context, s string) (microui.Vec2, bool) {
	for cmd := range ctx.Commands() {
		if cmd.Type == microui.CommandText && strings.Contains(cmd.Str, s) {
			return cmd.Pos, true
		}
	}
	return microui.Vec2{}, false
}

// click hovers p for two frames (hover root, then hover), then presses and
// releases the left button over it.
func click(ctx *microui.Context, frame func(), p microui.Vec2) {
	ctx.InputMouseMove(p.X, p.Y)
	frame()
	frame()
	ctx.InputMouseDown(p.X, p.Y, microui.MouseLeft)
	frame()
	ctx.InputMouseUp(p.X, p.Y, microui.MouseLeft)
	frame()
}

// clickText renders a frame, finds s and clicks it.
func clickText(t *testing.T, ctx *microui.Context, frame func(), s string) {
	t.Helper()
	frame()
	p, ok := find(ctx, s)
	if !ok {
		t.Fatalf("%q not drawn", s)
	}
	click(ctx, frame, p)
}

func TestToggleFlips(t *testing.T) {
	ctx, _ := newCtx(t)
	on, changes := false, 0
	r := &runner{ctx: ctx, body: func() {
		if Toggle(ctx, "Wi-Fi", &on).Changed() {
			changes++
		}
	}}
	clickText(t, ctx, r.frame, "[OFF ]")
	if !on || changes != 1 {
		t.Fatalf("after click: on=%v changes=%d, want true/1", on, changes)
	}
	clickText(t, ctx, r.frame, "[ ON ]")
	if on || changes != 2 {
		t.Fatalf("after second click: on=%v changes=%d, want false/2", on, changes)
	}
}

func TestRadioGroupSelectsGenericValue(t *testing.T) {
	ctx, _ := newCtx(t)
	size := Small
	r := &runner{ctx: ctx, body: func() {
		RadioGroup(ctx, &size, sizes, []string{"Small", "Medium", "Large"})
	}}
	clickText(t, ctx, r.frame, "Large")
	if size != Large {
		t.Fatalf("size = %d, want Large", size)
	}
	if _, ok := find(ctx, "(•) Large"); !ok {
		t.Fatal("selected option is not marked")
	}
}

func TestTabsChangeIndex(t *testing.T) {
	ctx, _ := newCtx(t)
	tab := 0
	var res microui.Result
	r := &runner{ctx: ctx, body: func() {
		if got := Tabs(ctx, &tab, "One", "Two", "Three"); got.Changed() {
			res = got
		}
	}}
	clickText(t, ctx, r.frame, "Three")
	if tab != 2 || !res.Changed() {
		t.Fatalf("tab = %d (changed=%v), want 2", tab, res.Changed())
	}
}

func TestSelectPicksFromPopup(t *testing.T) {
	ctx, _ := newCtx(t)
	fruit := "Apple"
	r := &runner{ctx: ctx, body: func() {
		Select(ctx, "fruit", &fruit, fruits, func(s string) string { return s })
	}}
	clickText(t, ctx, r.frame, "Apple ▾") // opens the popup
	r.frame()
	if _, ok := find(ctx, "Durian"); !ok {
		t.Fatal("popup did not open")
	}
	clickText(t, ctx, r.frame, "Cherry")
	if fruit != "Cherry" {
		t.Fatalf("fruit = %q, want Cherry", fruit)
	}
	r.frame()
	if _, ok := find(ctx, "Durian"); ok {
		t.Fatal("popup still open after picking")
	}
}

func TestColorSwatchSubmits(t *testing.T) {
	ctx, _ := newCtx(t)
	hits := 0
	r := &runner{ctx: ctx, body: func() {
		if ColorSwatch(ctx, swatches[1]).Submitted() {
			hits++
		}
	}}
	clickText(t, ctx, r.frame, "#2e9e5b")
	if hits != 1 {
		t.Fatalf("swatch submitted %d times, want 1", hits)
	}
}

// appFrame builds one frame of the whole gallery.
func appFrame(ctx *microui.Context, a *App) func() {
	return func() {
		ctx.Begin()
		a.ui(ctx)
		ctx.End()
	}
}

func TestAppThemeAndFlatToggle(t *testing.T) {
	ctx, term := newCtx(t)
	a := NewApp(&term.BG)
	a.setup(ctx)
	frame := appFrame(ctx, a)

	clickText(t, ctx, frame, "Theme") // the tab
	if a.tab != 2 {
		t.Fatalf("tab = %d, want 2 (Theme)", a.tab)
	}

	clickText(t, ctx, frame, "Dark ▾")
	clickText(t, ctx, frame, "Light")
	if a.theme != 1 || term.BG != a.themes[1].BG {
		t.Fatalf("theme = %d, bg = %+v; want Light applied", a.theme, term.BG)
	}
	if ctx.Style.Colors[microui.ColorWindowBG] != a.themes[1].Style.Colors[microui.ColorWindowBG] {
		t.Fatal("theme style not installed into ctx.Style")
	}

	clickText(t, ctx, frame, "Flat (no borders)")
	if !a.flat {
		t.Fatal("flat toggle did not flip")
	}
	// With the flat frame no border-colored rects are emitted.
	frame()
	border := ctx.Style.Colors[microui.ColorBorder]
	for cmd := range ctx.Commands() {
		if cmd.Type == microui.CommandRect && cmd.Color == border && cmd.Rect.H == 1 && cmd.Rect.W > 2 {
			t.Fatal("flat mode still draws borders")
		}
	}
	clickText(t, ctx, frame, "Flat (no borders)")
	if a.flat {
		t.Fatal("flat toggle did not flip back")
	}

	if len(a.log) == 0 || !strings.Contains(strings.Join(a.log, "\n"), "theme -> Light") {
		t.Fatalf("log = %q, want a theme entry", a.log)
	}
}

func TestAppRendersEveryTab(t *testing.T) {
	ctx, term := newCtx(t)
	a := NewApp(&term.BG)
	a.setup(ctx)
	frame := appFrame(ctx, a)
	for tab := range 3 {
		a.tab = tab
		for range 3 {
			frame()
		}
	}
}

func TestAppFitsSmallTerminal(t *testing.T) {
	ctx, term := newCtx(t)
	a := NewApp(&term.BG)
	a.setup(ctx)
	frame := appFrame(ctx, a)
	pad := ctx.Style.Padding
	for tab := range 3 {
		a.tab = tab
		frame()
		frame() // ContentSize is recorded at the end of a frame
		for _, name := range []string{"Widgets", "Log"} {
			cnt := ctx.GetContainer(name)
			if r := cnt.Rect; r.X < 0 || r.Y < 0 || r.X+r.W > screenW || r.Y+r.H > screenH {
				t.Fatalf("tab %d: window %s %+v does not fit %dx%d", tab, name, r, screenW, screenH)
			}
		}
		cnt := ctx.GetContainer("Widgets")
		if need := cnt.ContentSize.Y + 2*pad; need > cnt.Body.H {
			t.Errorf("tab %d needs %d rows but the window has %d", tab, need, cnt.Body.H)
		}
	}
}

func TestSelectPopupsStayOnScreen(t *testing.T) {
	ctx, term := newCtx(t)
	a := NewApp(&term.BG)
	a.setup(ctx)
	frame := appFrame(ctx, a)
	for _, sel := range []struct{ button, name string }{{"Apple ▾", "fruit"}, {"P2 ▾", "priority"}} {
		clickText(t, ctx, frame, sel.button)
		frame() // the popup sizes itself to its content on the next frame
		// The popup was opened inside the "Widgets" window, so its name is
		// hashed in that window's id scope.
		ctx.PushID("Widgets")
		cnt := ctx.GetContainer("select:" + sel.name)
		ctx.PopID()
		if !cnt.Open || cnt.Rect.Y+cnt.Rect.H > screenH {
			t.Fatalf("%s popup %+v (open=%v) runs off a %d-row screen", sel.name, cnt.Rect, cnt.Open, screenH)
		}
		click(ctx, frame, microui.Vec2{X: screenW - 1, Y: screenH - 1}) // click away to close it
		if cnt.Open {
			t.Fatalf("%s popup did not close on an outside click", sel.name)
		}
	}
}
