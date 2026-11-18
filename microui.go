// Package microui is a tiny, portable, immediate-mode UI library: a pure-Go
// (no cgo, no dependencies) port of rxi's microui
// (https://github.com/rxi/microui).
//
// microui never draws anything itself. Each frame produces a list of draw
// commands (filled rectangles, text, icons, clip regions) that the caller
// renders with any backend able to fill rectangles and draw text. The
// ella.to/microui/renderer package provides that loop plus a ready-made
// terminal backend.
//
// A typical frame looks like:
//
//	ctx.Begin()
//	if ctx.BeginWindow("My Window", microui.Rect{X: 10, Y: 10, W: 140, H: 86}).Active() {
//	    ctx.LayoutRow([]int{60, -1}, 0)
//	    ctx.Label("First:")
//	    if ctx.Button("Button1").Submitted() {
//	        // button pressed
//	    }
//	    ctx.EndWindow()
//	}
//	ctx.End()
//
//	for cmd := range ctx.Commands() {
//	    switch cmd.Type {
//	    case microui.CommandRect: // fill cmd.Rect with cmd.Color
//	    case microui.CommandText: // draw cmd.Str at cmd.Pos in cmd.Color
//	    case microui.CommandIcon: // draw icon cmd.Icon inside cmd.Rect
//	    case microui.CommandClip: // set the clip rectangle to cmd.Rect
//	    }
//	}
//
// Before the first Begin, the caller must set the TextWidth and TextHeight
// callbacks so the layout system knows how large rendered text will be.
package microui

// Version is the version of rxi/microui this port tracks.
const Version = "2.02"

// Tunable pool sizes. Unlike microui these only bound retained state (windows,
// panels, tree nodes); the command list and the various stacks grow as needed.
const (
	containerPoolSize = 48
	treenodePoolSize  = 48
	maxWidths         = 16
)

// Real is the numeric type microui uses internally for slider and number
// values, matching microui's MU_REAL. The value controls are generic, so
// callers may bind any Number type instead.
type Real = float32

// Number is the set of numeric types the value controls (Slider, Number) can
// be bound to.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// IDKey is the set of values GetID and PushID can hash into an ID: strings,
// byte slices and integers. Equal strings and byte slices hash equally.
type IDKey interface {
	~string | ~[]byte |
		~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// Font is an opaque, user-defined font handle passed back to the TextWidth and
// TextHeight callbacks and carried on text draw commands. It may be nil.
type Font = any

// ID is a control identifier produced by hashing. IDs let the library track
// retained per-control state (hover, focus, ...) across frames.
type ID uint32

// Vec2 is an integer 2D vector.
type Vec2 struct {
	X, Y int
}

// Add returns v+o.
func (v Vec2) Add(o Vec2) Vec2 { return Vec2{X: v.X + o.X, Y: v.Y + o.Y} }

// Sub returns v-o.
func (v Vec2) Sub(o Vec2) Vec2 { return Vec2{X: v.X - o.X, Y: v.Y - o.Y} }

// Rect is an integer axis-aligned rectangle. X,Y is the top-left corner.
type Rect struct {
	X, Y, W, H int
}

// Contains reports whether p lies inside r. The right and bottom edges are
// exclusive.
func (r Rect) Contains(p Vec2) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

// Expand grows r by n on every side (shrinks it when n is negative).
func (r Rect) Expand(n int) Rect {
	return Rect{X: r.X - n, Y: r.Y - n, W: r.W + n*2, H: r.H + n*2}
}

// Intersect returns the overlap of r and o, which is empty (W or H of 0) when
// they do not overlap.
func (r Rect) Intersect(o Rect) Rect {
	x1 := max(r.X, o.X)
	y1 := max(r.Y, o.Y)
	x2 := max(min(r.X+r.W, o.X+o.W), x1)
	y2 := max(min(r.Y+r.H, o.Y+o.H), y1)
	return Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

// Empty reports whether r has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Center returns the point at the middle of r.
func (r Rect) Center() Vec2 { return Vec2{X: r.X + r.W/2, Y: r.Y + r.H/2} }

// Color is an 8-bit-per-channel RGBA color. A is opacity: 255 is opaque, 0 is
// fully transparent. Renderers that can blend honor intermediate values.
type Color struct {
	R, G, B, A uint8
}

// RGBA builds a Color from ints, truncating each channel to a byte. It is a
// convenience matching microui's mu_color.
func RGBA(r, g, b, a int) Color {
	return Color{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}

// Hex builds an opaque Color from a 0xRRGGBB literal, e.g. Hex(0x282c34).
func Hex(rgb uint32) Color {
	return Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 255}
}

// WithAlpha returns c with its opacity replaced by a.
func (c Color) WithAlpha(a uint8) Color { c.A = a; return c }

// Lerp blends c toward o by t in [0,1] (all four channels), useful for
// animating colors.
func (c Color) Lerp(o Color, t float64) Color {
	t = min(max(t, 0), 1)
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return Color{R: mix(c.R, o.R), G: mix(c.G, o.G), B: mix(c.B, o.B), A: mix(c.A, o.A)}
}

// clip results returned by checkClip.
const (
	clipNone = iota
	clipPart
	clipAll
)

// ColorID indexes into Style.Colors.
type ColorID int

const (
	ColorText ColorID = iota
	ColorBorder
	ColorWindowBG
	ColorTitleBG
	ColorTitleText
	ColorPanelBG
	ColorButton
	ColorButtonHover
	ColorButtonFocus
	ColorBase
	ColorBaseHover
	ColorBaseFocus
	ColorScrollBase
	ColorScrollThumb
	ColorSelection
	ColorMax
)

// Icon identifies a built-in icon carried on a CommandIcon. The renderer
// decides how to draw each one.
type Icon int

const (
	IconClose Icon = iota + 1
	IconCheck
	IconCollapsed
	IconExpanded
	IconMax
)

// Result is the bitmask of flags returned by controls and containers.
type Result int

const (
	// ResActive: a window is open, a header or tree node is expanded.
	ResActive Result = 1 << iota
	// ResSubmit: a button was clicked, Enter was pressed in a textbox.
	ResSubmit
	// ResChange: a value control changed its bound value this frame.
	ResChange
)

// Active reports whether ResActive is set.
func (r Result) Active() bool { return r&ResActive != 0 }

// Submitted reports whether ResSubmit is set.
func (r Result) Submitted() bool { return r&ResSubmit != 0 }

// Changed reports whether ResChange is set.
func (r Result) Changed() bool { return r&ResChange != 0 }

// Option is a bitmask of control/window options.
type Option int

const (
	OptAlignCenter Option = 1 << iota
	OptAlignRight
	OptNoInteract
	OptNoFrame
	OptNoResize
	OptNoScroll
	OptNoClose
	OptNoTitle
	OptHoldFocus
	OptAutoSize
	OptPopup
	OptClosed
	OptExpanded
	// OptPassword makes a textbox display PasswordMask in place of every rune
	// while still editing the real value.
	OptPassword
)

// has reports whether opt contains flag.
func (opt Option) has(flag Option) bool { return opt&flag != 0 }

// MouseButton is a bitmask of mouse buttons, passed to InputMouseDown /
// InputMouseUp and returned by MouseDown / MousePressed.
type MouseButton int

const (
	MouseLeft MouseButton = 1 << iota
	MouseRight
	MouseMiddle
)

// Key is a bitmask of keys, passed to InputKeyDown / InputKeyUp and returned
// by KeyDown / KeyPressed.
//
// The editing keys (arrows, Home, End, Delete, select-all) drive the textbox
// caret and selection; backends that do not report them simply get
// end-of-line editing. The navigation keys (Up, Down, Tab, Escape, PageUp,
// PageDown) are not used by the built-in controls but are reported so
// applications can implement keyboard navigation.
type Key int

const (
	KeyShift Key = 1 << iota
	KeyCtrl
	KeyAlt
	KeyBackspace
	KeyReturn
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyDelete
	// KeySelectAll selects a focused textbox's whole value. It is its own bit
	// (rather than Ctrl+A) so backends can map whatever chord is native.
	KeySelectAll
	KeyUp
	KeyDown
	KeyTab
	KeyEscape
	KeyPageUp
	KeyPageDown
)

// PasswordMask is the rune drawn per input rune by textboxes with
// OptPassword. It defaults to '*' so even ASCII-only backends render it.
var PasswordMask = '*'

// Style holds the colors and metrics used to lay out and draw controls.
type Style struct {
	Font          Font
	Size          Vec2
	Padding       int
	Spacing       int
	Indent        int
	TitleHeight   int
	ScrollbarSize int
	ThumbSize     int
	// MinWidth and MinHeight are the smallest size a window may be resized to
	// via its resize handle, in the renderer's coordinate units. microui
	// hardcodes 96x64 (pixels); expose it here so non-pixel renderers (e.g. a
	// terminal measured in cells) can use an appropriate minimum.
	MinWidth  int
	MinHeight int
	// ResizeHandle is the size of the square grab zone in a window's
	// bottom-right corner. microui couples this to TitleHeight; when this field
	// is <= 0 that behaviour is kept. Set it explicitly when the title height
	// is too small to grab comfortably (e.g. a 1-cell title bar in a terminal).
	ResizeHandle int
	Colors       [ColorMax]Color
}

// DefaultStyle returns a copy of microui's default style.
func DefaultStyle() Style {
	return Style{
		Size:          Vec2{X: 68, Y: 10},
		Padding:       5,
		Spacing:       4,
		Indent:        24,
		TitleHeight:   24,
		ScrollbarSize: 12,
		ThumbSize:     8,
		MinWidth:      96,
		MinHeight:     64,
		Colors: [ColorMax]Color{
			ColorText:        {230, 230, 230, 255},
			ColorBorder:      {25, 25, 25, 255},
			ColorWindowBG:    {50, 50, 50, 255},
			ColorTitleBG:     {25, 25, 25, 255},
			ColorTitleText:   {240, 240, 240, 255},
			ColorPanelBG:     {0, 0, 0, 0},
			ColorButton:      {75, 75, 75, 255},
			ColorButtonHover: {95, 95, 95, 255},
			ColorButtonFocus: {115, 115, 115, 255},
			ColorBase:        {30, 30, 30, 255},
			ColorBaseHover:   {35, 35, 35, 255},
			ColorBaseFocus:   {40, 40, 40, 255},
			ColorScrollBase:  {43, 43, 43, 255},
			ColorScrollThumb: {30, 30, 30, 255},
			ColorSelection:   {60, 90, 130, 255},
		},
	}
}

// unclippedRect is the "no clipping" sentinel pushed at the root of every
// window. Matches microui's unclipped_rect.
var unclippedRect = Rect{X: 0, Y: 0, W: 0x1000000, H: 0x1000000}

func clampI(x, a, b int) int { return min(b, max(a, x)) }
