# The microui guide

A practical, end-to-end guide to building interactive UIs with
`ella.to/microui` and running them on any platform: a terminal, a browser
canvas, a GPU window, or a file on disk.

The [README](README.md) is the quick tour. This guide explains *how the pieces
fit*, shows the patterns real applications need, and walks through writing
your own renderer.

**Contents**

1. [The mental model](#1-the-mental-model)
2. [Your first app in 30 lines](#2-your-first-app-in-30-lines)
3. [The frame, step by step](#3-the-frame-step-by-step)
4. [Layout](#4-layout)
5. [Controls reference](#5-controls-reference)
6. [State and identity](#6-state-and-identity)
7. [Windows, panels, popups and modals](#7-windows-panels-popups-and-modals)
8. [Keyboard input and focus](#8-keyboard-input-and-focus)
9. [Building custom controls](#9-building-custom-controls)
10. [Styling and theming](#10-styling-and-theming)
11. [Animation and time](#11-animation-and-time)
12. [Goroutines and background work](#12-goroutines-and-background-work)
13. [Structuring an application](#13-structuring-an-application)
14. [Testing UIs](#14-testing-uis)
15. [Renderers: targeting any platform](#15-renderers-targeting-any-platform)
16. [Writing your own renderer](#16-writing-your-own-renderer)
17. [The terminal renderer in depth](#17-the-terminal-renderer-in-depth)
18. [Performance](#18-performance)
19. [Gotchas and FAQ](#19-gotchas-and-faq)
20. [Migrating from v0.1](#20-migrating-from-v01)
21. [Example index](#21-example-index)

---

## 1. The mental model

microui is an **immediate-mode** UI library. There is no widget tree to build,
keep in sync, or tear down. Instead, every frame you *describe the whole UI
from scratch* with ordinary Go code:

```go
if ctx.Button("Save").Submitted() {
	save()
}
```

That single line both **draws** the button and **asks** whether it was clicked
this frame. If you stop calling it, the button is gone. If you call it inside
an `if`, it appears conditionally. Loops make lists; functions make reusable
components. Your program's data *is* the UI state.

Three ideas carry the whole library:

| idea | what it means |
|------|---------------|
| **Frames** | Each frame: feed input → call controls between `Begin` and `End` → render the command list. A driver repeats this ~30 times a second. |
| **Commands** | microui never draws. A frame produces a flat list of `Rect`, `Text`, `Icon` and `Clip` commands. A *renderer* turns them into pixels, cells, SVG... |
| **IDs** | Hover, focus, window positions and scroll offsets must survive between frames. microui keys that small retained state by an ID hashed from a control's label or from the address of the variable it edits. |

The package splits along that boundary:

```
 your UI code  ──►  ella.to/microui            ──►  ella.to/microui/renderer
 func(ctx)          layout, controls, IDs,           Renderer/Driver interfaces,
                    command list (no drawing)        Run loop, Terminal driver
```

Your UI is just a `func(*microui.Context)`. It never knows which renderer is
running, which is what makes it portable.

---

## 2. Your first app in 30 lines

```go
package main

import (
	"fmt"
	"log"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

type app struct {
	name   string
	volume float64
	level  int
	muted  bool
	clicks int
}

func (a *app) ui(ctx *microui.Context) {
	if !ctx.BeginWindow("Hello", microui.Rect{X: 2, Y: 1, W: 44, H: 16}).Active() {
		return
	}
	defer ctx.EndWindow()

	// a label column, then a cell filling the rest; rows 1 cell tall (0 would
	// use the style's default control height, 3 cells in a terminal)
	ctx.LayoutRow([]int{9, -1}, 1)
	ctx.Label("Name:")
	ctx.Textbox(&a.name)
	ctx.Label("Volume:")
	ctx.Slider(&a.volume, 0, 1) // float64...
	ctx.Label("Level:")
	ctx.Slider(&a.level, 1, 10) // ...or int: Slider is generic

	ctx.LayoutRow([]int{-1}, 1)
	ctx.Checkbox("Muted", &a.muted)
	if ctx.Button("Click me").Submitted() {
		a.clicks++
	}
	ctx.Label(fmt.Sprintf("Hello %s, clicked %d times", a.name, a.clicks))
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		log.Fatal(err)
	}
	a := &app{name: "world", volume: 0.5, level: 3}
	if err := renderer.Run(term, a.ui); err != nil {
		log.Fatal(err)
	}
}
```

Run it with `go run .`. Drag the title bar to move the window, drag its
bottom-right corner to resize, click and type. Press **Ctrl-C** to quit.

> Requires **Go 1.27+**: `Slider`, `Number`, `GetID` and `PushID` are generic
> *methods*, which Go 1.27 introduced.

---

## 3. The frame, step by step

`renderer.Run` hides the loop, but knowing what it does makes everything else
obvious:

```go
for {
	if driver.Poll(ctx) {        // 1. feed platform input into ctx; quit?
		return
	}
	ctx.Begin()                  // 2. start a frame
	ui(ctx)                      // 3. your code: controls read input, emit commands
	ctx.End()                    // 4. resolve focus/scroll/z-order, finish the list
	renderer.Paint(driver, ctx, driver.Background()) // 5. commands → renderer → screen
	// 6. wait for the next tick, or wake early when input arrives
}
```

Things worth internalizing:

* **Input is sampled once per frame.** "Pressed" means *pressed since the last
  frame*. `ctx.KeyPressed()`, `ctx.MousePressed()` and `ctx.TextInput()` are
  cleared by `End`.
* **Results describe this frame.** `Button(...).Submitted()` is true for
  exactly one frame per click.
* **Hover lags one frame.** microui decides which window is under the mouse
  from the *previous* frame's layout. Real input always moves the mouse before
  clicking, so you never notice, except in tests (see
  [Testing](#14-testing-uis)).
* **Drawing order is z-order.** `End` sorts root windows back to front, so
  overlapping windows paint correctly no matter the order you built them in.
* **Begin/End pairs must balance.** `End` panics with a descriptive message if
  a window, panel, clip, ID or column was left open. That's deliberate: it
  catches bugs on the first frame.

### Results

Controls return a `microui.Result` bitmask with helpers:

| method | flag | when |
|--------|------|------|
| `.Active()` | `ResActive` | a window/popup is open, a header/tree node is expanded |
| `.Submitted()` | `ResSubmit` | a button was clicked, Enter pressed in a textbox |
| `.Changed()` | `ResChange` | a checkbox/slider/number/textbox changed its value this frame |

The raw bits still work (`res&microui.ResChange != 0`, `ctx.Button("x") != 0`).

---

## 4. Layout

Controls never take coordinates. They ask the layout for "the next cell".
Layout works in **rows**:

```go
ctx.LayoutRow([]int{80, -1}, 0) // two cells per row: 80 wide, then "the rest"
ctx.Label("Name:")              // cell 1
ctx.Textbox(&name)              // cell 2
ctx.Label("Email:")             // the row repeats: cell 1 again
ctx.Textbox(&email)
```

A row's width list repeats until you call `LayoutRow` again.

**Widths** (per cell) and **height** (for the row):

| value | meaning |
|-------|---------|
| `> 0` | exactly that many units |
| `0` | the style default (`Style.Size` + padding) |
| `< 0` | stretch to that many units *from the right/bottom edge*; `-1` = fill |

```go
ctx.LayoutRow([]int{-1}, -1)      // one cell filling all remaining width and height
ctx.LayoutRow([]int{-12, -1}, 0)  // a field that leaves 11 units for a button
ctx.LayoutRow([]int{10, 10, 10}, 3)
```

Units are whatever your renderer measures in: **pixels** for a canvas,
**character cells** for the terminal. In the terminal the default control is
3 rows tall (1 text row + 1 padding above and below). A row height of 1 gives
compact single-line rows.

### Columns

`LayoutBeginColumn` turns the next cell into its own vertical flow. Use it to
put a stack of controls side by side with something else:

```go
ctx.LayoutRow([]int{20, -1}, -1)
ctx.LayoutBeginColumn()
for _, item := range menu {
	ctx.LayoutRow([]int{-1}, 0)
	ctx.Button(item)
}
ctx.LayoutEndColumn()

ctx.LayoutBeginColumn()
ctx.Text(details) // wraps within the right column
ctx.LayoutEndColumn()
```

### Manual placement

* `r := ctx.LayoutNext()` claims the next cell and returns its rectangle. Draw
  into it yourself, or hand it to a custom control.
* `ctx.LayoutSetNext(r, relative)` forces the next cell's rectangle (relative
  to the container body, or absolute).
* `ctx.LastRect` is the rectangle of the most recent control. Use it to place
  a tooltip or popup next to it.

### The full-screen canvas pattern

Games, dashboards and anything absolutely positioned use one chrome-less,
oversized window as a drawing surface and receive input through it:

```go
const canvas = microui.OptNoTitle | microui.OptNoResize | microui.OptNoScroll | microui.OptNoFrame
if ctx.BeginWindowEx("canvas", microui.Rect{W: 10000, H: 10000}, canvas).Active() {
	defer ctx.EndWindow()
	w, h := term.Size() // the renderer clips to the real surface
	ctx.DrawRect(microui.Rect{X: w/2 - 5, Y: h/2 - 2, W: 10, H: 4}, microui.Hex(0x4682b4))
}
```

See `examples/boxdrag`, `examples/animation` and `examples/hokm`.

---

## 5. Controls reference

| control | signature | result | notes |
|---------|-----------|--------|-------|
| Label | `Label(text)` | — | one line, clipped to its cell |
| Text | `Text(text)` | — | word-wraps, honours `\n`, spans as many rows as needed |
| Button | `Button(label)`, `ButtonEx(label, icon, opt)` | `Submitted` | label is the ID; `ButtonEx("", microui.IconClose, 0)` is an icon button |
| Checkbox | `Checkbox(label, *bool)` | `Changed` | ID = address of the bool |
| Textbox | `Textbox(*string)`, `TextboxPassword`, `TextboxEx(buf, opt)` | `Changed`, `Submitted` (Enter) | caret, selection, double-click word, horizontal scroll |
| Slider | `Slider(*T, lo, hi)`, `SliderEx(*T, lo, hi, step, fmt, opt)` | `Changed` | **any numeric T**; ints snap; shift-click to type a value |
| Number | `Number(*T, step)`, `NumberEx(*T, step, fmt, opt)` | `Changed` | drag horizontally to change; shift-click to type |
| Header | `Header(label)`, `HeaderEx(label, opt)` | `Active` while open | `OptExpanded` starts open |
| Tree node | `BeginTreenode(label)` … `EndTreenode()` | `Active` while open | indents children; call `End` only when Active |
| Window | `BeginWindow(title, rect)` … `EndWindow()` | `Active` while open | rect is the *initial* geometry |
| Panel | `BeginPanel(name)` … `EndPanel()` | — | scrollable region in the next cell; **always** call `EndPanel` |
| Popup | `OpenPopup(name)`, `BeginPopup(name)` … `EndPopup()` | `Active` while open | opens at the mouse; closes on outside click or `ClosePopup` |

A value format of `""` picks a default by type (`%d` for integers, `%.2f` for
floats):

```go
ctx.SliderEx(&percent, 0, 100, 5, "%d%%", microui.OptAlignCenter)
ctx.NumberEx(&height, 0.01, "%.2f m", microui.OptAlignCenter)
```

**Options** (`microui.Opt*`, combine with `|`): `AlignCenter`, `AlignRight`,
`NoInteract` (visible but inert), `NoFrame`, `NoResize`, `NoScroll`,
`NoClose`, `NoTitle`, `HoldFocus`, `AutoSize` (window fits its content),
`Popup`, `Closed` (window starts closed), `Expanded`, `Password`.

---

## 6. State and identity

This is the one concept that trips people up.

**State lives in your program, not in microui.** A checkbox doesn't own a
bool; you pass `&enabled` and it flips your variable. microui only remembers
interaction state (hover, focus, scroll, window geometry), keyed by ID.

### How IDs are made

* **Value controls** (`Checkbox`, `Textbox`, `Slider`, `Number`) use the
  **address** of the bound variable.
* **Labelled controls** (`Button`, `Header`, `BeginTreenode`, windows, panels,
  popups) use the **label**.
* Both are hashed together with the **ID stack**, which is how windows keep
  their controls separate and how you scope loops.

### Rule 1: keep state alive across frames

```go
// GOOD: fields of a long-lived struct have a stable address
type App struct{ enabled bool }
func (a *App) ui(ctx *microui.Context) { ctx.Checkbox("On", &a.enabled) }

// BAD: a fresh local each frame, so focus and dragging break
func ui(ctx *microui.Context) {
	var enabled bool
	ctx.Checkbox("On", &enabled)
}
```

When binding slice elements, use the element's address, `&items[i].Done`,
never the loop copy `&item.Done`. Beware that `append` may move a slice's
backing array, which changes every element's address. It's harmless for one
frame (a drag in progress is lost), but don't append to the slice you are
iterating.

### Rule 2: scope repeated labels with PushID

Ten "Delete" buttons have the same ID. Scope each iteration. `PushID` accepts
strings, byte slices and integers:

```go
for i := range rows {
	ctx.PushID(i) // or a stable key: ctx.PushID(rows[i].ID)
	ctx.Label(rows[i].Name)
	if ctx.Button("Delete").Submitted() {
		toDelete = i // mutate after the loop, see §13
	}
	ctx.PopID()
}
```

Prefer a **stable key** (a database ID, a file path) over the index when rows
can be reordered or deleted, so focus stays with the right row.
`examples/filebrowser` scopes each tree node by its full path, so two folders
both named `docs` never collide.

`GetID(key)` returns the ID for custom controls and follows the same rules:
`ctx.GetID("my-knob")`, `ctx.GetID(rowID)`.

### Labels that differ from IDs

The label doubles as the ID, so a button whose text changes ("Play" / "Pause")
gets a new ID each time it changes. That's usually fine. If you need a stable
ID behind a changing label, build the button yourself (see
[§9](#9-building-custom-controls)) with `GetID("play-toggle")`.

---

## 7. Windows, panels, popups and modals

### Windows

```go
if ctx.BeginWindowEx("Inspector", microui.Rect{X: 50, Y: 2, W: 30, H: 20}, microui.OptNoClose).Active() {
	// ...
	ctx.EndWindow()
}
```

The rect is used only the first time. Afterwards the user's moves and resizes
win. You can read and change the retained geometry:

```go
win := ctx.GetCurrentContainer()      // inside BeginWindow
win.Rect.W = max(win.Rect.W, 30)      // enforce a minimum width

ctx.GetContainer("Inspector").Rect = r // from outside, e.g. re-tile on resize
ctx.BringToFront(ctx.GetContainer("Inspector"))
```

`Container` exposes `Rect`, `Body` (the inner area), `ContentSize`, `Scroll`,
`ZIndex` and `Open`. Closing a window (its ✕ button) sets `Open = false`.
Reopen it with `ctx.GetContainer(title).Open = true`. Note that
`GetContainer` hashes the name in the *current* ID scope, so call it at the
same nesting level as the window itself, usually outside any other window.

### Panels: scrollable regions

```go
ctx.LayoutRow([]int{-1}, -4) // all remaining height except 3 rows for an input
ctx.BeginPanel("log")
panel := ctx.GetCurrentContainer()
ctx.LayoutRow([]int{-1}, 1)
for _, line := range lines {
	ctx.Label(line)
}
ctx.EndPanel()
```

**Auto-scroll, but only if the user is already at the bottom:**

```go
maxScroll := panel.ContentSize.Y + 2*ctx.Style.Padding - panel.Body.H
atBottom := panel.Scroll.Y >= maxScroll-1
// ...after appending lines this frame:
if appended && atBottom {
	panel.Scroll.Y = panel.ContentSize.Y // clamped to the max next frame
}
```

**Scroll a selected row into view** from keyboard navigation by adjusting
`panel.Scroll.Y` (see `examples/todo`).

### Popups and context menus

```go
if ctx.Button("Options ▾").Submitted() {
	ctx.OpenPopup("options") // opens at the mouse
}
if ctx.BeginPopup("options").Active() {
	ctx.LayoutRow([]int{16}, 0)
	if ctx.Button("Rename").Submitted() {
		rename()
		ctx.ClosePopup("options") // safe to call inside the popup
	}
	ctx.EndPopup()
}
```

Call `OpenPopup` and `BeginPopup` **in the same scope** (the same enclosing
window), because the name is hashed under the current ID stack. A popup
auto-closes when the user clicks outside it. To place it somewhere other than
the mouse, set its rect right after opening:

```go
ctx.OpenPopup("menu")
ctx.GetContainer("menu").Rect = microui.Rect{X: ctx.LastRect.X, Y: ctx.LastRect.Y + ctx.LastRect.H, W: 1, H: 1}
```

### Modals

A modal blocks the UI behind it. Build it from a full-screen, chrome-less
window drawn on top. Because the mouse is always "inside" it, controls behind
it can't be hovered or clicked:

```go
if m.open {
	const opt = microui.OptNoTitle | microui.OptNoResize | microui.OptNoScroll | microui.OptNoFrame
	w, h := term.Size()
	screen := microui.Rect{W: w, H: h}
	if ctx.BeginWindowEx("modal", screen, opt).Active() {
		win := ctx.GetCurrentContainer()
		win.Rect = screen          // follow terminal resizes
		ctx.BringToFront(win)      // stay above everything
		ctx.DrawRect(screen, microui.Hex(0x000000).WithAlpha(160)) // dim what's behind

		box := microui.Rect{X: w/2 - 20, Y: h/2 - 5, W: 40, H: 9}
		ctx.DrawRect(box, ctx.Style.Colors[microui.ColorWindowBG])
		ctx.LayoutSetNext(box, false) // absolute placement for the panel
		ctx.BeginPanelEx("dialog", microui.OptNoScroll)
		ctx.LayoutRow([]int{-1}, 0)
		ctx.Label("Saved!")
		if ctx.Button("OK").Submitted() || ctx.KeyPressed()&microui.KeyEscape != 0 {
			m.open = false
		}
		ctx.EndPanel()
		ctx.EndWindow()
	}
}
```

The terminal renderer alpha-blends the translucent backdrop over the UI
beneath. `examples/form` has the complete version.

---

## 8. Keyboard input and focus

### Keys

`ctx.KeyPressed()` returns the keys pressed this frame and `ctx.KeyDown()` the
keys held. Both are `microui.Key` bitmasks:

| keys | used by |
|------|---------|
| `KeyBackspace`, `KeyDelete`, `KeyReturn`, `KeyLeft`, `KeyRight`, `KeyHome`, `KeyEnd`, `KeySelectAll` | the textbox |
| `KeyShift`, `KeyCtrl`, `KeyAlt` | modifiers (selection, word jumps, shift-click) |
| `KeyUp`, `KeyDown`, `KeyTab`, `KeyEscape`, `KeyPageUp`, `KeyPageDown` | your app: navigation, dismissal |

`ctx.TextInput()` is the text typed this frame. The focused textbox consumes
it, but custom controls can read it too.

### Focus

* `ctx.Focus()` is the focused control's ID (0 for none). Textboxes keep focus
  until Enter, a click elsewhere, or `SetFocus(0)`.
* `ctx.SetFocus(id)` moves focus. A typical use: keep typing after Enter.

```go
if ctx.Textbox(&draft).Submitted() {
	add(draft)
	draft = ""
	ctx.SetFocus(ctx.LastID()) // Enter cleared focus; take it back
}
```

**Global shortcuts only when nothing is being edited**, so typing "j" in a
textbox doesn't also move a selection:

```go
if ctx.Focus() == 0 {
	switch k := ctx.KeyPressed(); {
	case k&microui.KeyUp != 0:
		sel = max(sel-1, 0)
	case k&microui.KeyDown != 0:
		sel = min(sel+1, len(items)-1)
	case k&microui.KeyReturn != 0:
		open(items[sel])
	}
}
```

### Tab / Shift-Tab between fields

Collect field IDs as you build them, then move focus at the end of the frame:

```go
var fields []microui.ID
field := func(label string, buf *string) {
	ctx.Label(label)
	ctx.Textbox(buf)
	fields = append(fields, ctx.LastID())
}
field("Name:", &f.Name)
field("Email:", &f.Email)

if k := ctx.KeyPressed(); k&microui.KeyTab != 0 {
	i := slices.Index(fields, ctx.Focus())
	if ctx.KeyDown()&microui.KeyShift != 0 || k&microui.KeyShift != 0 {
		i-- // Shift-Tab
	} else {
		i++
	}
	ctx.SetFocus(fields[(i+len(fields))%len(fields)])
}
```

(The terminal reports Shift-Tab as `KeyTab` with `KeyShift` pressed in the
same frame, which is why both bitmasks are checked.)

---

## 9. Building custom controls

Every built-in control is built from public primitives, so yours can be
first-class. The anatomy:

```go
func Toggle(ctx *microui.Context, label string, on *bool) microui.Result {
	id := ctx.GetID(label)       // 1. identity
	r := ctx.LayoutNext()        // 2. claim a cell
	ctx.UpdateControl(id, r, 0)  // 3. hover/focus bookkeeping (call before reading them)

	var res microui.Result       // 4. interaction
	if ctx.MousePressed()&microui.MouseLeft != 0 && ctx.Focus() == id {
		*on = !*on
		res |= microui.ResChange
	}

	ctx.DrawControlFrame(id, r, microui.ColorButton, 0) // 5. draw (hover/focus tints)
	text := "[ OFF] " + label
	if *on {
		text = "[ ON ] " + label
	}
	ctx.DrawControlText(text, r, microui.ColorText, 0)
	return res
}
```

The building blocks:

| primitive | purpose |
|-----------|---------|
| `GetID(key)` | stable ID (string, []byte or integer; scoped by the ID stack) |
| `LayoutNext()` | the next cell's rect |
| `UpdateControl(id, rect, opt)` | sets hover when the mouse is over it, focus on press |
| `Hover()`, `Focus()`, `MouseOver(rect)` | interaction state |
| `MousePressed()`, `MouseDown()`, `MousePos()`, `MouseDelta()`, `ScrollDelta()` | mouse |
| `KeyPressed()`, `KeyDown()`, `TextInput()` | keyboard |
| `DrawControlFrame(id, rect, base, opt)` | background that tints on hover/focus (`ColorButton`, `ColorBase`) |
| `DrawControlText(str, rect, colorID, opt)` | aligned, clipped text |
| `DrawRect`, `DrawBox`, `DrawText`, `DrawIcon` | raw drawing (clipped to the current clip rect) |
| `PushClipRect` / `PopClipRect` | clip a region |

### Generic controls

Go 1.27 generic **methods** power `Slider`/`Number`. Your own components can
be generic functions. A radio group over any comparable type:

```go
func RadioGroup[T comparable](ctx *microui.Context, value *T, options []T, labels []string) microui.Result {
	var res microui.Result
	for i, opt := range options {
		ctx.PushID(i)
		mark := "( ) "
		if *value == opt {
			mark = "(•) "
		}
		if ctx.Button(mark + labels[i]).Submitted() && *value != opt {
			*value = opt
			res |= microui.ResChange
		}
		ctx.PopID()
	}
	return res
}

type Size int
const (Small Size = iota; Medium; Large)
RadioGroup(ctx, &size, []Size{Small, Medium, Large}, []string{"S", "M", "L"})
```

### Drawing-only widgets

A progress bar doesn't need an ID at all:

```go
func ProgressBar(ctx *microui.Context, frac float64) {
	r := ctx.LayoutNext()
	ctx.DrawRect(r, ctx.Style.Colors[microui.ColorBase])
	fill := r
	fill.W = int(float64(r.W) * min(max(frac, 0), 1))
	ctx.DrawRect(fill, microui.Hex(0x3c8c5a).Lerp(microui.Hex(0xc83c3c), frac))
	ctx.DrawControlText(fmt.Sprintf("%3.0f%%", frac*100), r, microui.ColorText, microui.OptAlignCenter)
}
```

### Dragging

Focus sticks while the button is held, even if the pointer leaves the
control, so drags don't drop when the mouse moves fast:

```go
id := ctx.GetID("knob")
ctx.UpdateControl(id, knobRect, 0)
if ctx.Focus() == id && ctx.MouseDown()&microui.MouseLeft != 0 {
	pos = pos.Add(ctx.MouseDelta())
}
```

`examples/widgets` contains copy-ready `Toggle`, `RadioGroup`, `Tabs`,
`Select` (a dropdown built on popups), `ProgressBar`, `Spinner` and
`ColorSwatch`.

---

## 10. Styling and theming

`ctx.Style` points at a `microui.Style`: metrics (`Size`, `Padding`,
`Spacing`, `Indent`, `TitleHeight`, `ScrollbarSize`, `ThumbSize`, `MinWidth`,
`MinHeight`, `ResizeHandle`) and colors indexed by `ColorID`.

```go
renderer.Run(term, ui, renderer.WithSetup(func(ctx *microui.Context) {
	ctx.Style.Colors[microui.ColorButton] = microui.Hex(0x2d5a9e)
	ctx.Style.Colors[microui.ColorButtonHover] = microui.Hex(0x3a6fbf)
	ctx.Style.Colors[microui.ColorButtonFocus] = microui.Hex(0x4a86e0)
}))
```

`WithSetup` runs once, after the driver installed its own style (the
terminal's is tuned for cells). Start from `term.Style()` or
`renderer.TerminalStyle()` rather than `microui.DefaultStyle()`, which uses
pixel metrics.

**Runtime theme switching**: keep several `Style` values and assign one:

```go
dark, light := term.Style(), term.Style()
light.Colors[microui.ColorWindowBG] = microui.Hex(0xeeeeee)
light.Colors[microui.ColorText] = microui.Hex(0x202020)
// ...
*ctx.Style = light // takes effect immediately, mid-frame even
term.BG = microui.Hex(0xd0d0d0)
```

**Replacing frame drawing** changes every control's look at once:

```go
orig := ctx.DrawFrame // keep it to restore later
ctx.DrawFrame = func(c *microui.Context, r microui.Rect, id microui.ColorID) {
	c.DrawRect(r, c.Style.Colors[id]) // flat: no borders
}
```

**Per-control color overrides** are a save/restore away:

```go
old := ctx.Style.Colors[microui.ColorText]
ctx.Style.Colors[microui.ColorText] = microui.Hex(0xe06c75)
ctx.Label("Invalid email")
ctx.Style.Colors[microui.ColorText] = old
```

Color helpers: `microui.Hex(0xRRGGBB)`, `RGBA(r, g, b, a)`,
`c.WithAlpha(a)`, `c.Lerp(other, t)`. Translucent colors (0 < A < 255) blend
on renderers that support it (the terminal and canvas renderers both do).

---

## 11. Animation and time

`renderer.Run` redraws at a steady rate (`renderer.WithFPS(n)`, default 30)
and also immediately on input. Animation is just state you advance each
frame. **Prefer wall-clock time over frame counts** so speed doesn't depend on
the frame rate:

```go
type fader struct {
	v    float64
	last time.Time
}

func (f *fader) step(target float64) float64 {
	now := time.Now()
	dt := now.Sub(f.last).Seconds()
	f.last = now
	f.v += (target - f.v) * min(dt*10, 1) // exponential ease, ~100ms time constant
	return f.v
}
```

Hover-driven animation (`examples/animation`) eases toward a target chosen by
`ctx.MouseOver(rect)`. Hit-test against the *largest* size the element can
take, so the target doesn't flicker as it grows under the cursor.

Timers (`examples/hokm` paces its AI players) are a `time.Time` you compare
against `time.Now()` each frame. No goroutine is needed.

---

## 12. Goroutines and background work

**A `microui.Context` is not safe for concurrent use, and neither is your UI
state.** Build every frame on one goroutine (the one calling `Run`). Do slow
work elsewhere and hand results back over a channel, drained *without
blocking* at the start of each frame:

```go
type app struct {
	samples chan Sample // written by the worker
	history []Sample    // owned by the UI goroutine
}

func (a *app) ui(ctx *microui.Context) {
	for drained := false; !drained; {
		select {
		case s := <-a.samples:
			a.history = append(a.history, s)
		default:
			drained = true
		}
	}
	// ... draw a.history ...
}
```

* Results reach the screen on the next tick. Lower latency comes from a
  higher `WithFPS`, or from a driver that implements `renderer.Waker` (the
  terminal wakes on input, not on your channel).
* Settings that flow the other way (UI → worker) can use `sync/atomic` values
  or another channel.
* Stop workers with a `context.Context` that you cancel when `Run` returns.

`examples/dashboard` is the complete pattern: a producer goroutine, ring
buffers, sparklines, gauges, and clean shutdown.

### Quitting from the UI

Pass a context and cancel it from a button:

```go
runCtx, cancel := context.WithCancel(context.Background())
defer cancel()
err := renderer.Run(term, func(ctx *microui.Context) {
	// ...
	if ctx.Button("Quit").Submitted() {
		cancel()
	}
}, renderer.WithContext(runCtx))
```

`Run` returns after the frame in which the context is done, restores the
terminal (even if your UI panics), and lets your deferred cleanup run.

---

## 13. Structuring an application

Patterns that keep immediate-mode code tidy as it grows:

**An app struct with methods.** State lives in fields (stable addresses),
each window is a method, `ui` composes them:

```go
type App struct {
	todos  []Todo
	draft  string
	filter int
}

func (a *App) ui(ctx *microui.Context) {
	a.listWindow(ctx)
	a.statsWindow(ctx)
}
```

**Mutate after iterating.** Deleting from a slice while ranging over it
skips elements and shifts addresses mid-frame. Record the intent and apply
it after the loop:

```go
del := -1
for i := range a.todos {
	ctx.PushID(i)
	if ctx.Button("x").Submitted() {
		del = i
	}
	ctx.PopID()
}
if del >= 0 {
	a.todos = slices.Delete(a.todos, del, del+1)
}
```

**Separate model from view.** Keep rules (validation, game logic) in plain
functions on your types and test them without any UI. `examples/hokm` tests
its card-game engine over hundreds of random hands, separately from drawing.

**Pass the driver only where needed.** Most UI code takes only
`*microui.Context`. Close over the driver (`term.Size()`, `term.BG`) in the
few places that need it, so the rest stays renderer-agnostic.

**Lay out from the surface size.** `w, h := term.Size()` each frame. To
re-tile windows on resize, write `GetContainer(name).Rect` when the size
changes (see `examples/dashboard`).

---

## 14. Testing UIs

Because a UI is a plain function and the terminal renderer has a headless
mode, UIs are easy to test in ordinary `go test`:

```go
func newHarness(w, h int) (*renderer.Terminal, *microui.Context) {
	term := renderer.NewHeadless(w, h)
	ctx := microui.New()
	renderer.Connect(ctx, term)
	st := term.Style()
	ctx.Style = &st
	return term, ctx
}

func frame(term *renderer.Terminal, ctx *microui.Context, ui func(*microui.Context)) {
	ctx.Begin()
	ui(ctx)
	ctx.End()
	renderer.Draw(term, ctx, term.BG) // paint without Present: nothing is printed
}
```

`term.Text()` returns the screen as plain text, ideal for assertions and
golden files:

```go
if !strings.Contains(term.Text(), "1 item left") { t.Fatal(term.Text()) }
```

**Simulating a click** takes three frames, because hover is decided from the
previous frame:

```go
ctx.InputMouseMove(x, y); frame(...)            // the window becomes the hover root
ctx.InputMouseMove(x, y); frame(...)            // the control becomes hovered
ctx.InputMouseDown(x, y, microui.MouseLeft); frame(...) // click
ctx.InputMouseUp(x, y, microui.MouseLeft); frame(...)
```

**Typing and keys:** `ctx.InputText("hello")` for text. For a key tap,
`ctx.InputKeyDown(k)` then `ctx.InputKeyUp(k)` before the frame. Both are
seen through `KeyPressed`.

`renderer.Draw` paints without presenting, so tests stay quiet; use
`renderer.Paint` when you do want the headless frame printed. For visual golden
files, render to SVG with `examples/svg`'s renderer.

The larger examples (`todo`, `form`, `widgets`, `dashboard`, `filebrowser`,
`hokm`, `svg`) ship tests in this style. They're the best reference.

---

## 15. Renderers: targeting any platform

```
            ┌────────────── renderer.Driver ───────────────┐
 ui(ctx) ─► │ Poll(ctx) → input   Style()   Background()   │
            │        ┌──────── renderer.Renderer ───────┐  │
 commands ─►│        │ TextWidth TextHeight Size        │  │
            │        │ Clear SetClip FillRect DrawText  │  │
            │        │ DrawIcon Present                 │  │
            │        └──────────────────────────────────┘  │
            │ Close()        (optional: Waker, Errer)      │
            └──────────────────────────────────────────────┘
```

* A **`Renderer`** is a drawing surface: measure text, fill rects, draw
  text/icons, clip, present. That's enough for static output
  (`examples/svg` writes SVG files).
* A **`Driver`** is a Renderer that also runs live: it supplies a style and
  background, pumps platform input in `Poll`, and cleans up in `Close`.
  `renderer.Run` drives any Driver.
* **Optional extensions:** `Waker` (a channel that wakes `Run` early on input)
  and `Errer` (why `Poll` quit).

What ships in this repository:

| backend | where | kind |
|---------|-------|------|
| Terminal (Unix + Windows, truecolor/256/16) | `renderer.NewTerminal` | Driver |
| Headless terminal (tests, pipes) | `renderer.NewHeadless` | Driver |
| HTML canvas via `syscall/js` | `examples/wasm` | Driver |
| SVG documents | `examples/svg` | Renderer |

Switching platforms means switching the Driver. `ui` stays the same:

```go
var d renderer.Driver
if browser {
	d = NewCanvas("screen") // your js/wasm driver
} else {
	d, _ = renderer.NewTerminal()
}
renderer.Run(d, app.ui)
```

You can also skip `Run` and drive frames yourself, e.g. from
`requestAnimationFrame` in a browser or from a game engine's update loop:

```go
renderer.Connect(ctx, r) // once: wire text metrics into the layout engine
// every frame:
feedInput(ctx)
ctx.Begin(); ui(ctx); ctx.End()
renderer.Paint(r, ctx, bg)
```

---

## 16. Writing your own renderer

Implementing a backend for SDL, Ebitengine, raylib, OpenGL/Vulkan, a
framebuffer, a PDF, or an e-ink display takes the same steps. Read
`examples/svg/main.go` (about 100 lines) for the smallest complete one and
`examples/wasm/canvas.go` for an interactive one.

### Step 1: pick your units and measure text

microui is unit-agnostic. Choose pixels (GUI), cells (terminal), points
(PDF), and make `TextWidth`, `TextHeight` and every coordinate agree.

```go
func (r *MyRenderer) TextWidth(font microui.Font, s string) int  { return r.font.Measure(s) }
func (r *MyRenderer) TextHeight(font microui.Font) int           { return r.font.LineHeight() }
```

`microui.Font` is an opaque `any` you can use to carry a font handle
(`ctx.Style.Font = myFont`). It comes back on every text command. **Cache
measurements.** Layout calls `TextWidth` constantly; the canvas driver keeps a
bounded `map[string]int`.

### Step 2: implement the drawing contract

| method | contract |
|--------|----------|
| `Clear(bg)` | Start a frame: fill everything with `bg`, reset the clip to the full surface. |
| `SetClip(rect)` | **Replace** the clip with `rect ∩ surface`. microui resets clipping by passing a huge rect (`0x1000000` wide). Don't nest; always replace. |
| `FillRect(rect, color)` | Fill. microui already clipped `rect`; still intersect with the surface. Blend `0 < A < 255` if you can (`A == 0` never reaches you via `Paint`). |
| `DrawText(font, str, pos, color)` | `pos` is the **top-left** of the text box (use a "top" baseline). Must honour the clip. |
| `DrawIcon(id, rect, color)` | Draw `IconClose`, `IconCheck`, `IconCollapsed`, `IconExpanded` centered in `rect`. Glyphs or tiny vector shapes both work. |
| `Present()` | Flush or swap. For immediate canvases it can be a no-op. |
| `Size()` | Drawable area in your units. |

### Step 3 (live backends): input and lifecycle

```go
func (d *MyDriver) Style() microui.Style      { return microui.DefaultStyle() } // pixel metrics
func (d *MyDriver) Background() microui.Color { return d.bg }

func (d *MyDriver) Poll(ctx *microui.Context) (quit bool) {
	for _, ev := range d.drainEvents() { // must not block
		switch ev.kind {
		case mouseMove:
			ctx.InputMouseMove(ev.x, ev.y)
		case mouseDown:
			ctx.InputMouseDown(ev.x, ev.y, microui.MouseLeft)
		case mouseUp:
			ctx.InputMouseUp(ev.x, ev.y, microui.MouseLeft)
		case wheel:
			ctx.InputScroll(0, ev.dy) // +y scrolls content down
		case text:
			ctx.InputText(ev.text)
		case key:
			ctx.InputKeyDown(mapKey(ev.key)) // see the key table in §8
			ctx.InputKeyUp(mapKey(ev.key))   // for taps; real releases call it later
		case closeRequested:
			return true
		}
	}
	return false
}
```

**Batch input per frame.** microui sees keys and buttons as per-frame
bitmasks, so two presses of Backspace in one frame collapse into one, and
"Backspace then x" gets applied as "x then Backspace". Robust drivers apply
**at most one discrete event (key, button, text run) per frame**, leave the
rest queued, and wake `Run` (via `Waker`) to process them on the next frame
right away. Motion and wheel can coalesce. Both the terminal and canvas
drivers do this. Copy their approach.

Hold modifiers with real down/up events if your platform reports them
(shift-click on a slider types an exact value). Otherwise synthesize them
around the key the way the terminal does.

### Step 4: run it

```go
d := NewMyDriver()
if err := renderer.Run(d, app.ui, renderer.WithFPS(60)); err != nil { ... }
```

### Sketch: a GPU / game-engine backend

Most 2D engines map one to one:

| microui | typical engine call |
|---------|---------------------|
| `Clear` | clear the framebuffer, reset the scissor |
| `SetClip` | set the scissor rect (flush the batch first) |
| `FillRect` | push a solid quad (alpha blending on) |
| `DrawText` | draw a glyph-atlas string at pos with a top-left origin |
| `DrawIcon` | draw a sprite from an icon atlas |
| `Present` | flush batches, swap buffers |

If the engine owns the main loop (`Update`/`Draw` callbacks), don't call
`renderer.Run`. Call `Poll`/`Begin`/`ui`/`End` in `Update` and `Paint` in
`Draw`.

---

## 17. The terminal renderer in depth

```go
term, err := renderer.NewTerminal(
	renderer.WithColorMode(renderer.ColorAuto), // or ColorTrue, Color256, Color16
	renderer.WithMouse(true),                   // false keeps native text selection
	renderer.WithSuspend(true),                 // Ctrl-Z suspends to the shell (Unix)
)
```

**Model.** One character cell is one unit. Text is the terminal's font; wide
runes (CJK, most emoji) take two cells and `TextWidth` accounts for that, so
alignment holds. `renderer.StringWidth(s)` gives you the same measure for
your own centering.

**Output.**
* Double-buffered: each frame writes **only the cells that changed**, so an
  idle UI writes nothing.
* The frame is built in one reused buffer and written with a single `write`,
  wrapped in a synchronized update (`CSI ? 2026`) so supporting terminals swap
  it in atomically: no tearing or flicker.
* Autowrap is disabled, the cursor is hidden, and the alternate screen keeps
  your scrollback intact.
* Translucent fills blend over the cells beneath, dimming their glyphs too
  (modal backdrops).

**Colors.** `ColorAuto` picks truecolor when `COLORTERM=truecolor|24bit`,
for known truecolor terminals (`TERM` of kitty, ghostty, wezterm, alacritty,
foot, `*-direct`; `TERM_PROGRAM` of iTerm, WezTerm, VS Code, ghostty;
Windows Terminal), 16 colors for `TERM=linux|vt100|ansi`, and 256 colors
otherwise. Force a mode with `WithColorMode` or by exporting
`COLORTERM=truecolor`. Inside tmux, enable truecolor with
`set -as terminal-features ",*:RGB"`.

**Input.**

| what | how it arrives |
|------|----------------|
| Mouse move, press/release (left/middle/right), wheel (vertical and horizontal) | SGR mouse reporting (`1003` + `1006`) |
| Shift/Alt/Ctrl + click | modifiers held in `KeyDown` for that frame |
| Arrows, Home/End, PgUp/PgDn, Delete, Tab, Shift-Tab, Enter, Backspace | CSI, SS3 and CSI-u encodings, with xterm modifiers (Shift/Ctrl+arrows) |
| Esc | reported after a 40 ms quiet period, so it's never confused with an escape sequence |
| Ctrl-A | `KeySelectAll` |
| Paste | bracketed paste, delivered as text (newlines become spaces) |
| Ctrl-C | quit · **Ctrl-L** full redraw · **Ctrl-Z** suspend (Unix) |

Input is fed one coherent batch per frame (see §16), so fast typing, held
Backspace and pastes are never lost or reordered.

**Lifecycle.** Resizes (SIGWINCH on Unix, polled on Windows) trigger a full
repaint. SIGINT, SIGTERM and SIGHUP make `Poll` report quit, so `Run` returns
normally and your defers run. `Close` restores everything (raw mode, mouse,
paste mode, cursor, autowrap, alternate screen), and `Run` calls it even if
your UI panics.

**Platforms.**
* *Unix* (macOS, Linux, BSDs): raw mode via `stty(1)`. No cgo.
* *Windows 10 1809+* / Windows Terminal: console VT modes via kernel32. No cgo.
* *Elsewhere* (js/wasm, wasip1, plan9): `NewTerminal` returns the headless
  renderer.

**Headless.** When stdin or stdout is not a terminal (piped, CI), or with
`renderer.NewHeadless(w, h)`, the terminal paints into memory, `Present`
prints one plain-text frame, and `Poll` quits after it. `term.Text()`
snapshots the surface any time.

**Troubleshooting.**

| symptom | fix |
|---------|-----|
| colors look banded | your terminal only got 256 colors: `export COLORTERM=truecolor` if it supports more |
| mouse doesn't work in tmux | `set -g mouse on` |
| can't select text to copy | hold Shift (most terminals), or `WithMouse(false)` |
| emoji misaligned | the terminal's own width table disagrees; prefer symbols from the narrow ranges, or plain text |
| screen garbled after another program printed | Ctrl-L, or `term.Redraw()` |

---

## 18. Performance

A typical frame (two windows, a few dozen controls) builds in single-digit
microseconds, and the terminal driver writes nothing when the screen didn't
change. To keep it that way:

* **Don't allocate per frame needlessly.** `fmt.Sprintf` in a hot list of
  10,000 rows adds up. Format once when data changes, or only for visible rows.
* **Virtualize long lists.** In a panel, compute the visible range from
  `panel.Scroll.Y` and the row height, emit only those rows, and reserve the
  rest with `LayoutRow`/`LayoutNext` spacers so the scrollbar stays right.
* **Cache text measurement** in your renderer (layout calls `TextWidth` a
  lot).
* **Prefer integer and string IDs.** `PushID(i)` hashes 8 bytes, with no
  `strconv` needed.
* **Pick a modest FPS.** Wake-on-input means `WithFPS(10)` still feels instant
  for form-like apps. Raise it for animation.

Benchmarks live in `bench_test.go` and `renderer/bench_test.go`:

```sh
go test -run '^$' -bench . -benchmem ./ ./renderer
```

---

## 19. Gotchas and FAQ

**My checkbox/slider ignores clicks or drags jitter.** The bound variable is
re-created each frame. Bind a struct field or package variable (§6).

**Two buttons react together.** Same label, same ID. Wrap each in
`PushID`/`PopID` (§6).

**`End` panics "unbalanced ... stack".** A `Begin*` without its `End*`, or an
`End*` called when `Begin*` wasn't `Active`. Panels are the exception: always
end them. Using `defer ctx.EndWindow()` right after a successful
`BeginWindow` avoids most of these.

**My popup never opens / never closes.** `OpenPopup` and `BeginPopup` must
run in the same ID scope (§7). Close it with `ClosePopup`.

**A window ignores the rect I pass.** The rect is only the initial geometry.
Set `GetContainer(title).Rect` to move it programmatically.

**Content behind my overlay is still clickable.** Make the overlay a window
(the mouse only interacts with the top-most window under it) and bring it to
the front each frame (§7 Modals).

**The UI only updates when I move the mouse.** Your driver isn't running at a
steady rate. With `renderer.Run`, frames tick at `WithFPS`. Background
results appear on the next tick (§12).

**How do I get the terminal size?** `term.Size()` every frame. Resizes are
picked up automatically.

**Can I use multiple Contexts?** Yes: one per independent surface (say, two
windows of a GUI app), each driven separately.

**Is it safe to call controls from another goroutine?** No (§12).

---

## 20. Migrating from v0.1

| v0.1 | now |
|------|-----|
| `import miniui "ella.to/microui"` | `import "ella.to/microui"`. The package is named `microui`; drop the alias. |
| `ella.to/microui/pkg/renderer` | `ella.to/microui/renderer` |
| Go 1.23 | **Go 1.27** (generic methods) |
| controls return `int` | return `microui.Result`; `!= 0` checks still compile; prefer `.Submitted()` / `.Changed()` / `.Active()` |
| `PushID([]byte(strconv.Itoa(i)))` | `PushID(i)` (string, `[]byte` and integers are all accepted) |
| `Slider(*float32, ...)` only | `Slider(*T, ...)` for any numeric `T` (`int`, `float64`, `uint8`...) |
| `InputMouseDown(x, y, int)` | typed `microui.MouseButton`; keys are `microui.Key`; icons are `microui.Icon` |
| `GetContainer(name).Open = false` inside a popup | `ClosePopup(name)` (the old form addressed the wrong container) |
| `renderer.Run(d, ui)` | unchanged, plus options: `WithFPS`, `WithContext`, `WithSetup` |
| `NewTerminal()` | unchanged, plus options: `WithColorMode`, `WithMouse`, `WithSuspend` |

Code that stores results in an `int` variable (`var r int = ctx.Button(..)`)
or passes `int` variables as mouse buttons or keys needs a type change. Typed
constants work as before.

---

## 21. Example index

```sh
go run ella.to/microui/examples/<name>
```

| example | what it teaches |
|---------|-----------------|
| [`textinput`](examples/textinput) | the smallest app: a textbox and a label |
| [`boxdrag`](examples/boxdrag) | a custom draggable control from primitives on a canvas window |
| [`animation`](examples/animation) | per-frame easing driven by hover |
| [`modal`](examples/modal) | a popup with a text field, closed with `ClosePopup` |
| [`demo`](examples/demo) | the classic microui showcase: windows, headers, tree, sliders, log |
| [`todo`](examples/todo) | list app: `PushID` rows, deferred deletion, custom tabs, keyboard navigation, quit via context |
| [`form`](examples/form) | validation, Tab/Shift-Tab focus cycling, generic int/float controls, a true modal with a dimmed backdrop |
| [`widgets`](examples/widgets) | copy-ready custom controls (Toggle, generic RadioGroup and Select, Tabs, ProgressBar, Spinner) and runtime theming |
| [`dashboard`](examples/dashboard) | goroutine → channel → UI pattern, sparklines, gauges, resize-aware tiling |
| [`filebrowser`](examples/filebrowser) | recursive tree nodes, lazy loading over `fs.FS`, path-scoped IDs, previews |
| [`hokm`](examples/hokm) | a complete card game on a canvas window, with a separately tested engine |
| [`svg`](examples/svg) | a 100-line non-interactive `Renderer` that snapshots a UI to SVG |
| [`wasm`](examples/wasm) | a browser `Driver` on `<canvas>` using only `syscall/js` |
