# miniui

A *tiny*, portable, immediate-mode UI library for Go — a pure-Go (no cgo) port
of [rxi/microui](https://github.com/rxi/microui).

```
import "ella.to/microui"
```

* **Pure Go, no cgo, no dependencies** — just the standard library.
* **Renderer-agnostic** — miniui never draws anything itself. Each frame it
  produces a list of draw commands (filled rectangles, text, icons, clip
  regions) that you render with any backend: a GPU, a software canvas, or even a
  [terminal](#renderers).
* **Immediate mode** — there is no widget tree to keep in sync. You re-describe
  the whole UI every frame with plain `if`/`for` statements; the library tracks
  just enough retained state (focus, hover, window positions, scroll) between
  frames for you.
* **Built-in controls:** window, scrollable panel, popup, button, checkbox,
  slider, number, textbox, label, word-wrapped text, collapsible header and
  tree node — plus the primitives to build your own.

---

## Install

```sh
go get ella.to/microui
```

Requires Go 1.23+ (the command iterator uses `iter.Seq`).

---

## How it works

Every frame is three steps:

1. **Feed input** — tell miniui where the mouse is, what was clicked, what was
   typed.
2. **Build the UI** — between `Begin()` and `End()`, call control functions.
   Their return values tell you what the user did *this frame*.
3. **Render** — walk the command list miniui produced and draw it with your
   backend.

Before the first frame you must set two callbacks so the layout engine knows how
big text will be in your renderer's units (pixels, cells, whatever you like).

### Minimal complete example

```go
package main

import "ella.to/microui"

// State lives outside the frame loop so it persists across frames.
var (
	name    string
	volume  float32 = 0.5
	enabled bool
)

func main() {
	ctx := miniui.New()
	ctx.TextWidth = func(_ miniui.Font, s string) int { return len(s) * 7 } // px
	ctx.TextHeight = func(_ miniui.Font) int { return 14 }

	for { // your platform's main loop
		feedInput(ctx) // see "Feeding input" below

		ctx.Begin()
		if ctx.BeginWindow("Demo", miniui.Rect{X: 10, Y: 10, W: 240, H: 160}) != 0 {
			ctx.LayoutRow([]int{60, -1}, 0) // label column + a field filling the rest
			ctx.Label("Name:")
			ctx.Textbox(&name)

			ctx.LayoutRow([]int{-1}, 0) // one full-width cell per row
			ctx.Checkbox("Enabled", &enabled)
			ctx.Slider(&volume, 0, 1)
			if ctx.Button("Save") != 0 {
				save(name, volume, enabled)
			}
			ctx.EndWindow()
		}
		ctx.End()

		render(ctx) // see "Rendering" below
	}
}
```

### Rendering

`ctx.Commands()` is an iterator that yields the frame's draw commands in correct
back-to-front order (overlapping windows are interleaved for you):

```go
func render(ctx *miniui.Context) {
	for cmd := range ctx.Commands() {
		switch cmd.Type {
		case miniui.CommandRect:
			fillRect(cmd.Rect, cmd.Color) // your backend
		case miniui.CommandText:
			drawText(cmd.Str, cmd.Pos, cmd.Color, cmd.Font)
		case miniui.CommandIcon:
			drawIcon(cmd.Icon, cmd.Rect, cmd.Color) // IconClose/Check/Collapsed/Expanded
		case miniui.CommandClip:
			setClipRect(cmd.Rect) // clip following draws to this rectangle
		}
	}
}
```

That's the entire integration surface: implement `fillRect`, `drawText`,
`drawIcon` and `setClipRect` for your platform and every control just works.

You rarely have to write that loop by hand: the [`pkg/renderer`](pkg/renderer)
package wraps it behind a small swappable interface and ships a ready-made
terminal backend, so the same UI code runs on any engine. See
[Renderers](#renderers).

---

## Controls

Each control returns an `int` bitmask of result flags (see
[Result flags](#result-flags)). The smallest useful snippet for each:

### Button

```go
if ctx.Button("Save") != 0 { // non-zero == clicked this frame
	save()
}
```

### Label and wrapped text

```go
ctx.Label("Status:")                       // one line, left-aligned in its cell
ctx.Text("A longer paragraph that is " +   // word-wrapped to the column width,
	"automatically wrapped across lines.") // spanning as many rows as needed
```

### Checkbox

```go
var enabled bool // must persist across frames

if ctx.Checkbox("Enabled", &enabled)&miniui.ResChange != 0 {
	// value just toggled
}
```

### Slider and number

```go
var gain float32 = 1.0 // persists across frames

ctx.Slider(&gain, 0, 10)  // drag between 0 and 10
ctx.Number(&gain, 0.1)    // drag to nudge by 0.1 per unit of mouse movement

// With a step, value format and alignment:
ctx.SliderEx(&gain, 0, 10, 0.5, "%.1f", miniui.OptAlignCenter)
```

Tip: shift-click a slider or number to type an exact value.

### Textbox

```go
var query string // persists across frames

res := ctx.Textbox(&query)
if res&miniui.ResChange != 0 { /* text changed */ }
if res&miniui.ResSubmit != 0 { /* user pressed Enter */ }
```

A focused textbox supports full caret editing when the backend reports the
editing keys: arrows move the caret (Shift extends the selection, Ctrl jumps
to the ends), Home/End and Delete work, `KeySelectAll` selects everything,
clicking places the caret, dragging selects, and double-clicking selects a
word. Long values scroll horizontally to keep the caret visible. The terminal
backend reports all of these (select-all is Ctrl-A).

For password entry, mask the value while editing it normally:

```go
res := ctx.TextboxPassword(&secret)      // shows one '*' per rune
// equivalent: ctx.TextboxEx(&secret, miniui.OptPassword)
```

The mask rune is `miniui.PasswordMask` (default `'*'`); the real value never
reaches the draw commands.

### Header (collapsible section)

```go
if ctx.Header("Advanced") != 0 { // non-zero while expanded
	ctx.Label("only shown when the header is open")
}

// Start expanded:
if ctx.HeaderEx("Advanced", miniui.OptExpanded) != 0 { /* ... */ }
```

### Tree node

`Begin*`/`End*` controls must be balanced — only call `End*` when the matching
`Begin*` returned non-zero:

```go
if ctx.BeginTreenode("Root") != 0 {
	ctx.Label("child")
	if ctx.BeginTreenode("Nested") != 0 {
		ctx.Label("deeper")
		ctx.EndTreenode()
	}
	ctx.EndTreenode()
}
```

### Window

```go
if ctx.BeginWindow("Settings", miniui.Rect{X: 20, Y: 20, W: 300, H: 200}) != 0 {
	ctx.Label("Hello")
	ctx.EndWindow() // only when BeginWindow returned non-zero
}
```

The `Rect` is the window's *initial* position and size; after that the user can
drag the title bar to move it and the bottom-right corner to resize it, and
miniui remembers the new geometry. Options compose with `|`:

```go
opt := miniui.OptNoTitle | miniui.OptNoResize
ctx.BeginWindowEx("HUD", rect, opt)
```

### Panel (scrollable region)

A panel is a scrollable sub-region inside a window. Give it a layout cell first
(here a full-width, full-remaining-height cell):

```go
ctx.LayoutRow([]int{-1}, -1)
ctx.BeginPanel("list")
for i := 0; i < 200; i++ {
	ctx.LayoutRow([]int{-1}, 0)
	ctx.Label(fmt.Sprintf("row %d", i))
}
ctx.EndPanel()
```

### Popup / context menu

```go
if ctx.Button("Menu") != 0 {
	ctx.OpenPopup("ctx-menu") // open it at the mouse on click
}
if ctx.BeginPopup("ctx-menu") != 0 { // closes itself when you click elsewhere
	if ctx.Button("Copy") != 0 { copy() }
	if ctx.Button("Paste") != 0 { paste() }
	ctx.EndPopup()
}
```

---

## Layout

Controls are placed by a simple row system. `LayoutRow(widths, height)` starts a
new row; each control then consumes the next cell.

```go
// A label of fixed width 80, and a field that fills the rest of the row.
ctx.LayoutRow([]int{80, -1}, 0)
ctx.Label("Name:")
ctx.Textbox(&name)
```

**Width** values (per cell):

| value  | meaning                                             |
|--------|-----------------------------------------------------|
| `> 0`  | exactly that many units wide                        |
| `0`    | the style's default control width (`Style.Size.X`)  |
| `< 0`  | extend to that many units from the right edge (`-1` fills to the edge) |

**Height** (the second argument) works the same way: `0` = default control
height, `> 0` = exact, `< 0` = fill to that many units from the bottom.

```go
// Three buttons, 60 wide each, 25 tall.
ctx.LayoutRow([]int{60, 60, 60}, 25)
ctx.Button("A"); ctx.Button("B"); ctx.Button("C")

// One control filling the entire remaining width.
ctx.LayoutRow([]int{-1}, 0)
ctx.Button("Wide")

// A single column where every control stacks full-width (the default if you
// never call LayoutRow).
ctx.LayoutRow([]int{-1}, 0)
ctx.Label("line 1")
ctx.Label("line 2")
```

**Columns** let two areas flow independently side by side:

```go
ctx.LayoutRow([]int{140, -1}, 0)

ctx.LayoutBeginColumn()
ctx.Label("left line 1")
ctx.Label("left line 2")
ctx.LayoutEndColumn()

ctx.LayoutBeginColumn()
ctx.Text("the right column wraps and flows on its own")
ctx.LayoutEndColumn()
```

**Manual placement** — grab the next cell's rectangle and draw into it yourself:

```go
r := ctx.LayoutNext()
ctx.DrawRect(r, miniui.RGBA(200, 60, 60, 255))
```

---

## State & control identity

This is the one rule that trips people up, so it is worth understanding.

miniui identifies a control by **the memory address of the variable you pass to
it** (for value controls) or **its label** (for buttons, headers, windows).
Two consequences:

**1. Keep your state alive across frames.** A checkbox bound to `&enabled` only
remembers its focus/hover correctly if `enabled` is the *same* variable every
frame. Declare state as package vars, struct fields, or variables outside your
render loop — never as fresh locals inside it.

```go
// GOOD: same address every frame
type App struct{ enabled bool }
func (a *App) ui(ctx *miniui.Context) { ctx.Checkbox("On", &a.enabled) }

// BAD: a new variable each frame — focus/hover will misbehave
func ui(ctx *miniui.Context) {
	var enabled bool // re-created every call
	ctx.Checkbox("On", &enabled)
}
```

**2. Disambiguate repeated controls.** In a loop, many buttons may share the
same label ("Delete"), which means the same id and broken focus. Scope each
iteration with `PushID`/`PopID`:

```go
for i := range rows {
	ctx.PushID([]byte(strconv.Itoa(i))) // unique scope per row
	ctx.Label(rows[i].Name)
	if ctx.Button("Delete") != 0 {
		deleteRow(i)
	}
	ctx.PopID()
}
```

When binding to slice elements, take the element's address, not the loop copy:

```go
for i := range items {
	ctx.Checkbox(items[i].Name, &items[i].Done) // not &item from range
}
```

---

## Result flags

Control functions return a bitmask. Test the bits you care about:

| flag                 | meaning                                                       |
|----------------------|---------------------------------------------------------------|
| `miniui.ResSubmit`   | activated — button clicked, Enter pressed in a textbox        |
| `miniui.ResChange`   | value changed this frame — checkbox, slider, number, textbox  |
| `miniui.ResActive`   | container/section is open — window open, header/tree expanded |

```go
switch res := ctx.Textbox(&query); {
case res&miniui.ResSubmit != 0:
	runSearch(query)
case res&miniui.ResChange != 0:
	updateSuggestions(query)
}
```

`Button` only ever returns `ResSubmit` or `0`, so `if ctx.Button(...) != 0` is
the idiomatic check.

---

## Reading and adjusting containers

`GetCurrentContainer` returns the live state of the current window/panel, which
you can read and even modify mid-frame:

```go
if ctx.BeginWindow("W", rect) != 0 {
	win := ctx.GetCurrentContainer()
	win.Rect.W = max(win.Rect.W, 200) // enforce a minimum width
	_ = win.Scroll                    // current scroll offset (Vec2)
	_ = win.ContentSize               // measured content size (Vec2)
	ctx.EndWindow()
}
```

A common trick — auto-scroll a log panel to the bottom whenever new text is
appended:

```go
ctx.BeginPanel("log")
panel := ctx.GetCurrentContainer()
ctx.Text(logText)
ctx.EndPanel()
if appendedThisFrame {
	panel.Scroll.Y = panel.ContentSize.Y
}
```

---

## Styling

`Style` holds the colors and metrics. Copy the default and tweak it, then point
the context at it:

```go
st := miniui.DefaultStyle()
st.Colors[miniui.ColorButton] = miniui.RGBA(40, 90, 160, 255)
st.Colors[miniui.ColorButtonHover] = miniui.RGBA(55, 110, 190, 255)
st.Spacing = 8
st.Padding = 6
ctx.Style = &st
```

The color slots are `ColorText`, `ColorBorder`, `ColorWindowBG`, `ColorTitleBG`,
`ColorTitleText`, `ColorPanelBG`, `ColorButton`, `ColorButtonHover`,
`ColorButtonFocus`, `ColorBase`, `ColorBaseHover`, `ColorBaseFocus`,
`ColorScrollBase`, `ColorScrollThumb`. Useful metrics include `Size`,
`Padding`, `Spacing`, `Indent`, `TitleHeight`, `ScrollbarSize`, `ThumbSize`,
`MinWidth`/`MinHeight` (smallest a window can be resized to) and `ResizeHandle`
(size of the corner grab zone).

You can also replace how control frames are drawn entirely:

```go
ctx.DrawFrame = func(c *miniui.Context, r miniui.Rect, id miniui.ColorID) {
	c.DrawRect(r, c.Style.Colors[id]) // flat, borderless look
}
```

---

## Custom controls

The built-in controls are themselves built from a handful of public primitives:
`LayoutNext` (claim a cell), `UpdateControl` (run hover/focus logic), the
interaction accessors (`Focus`, `MousePressed`, `MouseDelta`, …) and the draw
helpers (`DrawControlFrame`, `DrawControlText`, `DrawRect`, `DrawIcon`). A
minimal clickable button:

```go
func myButton(ctx *miniui.Context, label string) bool {
	id := ctx.GetID([]byte(label))
	r := ctx.LayoutNext()
	ctx.UpdateControl(id, r, 0)

	clicked := ctx.MousePressed()&miniui.MouseLeft != 0 && ctx.Focus() == id

	ctx.DrawControlFrame(id, r, miniui.ColorButton, 0)
	ctx.DrawControlText(label, r, miniui.ColorText, miniui.OptAlignCenter)
	return clicked
}
```

Anything you can do with the built-ins, you can do here — drag handles
(`MouseDelta`), value editing (`KeyPressed`, `TextboxRaw`), custom hit areas
(`MouseOver`), and so on.

---

## Feeding input

Translate your platform's events into miniui input calls, before `Begin()`:

```go
ctx.InputMouseMove(x, y)
ctx.InputMouseDown(x, y, miniui.MouseLeft)  // or MouseRight / MouseMiddle
ctx.InputMouseUp(x, y, miniui.MouseLeft)
ctx.InputScroll(0, -3)                       // wheel; +y scrolls content down
ctx.InputText("a")                           // a typed character / string
ctx.InputKeyDown(miniui.KeyBackspace)        // KeyShift/Ctrl/Alt/Backspace/Return,
ctx.InputKeyUp(miniui.KeyBackspace)          // KeyLeft/Right/Home/End/Delete/SelectAll
```

For discrete key taps (Backspace, Enter), call `InputKeyDown` immediately
followed by `InputKeyUp` — miniui consumes "pressed this frame" at `End()`.

---

## Renderers

You can consume `ctx.Commands()` directly (see [Rendering](#rendering)), but the
[`pkg/renderer`](pkg/renderer) package wraps that loop behind a small interface
so the *same UI code* runs on any backend — a terminal, a GPU canvas, a web
canvas — and switching backends is switching one line.

A **`Renderer`** is the surface miniui paints onto. `renderer.Paint` walks a
frame's command list and turns each command into one method call:

```go
type Renderer interface {
	TextWidth(font miniui.Font, str string) int
	TextHeight(font miniui.Font) int
	Size() (w, h int)

	Clear(bg miniui.Color)    // begin a frame; reset the clip to the whole surface
	SetClip(rect miniui.Rect) // scissor rect for the following draws
	FillRect(rect miniui.Rect, color miniui.Color)
	DrawText(font miniui.Font, str string, pos miniui.Vec2, color miniui.Color)
	DrawIcon(id int, rect miniui.Rect, color miniui.Color)
	Present()                 // flush the finished frame to the display
}
```

A **`Driver`** is a `Renderer` that can also run a live loop — it supplies the
style and background, pumps input into the context, and tears itself down:

```go
type Driver interface {
	Renderer
	Style() miniui.Style
	Background() miniui.Color
	Poll(ctx *miniui.Context) (quit bool)
	Close() error
}
```

`renderer.Run` owns the frame loop. Your UI builder receives only a
`*miniui.Context`, so it is completely independent of which Driver is running:

```go
term, err := renderer.NewTerminal() // the default backend
if err != nil {
	log.Fatal(err)
}

renderer.Run(term, func(ctx *miniui.Context) {
	if ctx.BeginWindow("Hi", miniui.Rect{X: 1, Y: 1, W: 30, H: 8}) != 0 {
		ctx.Button("OK")
		ctx.EndWindow()
	}
})
```

**Swapping the engine** is swapping the Driver — `buildUI` is unchanged:

```go
drv := mygpu.New(window) // your own Driver implementation
renderer.Run(drv, buildUI)
```

If you only want the command-list dispatch (driving your own loop), implement
`Renderer`, call `renderer.Connect(ctx, r)` once to wire its
`TextWidth`/`TextHeight` into the context, then call `renderer.Paint(r, ctx, bg)`
after each `ctx.End()`.

The default backend is the terminal in
[`pkg/renderer/terminal.go`](pkg/renderer/terminal.go): each character cell is
one "pixel", text is the terminal font, input arrives via SGR mouse reporting
plus the keyboard, and raw mode is set with `stty(1)` — still no cgo and no
dependencies. It is a compact reference for writing your own backend.

---

## Examples

The [`examples/`](examples) directory has runnable programs. Each one's UI is a
plain `func(ctx *miniui.Context)` driven by the terminal backend, so the same UI
would run on any other `Driver` unchanged:

| example | what it shows |
|---------|---------------|
| [`boxdrag`](examples/boxdrag)     | a custom draggable box built from primitives |
| [`textinput`](examples/textinput) | a textbox echoing what you type |
| [`modal`](examples/modal)         | a popup/modal containing a text field |
| [`animation`](examples/animation) | per-frame hover animation — boxes grow on hover, shrink off |
| [`demo`](examples/demo)           | the full showcase: windows, headers, a tree, sliders that recolor the background live, and a scrolling log |
| [`hokm`](examples/hokm)           | a playable Hokm card game — click cards to play against three AIs, drawn as a full-screen canvas game |

```sh
go run ella.to/microui/examples/demo
go run ella.to/microui/examples/boxdrag
```

Drag title bars to move windows, drag the bottom-right corner to resize, use the
mouse wheel to scroll, click controls, type into text fields, and press
**Ctrl-C** to quit. When stdin/stdout is not a terminal each example prints a
single frame as plain text instead, which is handy for tests.

---

## Testing

```sh
go test ./...
```

---

## License

MIT, same as the upstream library. Original C microui © rxi. See [LICENSE](LICENSE).
