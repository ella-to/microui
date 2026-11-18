// Command widgets is a gallery of reusable custom controls built on microui's
// public primitives, plus runtime theme switching.
//
//	go run ella.to/microui/examples/widgets
//
// Every widget below (Toggle, RadioGroup, Tabs, Select, ProgressBar, Spinner,
// ColorSwatch) is a plain function taking a *microui.Context, written to be
// copied into your own project. They follow the same recipe as the built-in
// controls:
//
//  1. derive a stable ID (from a label, a loop index or a variable's address),
//  2. claim a rectangle with LayoutNext,
//  3. run UpdateControl to resolve hover/focus,
//  4. react to input (Focus()==id && MousePressed()) and mutate bound state,
//  5. draw with DrawRect / DrawControlFrame / DrawControlText,
//  6. return a microui.Result so callers can test Changed()/Submitted().
//
// The "Theme" tab swaps the whole Style at runtime and toggles a flat,
// borderless look by replacing ctx.DrawFrame. Press Ctrl-C to quit.
package main

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// ptrID returns an ID derived from p's address, the way the built-in value
// controls (Checkbox, Slider, Textbox) identify themselves: the bound
// variable must persist across frames, and then no label is needed to keep
// two otherwise identical widgets apart.
func ptrID[T any](ctx *microui.Context, p *T) microui.ID {
	return ctx.GetID(uintptr(unsafe.Pointer(p)))
}

// clicked reports whether the control id was clicked with the left button
// this frame. Call it after UpdateControl.
func clicked(ctx *microui.Context, id microui.ID) bool {
	return ctx.Focus() == id && ctx.MousePressed() == microui.MouseLeft
}

// accent is the highlight color the widgets share. They read it from the
// style's selection slot so themes (and the color swatches) restyle them.
func accent(ctx *microui.Context) microui.Color {
	return ctx.Style.Colors[microui.ColorSelection]
}

// ---- Toggle -----------------------------------------------------------------

// Toggle draws an on/off switch "[ ON ]" / "[OFF ]" followed by label, bound
// to on. The result is Changed on the frame it flips.
func Toggle(ctx *microui.Context, label string, on *bool) microui.Result {
	var res microui.Result
	id := ptrID(ctx, on)
	r := ctx.LayoutNext()
	ctx.UpdateControl(id, r, 0)
	if clicked(ctx, id) {
		*on = !*on
		res |= microui.ResChange
	}

	sw := microui.Rect{X: r.X, Y: r.Y, W: 6, H: r.H}
	text := "[OFF ]"
	if *on {
		text = "[ ON ]"
		c := accent(ctx)
		if ctx.Hover() == id {
			c = c.Lerp(microui.Hex(0xffffff), 0.15)
		}
		ctx.DrawRect(sw, c)
	} else {
		ctx.DrawControlFrame(id, sw, microui.ColorBase, 0)
	}
	ctx.DrawControlText(text, sw, microui.ColorText, microui.OptAlignCenter)
	rest := microui.Rect{X: sw.X + sw.W, Y: r.Y, W: r.W - sw.W, H: r.H}
	ctx.DrawControlText(label, rest, microui.ColorText, 0)
	return res
}

// ---- RadioGroup -------------------------------------------------------------

// RadioGroup draws one "(•) label" row per option, bound to value. It is
// generic, so value can be any comparable type: an enum, a string, a struct.
// The result is Changed when the selection moves.
func RadioGroup[T comparable](ctx *microui.Context, value *T, options []T, labels []string) microui.Result {
	var res microui.Result
	ctx.PushID(uintptr(unsafe.Pointer(value))) // scope the rows to this group
	defer ctx.PopID()
	for i, opt := range options {
		id := ctx.GetID(i) // unique per row within the group's scope
		r := ctx.LayoutNext()
		ctx.UpdateControl(id, r, 0)
		if clicked(ctx, id) && *value != opt {
			*value = opt
			res |= microui.ResChange
		}
		mark := "( ) "
		if *value == opt {
			mark = "(•) "
		}
		if ctx.Hover() == id {
			ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBaseHover])
		}
		ctx.DrawControlText(mark+labels[i], r, microui.ColorText, 0)
	}
	return res
}

// ---- Tabs -------------------------------------------------------------------

// Tabs draws a row of tabs, one per label, bound to the active index. It
// starts its own LayoutRow sized to the labels, so start a new row after it.
// The result is Changed when the user picks another tab.
func Tabs(ctx *microui.Context, active *int, labels ...string) microui.Result {
	var res microui.Result
	var buf [16]int // LayoutRow copies widths, so a stack array avoids allocating
	widths := buf[:0]
	for _, l := range labels[:min(len(labels), len(buf))] {
		widths = append(widths, ctx.TextWidth(ctx.Style.Font, l)+4)
	}
	ctx.LayoutRow(widths, 1)

	ctx.PushID(uintptr(unsafe.Pointer(active)))
	defer ctx.PopID()
	for i := range widths {
		id := ctx.GetID(i)
		r := ctx.LayoutNext()
		ctx.UpdateControl(id, r, 0)
		if clicked(ctx, id) && *active != i {
			*active = i
			res |= microui.ResChange
		}
		if *active == i {
			ctx.DrawRect(r, accent(ctx))
		} else {
			ctx.DrawControlFrame(id, r, microui.ColorButton, 0)
		}
		ctx.DrawControlText(labels[i], r, microui.ColorText, microui.OptAlignCenter)
	}
	return res
}

// ---- Select -----------------------------------------------------------------

// Select draws a dropdown: a button showing label(*value) that opens a popup
// listing every option. name identifies the dropdown (and its popup). The
// result is Changed when an option is picked.
func Select[T comparable](ctx *microui.Context, name string, value *T, options []T, label func(T) string) microui.Result {
	var res microui.Result
	popup := "select:" + name

	id := ctx.GetID(name)
	r := ctx.LayoutNext()
	ctx.UpdateControl(id, r, 0)
	if clicked(ctx, id) {
		ctx.OpenPopup(popup)
		// OpenPopup places the popup at the mouse; drop it below the button
		// instead. Its size is computed from the content (OptAutoSize).
		ctx.GetContainer(popup).Rect = microui.Rect{X: r.X, Y: r.Y + r.H, W: 1, H: 1}
	}
	ctx.DrawControlFrame(id, r, microui.ColorButton, 0)
	ctx.DrawControlText(label(*value)+" ▾", r, microui.ColorText, 0)

	if ctx.BeginPopup(popup).Active() {
		w := r.W - 2*ctx.Style.Padding
		for _, opt := range options {
			w = max(w, ctx.TextWidth(ctx.Style.Font, label(opt))+2)
		}
		ctx.LayoutRow([]int{w}, 1)
		for i, opt := range options {
			ctx.PushID(i)
			picked := ctx.ButtonEx(label(opt), 0, 0).Submitted()
			ctx.PopID()
			if picked {
				if *value != opt {
					*value = opt
					res |= microui.ResChange
				}
				// Called directly in the popup's scope (not under PushID(i)),
				// ClosePopup resolves the popup's name correctly.
				ctx.ClosePopup(popup) // takes effect next frame
			}
		}
		ctx.EndPopup()
	}
	return res
}

// ---- ProgressBar, Spinner, ColorSwatch --------------------------------------

// ProgressBar draws a bar filled to frac (0..1) with a centered percentage.
func ProgressBar(ctx *microui.Context, frac float64) {
	r := ctx.LayoutNext()
	frac = min(max(frac, 0), 1)
	ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBase])
	fill := r
	fill.W = int(frac*float64(r.W) + 0.5)
	ctx.DrawRect(fill, accent(ctx))
	ctx.DrawControlText(fmt.Sprintf("%3.0f%%", frac*100), r, microui.ColorText, microui.OptAlignCenter)
}

// spinnerFrames is a braille spinner; each frame is one cell wide.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Spinner draws an animated busy indicator followed by label. It animates
// from wall-clock time rather than a frame counter, so its speed does not
// depend on the frame rate.
func Spinner(ctx *microui.Context, label string) {
	f := spinnerFrames[time.Now().UnixMilli()/80%int64(len(spinnerFrames))]
	ctx.Label(string(f) + " " + label)
}

// ColorSwatch draws a clickable color chip with its hex code. The result is
// Submitted when clicked.
func ColorSwatch(ctx *microui.Context, c microui.Color) microui.Result {
	var res microui.Result
	// the color's bits are a stable, allocation-free key
	id := ctx.GetID(uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | uint32(c.A))
	r := ctx.LayoutNext()
	ctx.UpdateControl(id, r, 0)
	if clicked(ctx, id) {
		res |= microui.ResSubmit
	}
	chip := microui.Rect{X: r.X, Y: r.Y, W: 4, H: r.H}
	if ctx.Hover() == id {
		ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBaseHover])
	}
	ctx.DrawRect(chip, c)
	rest := microui.Rect{X: chip.X + chip.W, Y: r.Y, W: r.W - chip.W, H: r.H}
	ctx.DrawControlText(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B), rest, microui.ColorText, 0)
	return res
}

// ---- themes -----------------------------------------------------------------

// Theme is a complete Style plus the backdrop color it is meant for.
type Theme struct {
	Name  string
	Style microui.Style
	BG    microui.Color
}

// makeThemes derives three themes from base, keeping its metrics (which the
// renderer tuned for its units) and replacing only the colors.
func makeThemes(base microui.Style) []Theme {
	set := func(st microui.Style, colors map[microui.ColorID]uint32) microui.Style {
		for id, hex := range colors {
			st.Colors[id] = microui.Hex(hex)
		}
		return st
	}
	light := set(base, map[microui.ColorID]uint32{
		microui.ColorText: 0x1e1e1e, microui.ColorBorder: 0xb0b0b0,
		microui.ColorWindowBG: 0xf2f2f2, microui.ColorTitleBG: 0xd0d4dc,
		microui.ColorTitleText: 0x101010, microui.ColorButton: 0xdcdcdc,
		microui.ColorButtonHover: 0xcacaca, microui.ColorButtonFocus: 0xb8b8b8,
		microui.ColorBase: 0xffffff, microui.ColorBaseHover: 0xf0f0f0,
		microui.ColorBaseFocus: 0xe6e6e6, microui.ColorScrollBase: 0xe0e0e0,
		microui.ColorScrollThumb: 0xa8a8a8, microui.ColorSelection: 0x8fb8f0,
	})
	solar := set(base, map[microui.ColorID]uint32{
		microui.ColorText: 0x93a1a1, microui.ColorBorder: 0x073642,
		microui.ColorWindowBG: 0x002b36, microui.ColorTitleBG: 0x073642,
		microui.ColorTitleText: 0xeee8d5, microui.ColorButton: 0x0a4a5a,
		microui.ColorButtonHover: 0x10606f, microui.ColorButtonFocus: 0x167585,
		microui.ColorBase: 0x01323d, microui.ColorBaseHover: 0x053c48,
		microui.ColorBaseFocus: 0x094652, microui.ColorScrollBase: 0x073642,
		microui.ColorScrollThumb: 0x586e75, microui.ColorSelection: 0x268bd2,
	})
	return []Theme{
		{Name: "Dark", Style: base, BG: microui.Hex(0x282c34)},
		{Name: "Light", Style: light, BG: microui.Hex(0xc8ccd4)},
		{Name: "Solarized", Style: solar, BG: microui.Hex(0x001e26)},
	}
}

// flatFrame draws control frames as plain fills: no borders.
func flatFrame(c *microui.Context, r microui.Rect, id microui.ColorID) {
	c.DrawRect(r, c.Style.Colors[id])
}

// ---- the gallery app --------------------------------------------------------

// Size is a small enum, to show RadioGroup with a custom comparable type.
type Size int

const (
	Small Size = iota
	Medium
	Large
)

// App is the gallery's persistent state.
type App struct {
	bg *microui.Color // the driver's backdrop (e.g. &term.BG)

	themes    []Theme
	theme     int
	flat      bool
	origFrame func(*microui.Context, microui.Rect, microui.ColorID)

	tab       int
	wifi, bt  bool
	size      Size
	fruit     string
	priority  int
	progress  float64
	autoAdv   bool
	log       []string
	logAdded  bool
	lastFrame time.Time
}

// maxLog bounds the event log so it cannot grow without limit.
const maxLog = 100

// NewApp returns the gallery drawing its backdrop into bg.
func NewApp(bg *microui.Color) *App {
	return &App{bg: bg, wifi: true, size: Medium, fruit: "Apple", priority: 2, autoAdv: true}
}

// setup runs once before the first frame (via renderer.WithSetup): it saves
// the default DrawFrame so the flat toggle can restore it, and builds the
// themes from the driver's style so their metrics fit the renderer.
func (a *App) setup(ctx *microui.Context) {
	a.origFrame = ctx.DrawFrame
	a.themes = makeThemes(*ctx.Style)
	a.applyTheme(ctx)
}

func (a *App) applyTheme(ctx *microui.Context) {
	t := a.themes[a.theme]
	*ctx.Style = t.Style
	*a.bg = t.BG
}

func (a *App) logf(format string, args ...any) {
	a.log = append(a.log, fmt.Sprintf(format, args...))
	if over := len(a.log) - maxLog; over > 0 {
		a.log = append(a.log[:0], a.log[over:]...)
	}
	a.logAdded = true
}

func (a *App) ui(ctx *microui.Context) {
	// The two windows tile an 80x24 terminal side by side.
	if ctx.BeginWindowEx("Widgets", microui.Rect{X: 1, Y: 1, W: 46, H: 22}, microui.OptNoClose).Active() {
		if Tabs(ctx, &a.tab, "Inputs", "Feedback", "Theme").Changed() {
			a.logf("tab -> %d", a.tab)
		}
		switch a.tab {
		case 0:
			a.inputsTab(ctx)
		case 1:
			a.feedbackTab(ctx)
		case 2:
			a.themeTab(ctx)
		}
		if a.tab != 1 {
			a.lastFrame = time.Time{} // progress only advances while it is shown
		}
		ctx.EndWindow()
	}
	a.logWindow(ctx)
}

var (
	fruits     = []string{"Apple", "Banana", "Cherry", "Durian"}
	priorities = []int{1, 2, 3}
	sizes      = []Size{Small, Medium, Large}
	themeIdx   = []int{0, 1, 2} // indexes into App.themes
	swatches   = []microui.Color{
		microui.Hex(0x3c5a82), microui.Hex(0x2e9e5b), microui.Hex(0xc9a227),
		microui.Hex(0xd64545), microui.Hex(0x8e5bd6),
	}
)

func (a *App) inputsTab(ctx *microui.Context) {
	// The dropdowns come first: their popups open downwards and must stay
	// on screen.
	ctx.LayoutRow([]int{10, 20}, 1)
	ctx.Label("Fruit:")
	if Select(ctx, "fruit", &a.fruit, fruits, func(s string) string { return s }).Changed() {
		a.logf("picked %s", a.fruit)
	}
	ctx.Label("Priority:")
	if Select(ctx, "priority", &a.priority, priorities, func(p int) string { return fmt.Sprintf("P%d", p) }).Changed() {
		a.logf("priority -> P%d", a.priority)
	}

	ctx.LayoutRow([]int{-1}, 1)
	if Toggle(ctx, "Wi-Fi", &a.wifi).Changed() {
		a.logf("toggled Wi-Fi: %v", a.wifi)
	}
	if Toggle(ctx, "Bluetooth", &a.bt).Changed() {
		a.logf("toggled Bluetooth: %v", a.bt)
	}

	ctx.Label("Size:")
	if RadioGroup(ctx, &a.size, sizes, []string{"Small", "Medium", "Large"}).Changed() {
		a.logf("size -> %d", a.size)
	}
}

func (a *App) feedbackTab(ctx *microui.Context) {
	// advance by wall-clock time so the speed is frame-rate independent
	now := time.Now()
	if a.autoAdv && !a.lastFrame.IsZero() {
		a.progress += now.Sub(a.lastFrame).Seconds() / 8
		if a.progress > 1 {
			a.progress = 0
			a.logf("progress wrapped")
		}
	}
	a.lastFrame = now

	ctx.LayoutRow([]int{-1}, 1)
	ProgressBar(ctx, a.progress)
	if Toggle(ctx, "Auto-advance", &a.autoAdv).Changed() {
		a.logf("auto-advance: %v", a.autoAdv)
	}
	ctx.LayoutRow([]int{10, -1}, 1)
	ctx.Label("Progress:")
	ctx.Slider(&a.progress, 0.0, 1.0) // generic Slider bound to a float64
	ctx.LayoutRow([]int{-1}, 1)
	if a.autoAdv {
		Spinner(ctx, "working…")
	} else {
		ctx.Label("  idle")
	}
}

func (a *App) themeTab(ctx *microui.Context) {
	ctx.LayoutRow([]int{10, 20}, 1)
	ctx.Label("Theme:")
	names := func(i int) string { return a.themes[i].Name }
	if Select(ctx, "theme", &a.theme, themeIdx, names).Changed() {
		a.applyTheme(ctx) // DrawFrame is not part of Style, so "flat" survives
		a.logf("theme -> %s", a.themes[a.theme].Name)
	}

	ctx.LayoutRow([]int{-1}, 1)
	if Toggle(ctx, "Flat (no borders)", &a.flat).Changed() {
		if a.flat {
			ctx.DrawFrame = flatFrame
		} else {
			ctx.DrawFrame = a.origFrame // restore the saved default
		}
		a.logf("flat: %v", a.flat)
	}

	ctx.Label("Accent:")
	ctx.LayoutRow([]int{12, 12, 12}, 1) // the row repeats: 5 swatches wrap onto 2 rows
	for _, c := range swatches {
		if ColorSwatch(ctx, c).Submitted() {
			ctx.Style.Colors[microui.ColorSelection] = c
			a.logf("accent -> #%02x%02x%02x", c.R, c.G, c.B)
		}
	}
}

func (a *App) logWindow(ctx *microui.Context) {
	if !ctx.BeginWindowEx("Log", microui.Rect{X: 48, Y: 1, W: 31, H: 22}, microui.OptNoClose).Active() {
		return
	}
	defer ctx.EndWindow()
	ctx.LayoutRow([]int{-1}, -1)
	ctx.BeginPanel("log")
	panel := ctx.GetCurrentContainer()
	ctx.LayoutRow([]int{-1}, 1)
	for _, l := range a.log {
		ctx.Label(l)
	}
	ctx.EndPanel()
	if a.logAdded {
		panel.Scroll.Y = panel.ContentSize.Y
		a.logAdded = false
	}
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "widgets:", err)
		os.Exit(1)
	}
	app := NewApp(&term.BG)
	if err := renderer.Run(term, app.ui, renderer.WithSetup(app.setup)); err != nil {
		fmt.Fprintln(os.Stderr, "widgets:", err)
		os.Exit(1)
	}
}
