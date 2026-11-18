# microui

A *tiny*, portable, immediate-mode UI library for Go: a pure-Go (no cgo, no
dependencies) port of [rxi/microui](https://github.com/rxi/microui), with a
fast, robust terminal renderer and a renderer interface that targets any
platform.

```sh
go get ella.to/microui
```

Requires **Go 1.27+**: value controls and ID helpers are generic methods.

* **Immediate mode.** No widget tree to keep in sync. Describe the whole UI
  every frame with plain `if`/`for`; the library keeps just enough state
  (focus, hover, window geometry, scroll) between frames.
* **Renderer-agnostic.** microui never draws. Each frame produces a list of
  draw commands (rects, text, icons, clip regions) that any backend can
  paint: a terminal, a browser canvas, a GPU window, an SVG file.
* **Batteries included.** Windows, scrollable panels, popups, buttons,
  checkboxes, sliders and number fields over any numeric type, a textbox with
  full caret/selection editing, labels, word-wrapped text, headers and tree
  nodes, plus the primitives to build your own controls.
* **A solid terminal backend.** Diffed, flicker-free output; truecolor, 256 or
  16 colors; alpha blending; correct wide characters; mouse, keyboard and
  bracketed paste; resize, suspend and clean restore; Unix and Windows.

**→ Read the [GUIDE](GUIDE.md)** for the full walkthrough: layout, state and
IDs, custom controls, theming, modals, keyboard focus, goroutines, testing,
and writing your own renderer.

---

## Hello, terminal

```go
package main

import (
	"log"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

type app struct {
	name   string
	volume float64
	level  int
	muted  bool
}

func (a *app) ui(ctx *microui.Context) {
	if !ctx.BeginWindow("Hello", microui.Rect{X: 2, Y: 1, W: 44, H: 14}).Active() {
		return
	}
	defer ctx.EndWindow()

	ctx.LayoutRow([]int{9, -1}, 1) // label column + a cell filling the rest
	ctx.Label("Name:")
	ctx.Textbox(&a.name)
	ctx.Label("Volume:")
	ctx.Slider(&a.volume, 0, 1) // float64
	ctx.Label("Level:")
	ctx.Slider(&a.level, 1, 10) // int, the same generic method

	ctx.LayoutRow([]int{-1}, 1)
	ctx.Checkbox("Muted", &a.muted)
	if ctx.Button("Save").Submitted() {
		log.Printf("saved %+v", *a)
	}
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		log.Fatal(err)
	}
	if err := renderer.Run(term, (&app{name: "world"}).ui); err != nil {
		log.Fatal(err)
	}
}
```

Drag title bars to move windows, drag the bottom-right corner to resize, use
the wheel to scroll, and press **Ctrl-C** to quit.

---

## How it works

Every frame is three steps:

1. **Feed input:** where the mouse is, what was clicked, what was typed.
2. **Build the UI:** between `Begin()` and `End()`, call controls. Their
   `Result` tells you what happened *this frame*: `.Submitted()`,
   `.Changed()`, `.Active()`.
3. **Render:** walk the command list with any backend.

`renderer.Run` does all three for any `renderer.Driver`. Without it, the
whole integration surface is:

```go
ctx := microui.New()
ctx.TextWidth = func(f microui.Font, s string) int { return measure(f, s) } // your units
ctx.TextHeight = func(f microui.Font) int { return lineHeight(f) }

for running {
	feedInput(ctx) // ctx.InputMouseMove / InputMouseDown / InputText / InputKeyDown ...
	ctx.Begin()
	ui(ctx)
	ctx.End()
	for cmd := range ctx.Commands() { // back-to-front, overlapping windows interleaved
		switch cmd.Type {
		case microui.CommandRect: fillRect(cmd.Rect, cmd.Color)
		case microui.CommandText: drawText(cmd.Str, cmd.Pos, cmd.Color, cmd.Font)
		case microui.CommandIcon: drawIcon(cmd.Icon, cmd.Rect, cmd.Color)
		case microui.CommandClip: setClip(cmd.Rect)
		}
	}
}
```

## Controls at a glance

```go
ctx.Label("Status:")
ctx.Text("A paragraph that wraps to the column width.")
if ctx.Button("Save").Submitted() { save() }
if ctx.Checkbox("Enabled", &enabled).Changed() { apply() }
ctx.Slider(&gain, 0, 10)         // any numeric type; shift-click to type a value
ctx.Number(&count, 1)            // drag to change
if ctx.Textbox(&query).Submitted() { search(query) }
ctx.TextboxPassword(&secret)

if ctx.Header("Advanced").Active() { ... }
if ctx.BeginTreenode("Root").Active() { ...; ctx.EndTreenode() }

ctx.LayoutRow([]int{-1}, -1)
ctx.BeginPanel("list") // scrollable
for i, row := range rows {
	ctx.PushID(i) // scope repeated labels: strings, []byte or ints
	if ctx.Button("Delete").Submitted() { del = i }
	ctx.PopID()
}
ctx.EndPanel()

if ctx.Button("Menu").Submitted() { ctx.OpenPopup("menu") }
if ctx.BeginPopup("menu").Active() { ...; ctx.EndPopup() }
```

The one rule to remember: **value controls are identified by the address of
the variable you pass, and labelled controls by their label.** Keep state in
long-lived variables (struct fields), and wrap repeated labels in
`PushID`/`PopID`. See [GUIDE §6](GUIDE.md#6-state-and-identity).

## Renderers

The [`renderer`](renderer) package defines a small `Renderer` interface (measure
text, fill rects, draw text/icons, clip, present) and a `Driver` interface
(a Renderer that also polls input), and runs the frame loop:

```go
renderer.Run(driver, ui,
	renderer.WithFPS(30),             // steady redraw rate; drivers also wake on input
	renderer.WithContext(runCtx),     // cancel it to quit from the UI
	renderer.WithSetup(func(ctx *microui.Context) { /* tweak the style */ }),
)
```

| backend | where |
|---------|-------|
| Terminal: Unix and Windows, truecolor/256/16 colors | `renderer.NewTerminal(opts...)` |
| Headless terminal for tests and pipes (`term.Text()` snapshots) | `renderer.NewHeadless(w, h)` |
| Browser `<canvas>` via `syscall/js` | [`examples/wasm`](examples/wasm) |
| SVG files (a 100-line reference Renderer) | [`examples/svg`](examples/svg) |

Swapping platforms is swapping the Driver; the UI function is unchanged.
[GUIDE §16](GUIDE.md#16-writing-your-own-renderer) walks through writing one
for any engine (SDL, Ebitengine, raylib, OpenGL, a framebuffer...).

### The terminal renderer

* **Output:** double-buffered cell diffing, so an idle UI writes nothing; one
  write per frame inside a synchronized update (no tearing); autowrap off;
  alternate screen.
* **Color:** truecolor, 256 or 16 colors, auto-detected (`COLORTERM`,
  `TERM`, `TERM_PROGRAM`) or forced with `WithColorMode`; translucent colors
  alpha-blend.
* **Text:** wide CJK and emoji take two cells and layout accounts for it
  (`renderer.StringWidth`).
* **Input:** SGR mouse with wheel and modifiers; arrows, Home/End, PgUp/PgDn,
  Delete, Tab/Shift-Tab, Esc; bracketed paste. Events are batched per frame,
  so fast typing is never lost or reordered.
* **Lifecycle:** resize handling, Ctrl-L redraw, Ctrl-Z suspend (Unix),
  signal-safe shutdown, and a full terminal restore even if the UI panics.

## Examples

```sh
go run ella.to/microui/examples/<name>
```

| example | what it shows |
|---------|---------------|
| [`textinput`](examples/textinput) | a textbox echoing what you type |
| [`boxdrag`](examples/boxdrag) | a custom draggable control built from primitives |
| [`animation`](examples/animation) | per-frame hover animation |
| [`modal`](examples/modal) | a popup containing a text field |
| [`demo`](examples/demo) | the classic showcase: windows, headers, tree, sliders, log |
| [`todo`](examples/todo) | lists with `PushID`, deferred deletion, tabs, keyboard navigation |
| [`form`](examples/form) | validation, Tab focus cycling, a true modal with a dimmed backdrop |
| [`widgets`](examples/widgets) | copy-ready custom controls (Toggle, generic RadioGroup/Select, Tabs...) and theming |
| [`dashboard`](examples/dashboard) | goroutine → channel → UI, sparklines and gauges, resize-aware tiling |
| [`filebrowser`](examples/filebrowser) | recursive lazy tree over `fs.FS`, path-scoped IDs, file preview |
| [`hokm`](examples/hokm) | a playable card game drawn on a full-screen canvas window |
| [`svg`](examples/svg) | render a frame to an SVG file with a tiny custom Renderer |
| [`wasm`](examples/wasm) | the same kind of UI in a browser, on `<canvas>` |

When stdin/stdout is not a terminal, the terminal examples print a single
frame as plain text instead.

## Testing

```sh
go test ./...
go test -run '^$' -bench . -benchmem ./ ./renderer
```

UIs are testable headlessly: see [GUIDE §14](GUIDE.md#14-testing-uis).

## Upgrading from v0.1

The package is imported by its own name (`microui`, no `miniui` alias), the
renderer moved from `pkg/renderer` to `renderer`, results are typed
(`.Submitted()` etc.; `!= 0` still compiles), and `Slider`/`Number`/`PushID`
are generic. See [GUIDE §20](GUIDE.md#20-migrating-from-v01).

## License

MIT, same as the upstream library. Original C microui © rxi. See [LICENSE](LICENSE).
