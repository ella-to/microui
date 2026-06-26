// package microui is a tiny, portable, immediate-mode UI library, a pure-Go
// (no cgo) port of rxi's microui (https://github.com/rxi/microui).
//
// Like microui, miniui works within a renderer-agnostic model: it does not draw
// anything itself. Instead, each frame produces a list of draw commands
// (rectangles, text, icons, clip regions) that the caller is expected to render
// with any backend capable of drawing filled rectangles and text.
//
// A typical frame looks like:
//
//	ctx.Begin()
//	if ctx.BeginWindow("My Window", miniui.Rect{X: 10, Y: 10, W: 140, H: 86}) != 0 {
//	    ctx.LayoutRow([]int{60, -1}, 0)
//	    ctx.Label("First:")
//	    if ctx.Button("Button1") != 0 {
//	        // button pressed
//	    }
//	    ctx.EndWindow()
//	}
//	ctx.End()
//
//	for cmd := range ctx.Commands() {
//	    switch cmd.Type {
//	    case miniui.CommandRect: // draw cmd.Rect filled with cmd.Color
//	    case miniui.CommandText: // draw cmd.Str at cmd.Pos with cmd.Color
//	    case miniui.CommandIcon: // draw icon cmd.Icon inside cmd.Rect
//	    case miniui.CommandClip: // set clip rectangle to cmd.Rect
//	    }
//	}
//
// Before the first Begin, the caller must set the TextWidth and TextHeight
// callbacks so the layout system knows how large rendered text will be.
package microui

// Version is the version of microui this port tracks.
const Version = "2.02"

// Tunable pool sizes. Unlike microui these only bound retained state (windows,
// panels, tree nodes); the command list and the various stacks grow as needed.
const (
	containerPoolSize = 48
	treenodePoolSize  = 48
	maxWidths         = 16
)

// Real is the numeric type used for slider/number values, matching microui's
// MU_REAL.
type Real = float32

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

// Rect is an integer axis-aligned rectangle. X,Y is the top-left corner.
type Rect struct {
	X, Y, W, H int
}

// Color is an 8-bit-per-channel RGBA color.
type Color struct {
	R, G, B, A uint8
}

// RGBA builds a Color from ints, truncating each channel to a byte. It is a
// convenience matching microui's mu_color.
func RGBA(r, g, b, a int) Color {
	return Color{R: uint8(r), G: uint8(g), B: uint8(b), A: uint8(a)}
}

// clip results returned by checkClip.
const (
	clipPart = 1
	clipNone = 0
	clipAll  = 2
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
	ColorMax
)

// Icon identifies a built-in icon carried on a CommandIcon. The renderer
// decides how to draw each one.
const (
	IconClose = iota + 1
	IconCheck
	IconCollapsed
	IconExpanded
	IconMax
)

// Result flags returned by control functions, combined with bitwise OR.
const (
	ResActive = 1 << iota
	ResSubmit
	ResChange
)

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
)

// has reports whether opt contains flag.
func (opt Option) has(flag Option) bool { return opt&flag != 0 }

// Mouse button bits passed to InputMouseDown / InputMouseUp.
const (
	MouseLeft = 1 << iota
	MouseRight
	MouseMiddle
)

// Key bits passed to InputKeyDown / InputKeyUp.
const (
	KeyShift = 1 << iota
	KeyCtrl
	KeyAlt
	KeyBackspace
	KeyReturn
)

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
		},
	}
}

// unclippedRect is the "no clipping" sentinel pushed at the root of every
// window. Matches microui's unclipped_rect.
var unclippedRect = Rect{X: 0, Y: 0, W: 0x1000000, H: 0x1000000}

// ---- geometry helpers -------------------------------------------------------

func clampI(x, a, b int) int { return min(b, max(a, x)) }

func clampF(x, a, b Real) Real { return min(b, max(a, x)) }

func expandRect(r Rect, n int) Rect {
	return Rect{X: r.X - n, Y: r.Y - n, W: r.W + n*2, H: r.H + n*2}
}

func intersectRects(r1, r2 Rect) Rect {
	x1 := max(r1.X, r2.X)
	y1 := max(r1.Y, r2.Y)
	x2 := min(r1.X+r1.W, r2.X+r2.W)
	y2 := min(r1.Y+r1.H, r2.Y+r2.H)
	if x2 < x1 {
		x2 = x1
	}
	if y2 < y1 {
		y2 = y1
	}
	return Rect{X: x1, Y: y1, W: x2 - x1, H: y2 - y1}
}

func rectOverlapsVec2(r Rect, p Vec2) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}
