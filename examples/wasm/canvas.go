//go:build js && wasm

package main

import (
	"errors"
	"strconv"
	"sync"
	"syscall/js"
	"unicode/utf8"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// fontPx is the CSS pixel size of the UI font; textHeight is the line height
// reported to microui's layout.
const (
	fontPx     = 14
	textHeight = 16
	fontCSS    = "14px system-ui, -apple-system, 'Segoe UI', sans-serif"
)

// maxWidthCache bounds the measureText cache; typing into a textbox produces
// a new string every keystroke, so the cache is simply reset when full.
const maxWidthCache = 4096

// Canvas is a renderer.Driver that draws onto an HTML <canvas> with the 2D
// context API and reads mouse/keyboard input from DOM events, using nothing
// but syscall/js.
//
// Coordinates are CSS pixels; the backing store is scaled by
// devicePixelRatio so text stays crisp on high-DPI screens.
type Canvas struct {
	// BG is the clear color, readable and writable from the UI.
	BG microui.Color

	el, g2d js.Value
	w, h    int // CSS pixels

	widths   map[string]int // measureText cache
	clipping bool           // a save()/clip() is active
	fill     microui.Color  // last fillStyle set, to skip redundant sets
	fillSet  bool

	mu      sync.Mutex
	queue   []event
	resized bool
	wake    chan struct{}

	listeners []listener
}

// event is one DOM input event, queued by a JS callback and applied by Poll.
type event struct {
	kind   eventKind
	x, y   int
	btn    microui.MouseButton
	key    microui.Key
	text   string
	dx, dy int
}

type eventKind int

const (
	evMove eventKind = iota
	evDown
	evUp
	evScroll
	evKeyTap  // a discrete key press: down + up in one frame
	evKeyDown // modifier held
	evKeyUp   // modifier released
	evText
)

// discrete reports whether e must end its frame's batch: microui sees keys
// and buttons as per-frame bitmasks, so two presses of the same key in one
// frame would collapse into one, and motion applied after a click would move
// the point the click lands on.
func (e event) discrete() bool {
	return e.kind == evDown || e.kind == evUp || e.kind == evKeyTap || e.kind == evText
}

type listener struct {
	target js.Value
	typ    string
	fn     js.Func
}

// NewCanvas attaches to the <canvas> element with the given id.
func NewCanvas(id string) (*Canvas, error) {
	doc := js.Global().Get("document")
	el := doc.Call("getElementById", id)
	if el.IsNull() || el.IsUndefined() {
		return nil, errors.New("no element with id " + strconv.Quote(id))
	}
	c := &Canvas{
		BG:     microui.RGBA(40, 44, 52, 255),
		el:     el,
		g2d:    el.Call("getContext", "2d"),
		widths: map[string]int{},
		wake:   make(chan struct{}, 1),
	}
	c.resize()
	c.listen()
	return c, nil
}

// resize matches the backing store to the element's CSS size times
// devicePixelRatio. Assigning canvas.width resets the 2D context state, so
// the transform and text settings are re-applied.
func (c *Canvas) resize() {
	dpr := js.Global().Get("devicePixelRatio").Float()
	if dpr <= 0 {
		dpr = 1
	}
	c.w = c.el.Get("clientWidth").Int()
	c.h = c.el.Get("clientHeight").Int()
	c.el.Set("width", int(float64(c.w)*dpr))
	c.el.Set("height", int(float64(c.h)*dpr))
	c.g2d.Call("setTransform", dpr, 0, 0, dpr, 0, 0)
	c.g2d.Set("font", fontCSS)
	c.g2d.Set("textBaseline", "top")
	c.clipping = false
	c.fillSet = false
}

// ---- renderer.Renderer ------------------------------------------------------

// TextWidth measures str with the canvas font, caching results because
// measureText crosses the JS boundary and is comparatively slow.
func (c *Canvas) TextWidth(_ microui.Font, str string) int {
	if str == "" {
		return 0
	}
	if w, ok := c.widths[str]; ok {
		return w
	}
	if len(c.widths) >= maxWidthCache {
		clear(c.widths)
	}
	w := int(c.g2d.Call("measureText", str).Get("width").Float() + 0.5)
	c.widths[str] = w
	return w
}

// TextHeight returns the fixed line height.
func (c *Canvas) TextHeight(microui.Font) int { return textHeight }

// Size reports the canvas size in CSS pixels.
func (c *Canvas) Size() (w, h int) { return c.w, c.h }

// Clear fills the canvas with bg and drops any clip.
func (c *Canvas) Clear(bg microui.Color) {
	c.mu.Lock()
	resized := c.resized
	c.resized = false
	c.mu.Unlock()
	if resized {
		c.resize()
	}
	c.unclip()
	c.setFill(bg)
	c.g2d.Call("fillRect", 0, 0, c.w, c.h)
}

// SetClip restricts drawing to rect via save()+clip(); the previous clip is
// dropped first with restore(), since canvas clips can only shrink.
func (c *Canvas) SetClip(rect microui.Rect) {
	c.unclip()
	r := rect.Intersect(microui.Rect{W: c.w, H: c.h})
	c.g2d.Call("save")
	c.g2d.Call("beginPath")
	c.g2d.Call("rect", r.X, r.Y, r.W, r.H)
	c.g2d.Call("clip")
	c.clipping = true
}

func (c *Canvas) unclip() {
	if c.clipping {
		c.g2d.Call("restore") // also restores fillStyle
		c.clipping = false
		c.fillSet = false
	}
}

// setFill sets fillStyle (and strokeStyle) only when the color changed.
func (c *Canvas) setFill(col microui.Color) {
	if c.fillSet && c.fill == col {
		return
	}
	css := cssColor(col)
	c.g2d.Set("fillStyle", css)
	c.g2d.Set("strokeStyle", css)
	c.fill, c.fillSet = col, true
}

// cssColor formats col as rgba(r,g,b,a).
func cssColor(col microui.Color) string {
	b := make([]byte, 0, 24)
	b = append(b, "rgba("...)
	b = strconv.AppendInt(b, int64(col.R), 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, int64(col.G), 10)
	b = append(b, ',')
	b = strconv.AppendInt(b, int64(col.B), 10)
	b = append(b, ',')
	b = strconv.AppendFloat(b, float64(col.A)/255, 'f', 3, 64)
	b = append(b, ')')
	return string(b)
}

// FillRect fills rect; the canvas blends alpha natively.
func (c *Canvas) FillRect(rect microui.Rect, col microui.Color) {
	c.setFill(col)
	c.g2d.Call("fillRect", rect.X, rect.Y, rect.W, rect.H)
}

// DrawText draws str with its top-left corner at pos.
func (c *Canvas) DrawText(_ microui.Font, str string, pos microui.Vec2, col microui.Color) {
	c.setFill(col)
	// center the font's glyphs inside the taller layout line
	c.g2d.Call("fillText", str, pos.X, pos.Y+(textHeight-fontPx)/2)
}

// DrawIcon draws the built-in icons as small vector shapes centered in rect.
func (c *Canvas) DrawIcon(id microui.Icon, rect microui.Rect, col microui.Color) {
	c.setFill(col)
	g := c.g2d
	cx := float64(rect.X) + float64(rect.W)/2
	cy := float64(rect.Y) + float64(rect.H)/2
	s := float64(min(rect.W, rect.H)) * 0.25 // half-size of the glyph

	g.Call("beginPath")
	switch id {
	case microui.IconClose:
		g.Set("lineWidth", 1.5)
		g.Call("moveTo", cx-s, cy-s)
		g.Call("lineTo", cx+s, cy+s)
		g.Call("moveTo", cx+s, cy-s)
		g.Call("lineTo", cx-s, cy+s)
		g.Call("stroke")
	case microui.IconCheck:
		g.Set("lineWidth", 2)
		g.Call("moveTo", cx-s, cy)
		g.Call("lineTo", cx-s/3, cy+s*0.7)
		g.Call("lineTo", cx+s, cy-s*0.7)
		g.Call("stroke")
	case microui.IconCollapsed: // triangle pointing right
		g.Call("moveTo", cx-s*0.6, cy-s)
		g.Call("lineTo", cx+s*0.8, cy)
		g.Call("lineTo", cx-s*0.6, cy+s)
		g.Call("fill")
	case microui.IconExpanded: // triangle pointing down
		g.Call("moveTo", cx-s, cy-s*0.6)
		g.Call("lineTo", cx+s, cy-s*0.6)
		g.Call("lineTo", cx, cy+s*0.8)
		g.Call("fill")
	}
}

// Present is a no-op: canvas drawing is immediate and the browser composites
// it at the end of the animation frame.
func (c *Canvas) Present() { c.unclip() }

// ---- renderer.Driver ----------------------------------------------------------

// Style returns microui's default pixel-based style.
func (c *Canvas) Style() microui.Style { return microui.DefaultStyle() }

// Background returns the current clear color.
func (c *Canvas) Background() microui.Color { return c.BG }

// modMask is the set of modifier key bits.
const modMask = microui.KeyShift | microui.KeyCtrl | microui.KeyAlt

// Poll applies queued DOM events to ctx. Continuous events (motion, wheel,
// modifier changes) coalesce freely, but a discrete event (button, key, text)
// ends the frame's batch: everything after it stays queued for the next
// frame, so fast typing never loses or reorders keystrokes and a click
// registers where it happened. A browser tab never quits on its own.
func (c *Canvas) Poll(ctx *microui.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.queue {
		n++
		switch e.kind {
		case evMove:
			ctx.InputMouseMove(e.x, e.y)
		case evDown:
			ctx.InputMouseDown(e.x, e.y, e.btn)
		case evUp:
			ctx.InputMouseUp(e.x, e.y, e.btn)
		case evScroll:
			ctx.InputScroll(e.dx, e.dy)
		case evKeyTap:
			// Release only the tapped key: modifiers carried on the event are
			// still physically held and are released by their own keyup.
			ctx.InputKeyDown(e.key)
			ctx.InputKeyUp(e.key &^ modMask)
		case evKeyDown:
			ctx.InputKeyDown(e.key)
		case evKeyUp:
			ctx.InputKeyUp(e.key)
		case evText:
			ctx.InputText(e.text)
		}
		if e.discrete() {
			break
		}
	}
	c.queue = append(c.queue[:0], c.queue[n:]...)
	return false
}

// Wake implements renderer.Waker: it is signalled whenever input is queued,
// so a renderer.Run loop redraws immediately.
func (c *Canvas) Wake() <-chan struct{} { return c.wake }

// Close removes the DOM listeners and releases their js.Func handles.
func (c *Canvas) Close() error {
	for _, l := range c.listeners {
		l.target.Call("removeEventListener", l.typ, l.fn)
		l.fn.Release()
	}
	c.listeners = nil
	return nil
}

// ---- DOM input ------------------------------------------------------------------

// push queues an event and wakes the frame loop.
func (c *Canvas) push(e event) {
	c.mu.Lock()
	c.queue = append(c.queue, e)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// on registers a DOM listener, keeping the js.Func alive until Close.
func (c *Canvas) on(target js.Value, typ string, passive bool, fn func(e js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		fn(args[0])
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", passive)
	target.Call("addEventListener", typ, f, opts)
	c.listeners = append(c.listeners, listener{target: target, typ: typ, fn: f})
}

// pos converts a mouse event's client coordinates to canvas CSS pixels.
func (c *Canvas) pos(e js.Value) (int, int) {
	r := c.el.Call("getBoundingClientRect")
	return int(e.Get("clientX").Float() - r.Get("left").Float()),
		int(e.Get("clientY").Float() - r.Get("top").Float())
}

func mouseButton(b int) microui.MouseButton {
	switch b {
	case 0:
		return microui.MouseLeft
	case 1:
		return microui.MouseMiddle
	case 2:
		return microui.MouseRight
	}
	return 0
}

// namedKeys maps KeyboardEvent.key values to microui key bits.
var namedKeys = map[string]microui.Key{
	"Backspace":  microui.KeyBackspace,
	"Enter":      microui.KeyReturn,
	"ArrowLeft":  microui.KeyLeft,
	"ArrowRight": microui.KeyRight,
	"ArrowUp":    microui.KeyUp,
	"ArrowDown":  microui.KeyDown,
	"Home":       microui.KeyHome,
	"End":        microui.KeyEnd,
	"Delete":     microui.KeyDelete,
	"Tab":        microui.KeyTab,
	"Escape":     microui.KeyEscape,
	"PageUp":     microui.KeyPageUp,
	"PageDown":   microui.KeyPageDown,
}

// modifierKeys maps modifier KeyboardEvent.key values; these get real
// down/up events so "held" state (e.g. shift-click on a slider) works.
var modifierKeys = map[string]microui.Key{
	"Shift":   microui.KeyShift,
	"Control": microui.KeyCtrl,
	"Meta":    microui.KeyCtrl, // Cmd acts as Ctrl on macOS
	"Alt":     microui.KeyAlt,
}

// mods returns the modifier bits held during a keyboard event.
func mods(e js.Value) microui.Key {
	var m microui.Key
	if e.Get("shiftKey").Bool() {
		m |= microui.KeyShift
	}
	if e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() {
		m |= microui.KeyCtrl
	}
	if e.Get("altKey").Bool() {
		m |= microui.KeyAlt
	}
	return m
}

// listen wires DOM events into the input queue.
func (c *Canvas) listen() {
	win := js.Global().Get("window")

	// Motion and release listen on window so drags keep working when the
	// pointer leaves the canvas.
	c.on(win, "mousemove", true, func(e js.Value) {
		x, y := c.pos(e)
		c.push(event{kind: evMove, x: x, y: y})
	})
	c.on(c.el, "mousedown", false, func(e js.Value) {
		if b := mouseButton(e.Get("button").Int()); b != 0 {
			x, y := c.pos(e)
			c.push(event{kind: evDown, x: x, y: y, btn: b})
			e.Call("preventDefault") // no text selection / focus stealing
		}
	})
	c.on(win, "mouseup", true, func(e js.Value) {
		if b := mouseButton(e.Get("button").Int()); b != 0 {
			x, y := c.pos(e)
			c.push(event{kind: evUp, x: x, y: y, btn: b})
		}
	})
	c.on(c.el, "contextmenu", false, func(e js.Value) { e.Call("preventDefault") })
	c.on(c.el, "wheel", false, func(e js.Value) {
		dx, dy := e.Get("deltaX").Float(), e.Get("deltaY").Float()
		if e.Get("deltaMode").Int() == 1 { // lines, not pixels
			dx, dy = dx*30, dy*30
		}
		c.push(event{kind: evScroll, dx: int(dx), dy: int(dy)})
		e.Call("preventDefault")
	})

	c.on(win, "keydown", false, func(e js.Value) {
		key := e.Get("key").String()
		m := mods(e)
		if e.Call("getModifierState", "AltGraph").Bool() {
			// AltGr reports as Ctrl+Alt on Windows; it types characters such
			// as '@' on many layouts, so it is not a shortcut chord.
			m &^= microui.KeyCtrl | microui.KeyAlt
		}
		switch {
		case modifierKeys[key] != 0:
			c.push(event{kind: evKeyDown, key: modifierKeys[key]})
		case namedKeys[key] != 0:
			c.push(event{kind: evKeyTap, key: namedKeys[key] | m})
			e.Call("preventDefault") // Backspace/Tab/arrows must not navigate
		case m&microui.KeyCtrl != 0 && (key == "a" || key == "A"):
			c.push(event{kind: evKeyTap, key: microui.KeySelectAll})
			e.Call("preventDefault")
		case m&microui.KeyCtrl != 0:
			// leave other shortcuts (Cmd-V paste, Cmd-R reload) to the browser
		case utf8.RuneCountInString(key) == 1:
			c.push(event{kind: evText, text: key})
			e.Call("preventDefault") // e.g. Space must not scroll the page
		}
	})
	c.on(win, "keyup", true, func(e js.Value) {
		if k := modifierKeys[e.Get("key").String()]; k != 0 {
			c.push(event{kind: evKeyUp, key: k})
		}
	})
	// Losing focus while a modifier is held would otherwise leave it stuck.
	c.on(win, "blur", true, func(js.Value) {
		c.push(event{kind: evKeyUp, key: microui.KeyShift | microui.KeyCtrl | microui.KeyAlt})
	})
	c.on(win, "paste", false, func(e js.Value) {
		text := e.Get("clipboardData").Call("getData", "text").String()
		if text != "" {
			c.push(event{kind: evText, text: singleLine(text)})
			e.Call("preventDefault")
		}
	})
	c.on(win, "resize", true, func(js.Value) {
		c.mu.Lock()
		c.resized = true
		c.mu.Unlock()
	})
}

// singleLine flattens pasted text for microui's single-line textboxes.
func singleLine(s string) string {
	b := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\r':
		case '\n', '\t':
			b = append(b, ' ')
		default:
			b = append(b, r)
		}
	}
	return string(b)
}

var _ interface {
	renderer.Driver
	renderer.Waker
} = (*Canvas)(nil)
