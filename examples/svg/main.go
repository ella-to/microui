// Command svg renders a microui frame to an SVG file — no terminal, no window.
//
//	go run ella.to/microui/examples/svg > ui.svg
//
// It is the smallest complete renderer.Renderer in the repository (about 100
// lines), and shows that a backend does not have to be interactive: the same
// UI code that runs live in a terminal or a browser can be snapshotted for
// documentation, bug reports or golden-file tests. Because a Renderer has no
// input, the program drives a single frame itself with Connect, Begin/End and
// Paint instead of renderer.Run.
package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// Font metrics of the monospace font the SVG is rendered with. microui only
// needs widths and heights in the same units as its coordinates (pixels here).
const (
	charW    = 7
	lineH    = 14
	fontSize = 12
)

// SVG is a renderer.Renderer that serializes draw calls to an SVG document.
type SVG struct {
	W, H int

	b       strings.Builder
	clipID  int
	clipped bool // a clip group is open
}

// TextWidth implements renderer.Renderer.
func (s *SVG) TextWidth(_ microui.Font, str string) int { return charW * utf8.RuneCountInString(str) }

// TextHeight implements renderer.Renderer.
func (s *SVG) TextHeight(microui.Font) int { return lineH }

// Size implements renderer.Renderer.
func (s *SVG) Size() (w, h int) { return s.W, s.H }

// Clear starts a new document filled with bg.
func (s *SVG) Clear(bg microui.Color) {
	s.b.Reset()
	s.clipID, s.clipped = 0, false
	fmt.Fprintf(&s.b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" `+
		`font-family="Menlo, Consolas, monospace" font-size="%d">`, s.W, s.H, fontSize)
	s.FillRect(microui.Rect{W: s.W, H: s.H}, bg)
}

// SetClip closes the previous clip group and opens one for rect. microui
// resets clipping by passing a huge rectangle, which works here as-is.
func (s *SVG) SetClip(r microui.Rect) {
	s.closeClip()
	s.clipID++
	fmt.Fprintf(&s.b, `<clipPath id="c%d"><rect x="%d" y="%d" width="%d" height="%d"/></clipPath>`+
		`<g clip-path="url(#c%d)">`, s.clipID, r.X, r.Y, r.W, r.H, s.clipID)
	s.clipped = true
}

func (s *SVG) closeClip() {
	if s.clipped {
		s.b.WriteString("</g>")
		s.clipped = false
	}
}

// fill formats a color as SVG fill attributes, keeping alpha.
func fill(c microui.Color) string {
	return fmt.Sprintf(`fill="rgb(%d,%d,%d)" fill-opacity="%.3f"`, c.R, c.G, c.B, float64(c.A)/255)
}

// FillRect implements renderer.Renderer.
func (s *SVG) FillRect(r microui.Rect, c microui.Color) {
	fmt.Fprintf(&s.b, `<rect x="%d" y="%d" width="%d" height="%d" %s/>`, r.X, r.Y, r.W, r.H, fill(c))
}

// DrawText implements renderer.Renderer; pos is the text's top-left corner.
func (s *SVG) DrawText(_ microui.Font, str string, p microui.Vec2, c microui.Color) {
	fmt.Fprintf(&s.b, `<text x="%d" y="%d" dominant-baseline="text-before-edge" xml:space="preserve" %s>`,
		p.X, p.Y, fill(c))
	xml.EscapeText(&s.b, []byte(str))
	s.b.WriteString("</text>")
}

// DrawIcon draws each built-in icon as a glyph centered in r.
func (s *SVG) DrawIcon(id microui.Icon, r microui.Rect, c microui.Color) {
	glyph := map[microui.Icon]string{
		microui.IconClose:     "✕",
		microui.IconCheck:     "✓",
		microui.IconCollapsed: "▸",
		microui.IconExpanded:  "▾",
	}[id]
	p := microui.Vec2{X: r.X + (r.W-charW)/2, Y: r.Y + (r.H-lineH)/2}
	s.DrawText(nil, glyph, p, c)
}

// Present closes the document.
func (s *SVG) Present() {
	s.closeClip()
	s.b.WriteString("</svg>\n")
}

// String returns the finished SVG.
func (s *SVG) String() string { return s.b.String() }

// state the UI binds to; it would persist across frames in a live program.
var (
	name    = "Ada"
	volume  = 0.7
	quality = 3
	notify  = true
	logText = "Rendered without a screen.\nThe same ui() runs on any renderer."
)

// ui is an ordinary microui builder: nothing in it knows about SVG.
func ui(ctx *microui.Context) {
	if ctx.BeginWindow("Settings", microui.Rect{X: 20, Y: 20, W: 300, H: 260}).Active() {
		ctx.LayoutRow([]int{80, -1}, 0)
		ctx.Label("Name:")
		ctx.Textbox(&name)
		ctx.Label("Volume:")
		ctx.Slider(&volume, 0, 1)
		ctx.Label("Quality:")
		ctx.Slider(&quality, 1, 5)
		ctx.LayoutRow([]int{-1}, 0)
		ctx.Checkbox("Notifications", &notify)
		if ctx.HeaderEx("Log", microui.OptExpanded).Active() {
			ctx.Text(logText)
		}
		ctx.LayoutRow([]int{90, 90}, 0)
		ctx.Button("Save")
		ctx.Button("Cancel")
		ctx.EndWindow()
	}
}

// render paints one frame of build into a w*h SVG document.
func render(w, h int, build func(*microui.Context)) string {
	svg := &SVG{W: w, H: h}
	ctx := microui.New()
	renderer.Connect(ctx, svg) // wire TextWidth/TextHeight into the layout engine
	ctx.Begin()
	build(ctx)
	ctx.End()
	renderer.Paint(svg, ctx, microui.Hex(0x282c34))
	return svg.String()
}

func main() {
	if _, err := os.Stdout.WriteString(render(340, 300, ui)); err != nil {
		fmt.Fprintln(os.Stderr, "svg:", err)
		os.Exit(1)
	}
}
