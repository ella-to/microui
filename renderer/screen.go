package renderer

import (
	"strings"
	"unicode"

	"ella.to/microui"
)

// This file holds the terminal's in-memory surface: a grid of character
// cells that draw commands are composited into, with Unicode width handling
// and alpha blending.

// cell is one character cell. A glyph that is two columns wide (CJK, most
// emoji) occupies a lead cell holding the rune followed by a continuation
// cell whose ch is 0.
type cell struct {
	ch     rune
	fg, bg microui.Color
}

// screen is a w*h grid of cells.
type screen struct {
	w, h  int
	cells []cell
}

func newScreen(w, h int) *screen {
	w, h = max(w, 1), max(h, 1)
	return &screen{w: w, h: h, cells: make([]cell, w*h)}
}

// resize changes the surface size, discarding its contents.
func (s *screen) resize(w, h int) {
	w, h = max(w, 1), max(h, 1)
	if w == s.w && h == s.h {
		return
	}
	s.w, s.h = w, h
	if cap(s.cells) >= w*h {
		s.cells = s.cells[:w*h]
		clear(s.cells)
	} else {
		s.cells = make([]cell, w*h)
	}
}

func (s *screen) at(x, y int) *cell { return &s.cells[y*s.w+x] }

func (s *screen) bounds() microui.Rect { return microui.Rect{W: s.w, H: s.h} }

// clear fills every cell with a blank of color bg.
func (s *screen) clear(bg microui.Color) {
	if len(s.cells) == 0 {
		return
	}
	s.cells[0] = cell{ch: ' ', fg: bg, bg: bg}
	for n := 1; n < len(s.cells); n *= 2 {
		copy(s.cells[n:], s.cells[:n]) // doubling copy: memmove instead of a loop
	}
}

// blend composites src over dst using src's alpha. The result is opaque.
func blend(dst, src microui.Color) microui.Color {
	switch src.A {
	case 255:
		return src
	case 0:
		return dst
	}
	a := uint32(src.A)
	mix := func(d, s uint8) uint8 { return uint8((uint32(s)*a + uint32(d)*(255-a) + 127) / 255) }
	return microui.Color{R: mix(dst.R, src.R), G: mix(dst.G, src.G), B: mix(dst.B, src.B), A: 255}
}

// fillRect paints rect (clipped to clip and the screen). An opaque color
// replaces the cells; a translucent one tints both their background and
// their glyphs, so overlays such as a modal backdrop dim what is beneath.
func (s *screen) fillRect(rect, clip microui.Rect, color microui.Color) {
	r := rect.Intersect(clip).Intersect(s.bounds())
	opaque := color.A == 255
	for y := r.Y; y < r.Y+r.H; y++ {
		row := s.cells[y*s.w+r.X : y*s.w+r.X+r.W]
		for i := range row {
			c := &row[i]
			if opaque {
				*c = cell{ch: ' ', fg: color, bg: color}
				continue
			}
			c.bg = blend(c.bg, color)
			c.fg = blend(c.fg, color)
		}
	}
}

// drawText writes str starting at pos, keeping each cell's background so text
// sits on top of frames. Wide runes take two cells; zero-width runes
// (combining marks, joiners, control characters) are skipped.
func (s *screen) drawText(str string, pos microui.Vec2, clip microui.Rect, color microui.Color) {
	clip = clip.Intersect(s.bounds())
	y := pos.Y
	if y < clip.Y || y >= clip.Y+clip.H {
		return
	}
	x := pos.X
	for _, r := range str {
		w := runeWidth(r)
		if w == 0 {
			continue
		}
		if x >= clip.X+clip.W {
			return
		}
		if x >= clip.X && x+w <= clip.X+clip.W {
			c := s.at(x, y)
			c.ch = r
			c.fg = blend(c.bg, color)
			if w == 2 {
				next := s.at(x+1, y)
				next.ch = 0
				next.bg = c.bg
			}
		}
		x += w
	}
}

// drawIcon draws a single glyph centered within rect.
func (s *screen) drawIcon(id microui.Icon, rect, clip microui.Rect, color microui.Color) {
	p := microui.Vec2{X: rect.X + (rect.W-1)/2, Y: rect.Y + (rect.H-1)/2}
	if clip.Intersect(s.bounds()).Contains(p) {
		c := s.at(p.X, p.Y)
		c.ch = iconGlyph(id)
		c.fg = blend(c.bg, color)
	}
}

// normalize repairs wide glyphs that later draws cut in half: a lead cell
// without its continuation, or a continuation without its lead, becomes a
// blank. Afterwards every lead is followed by exactly one continuation, which
// is what the ANSI encoder relies on.
func (s *screen) normalize() {
	for y := range s.h {
		row := s.cells[y*s.w : (y+1)*s.w]
		for x := range row {
			c := &row[x]
			switch {
			case c.ch == 0:
				if x == 0 || runeWidth(row[x-1].ch) != 2 {
					c.ch = ' '
				}
			case c.ch < 0x1100: // fast path: nothing below U+1100 is wide
			case runeWidth(c.ch) == 2:
				if x+1 >= len(row) || row[x+1].ch != 0 {
					c.ch = ' '
				}
			}
		}
	}
}

// plainText renders the screen as rows of characters with no styling.
func (s *screen) plainText() string {
	var b strings.Builder
	b.Grow((s.w + 1) * s.h)
	for y := range s.h {
		for _, c := range s.cells[y*s.w : (y+1)*s.w] {
			if c.ch != 0 {
				b.WriteRune(c.ch)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// iconGlyph maps a microui icon id to a single display rune.
func iconGlyph(id microui.Icon) rune {
	switch id {
	case microui.IconClose:
		return '✕'
	case microui.IconCheck:
		return '✓'
	case microui.IconCollapsed:
		return '▶'
	case microui.IconExpanded:
		return '▼'
	default:
		return '?'
	}
}

// ---- character width --------------------------------------------------------

// StringWidth returns the number of terminal columns s occupies: 2 for each
// wide rune (CJK, most emoji), 0 for zero-width runes (combining marks,
// joiners, control characters) and 1 otherwise. It is the terminal's
// TextWidth, exported so UIs can center or truncate text consistently.
func StringWidth(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			// slow path from the first non-ASCII byte
			for _, r := range s[i:] {
				n += runeWidth(r)
			}
			return n
		}
		if s[i] >= 0x20 && s[i] != 0x7f {
			n++
		}
	}
	return n
}

// runeWidth returns the column width of r (0, 1 or 2), following the usual
// wcwidth conventions. Ambiguous-width runes are treated as narrow, which is
// what Western terminals do.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || r == 0x7f:
		return 0
	case r < 0x300:
		return 1
	case isZeroWidth(r):
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

func isZeroWidth(r rune) bool {
	switch {
	case r == 0x200B, r == 0x200C, r == 0x200D, r == 0x2060, r == 0xFEFF: // zero-width spaces/joiners
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF: // variation selectors
		return true
	case r >= 0x80 && r < 0xA0: // C1 controls
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

// wideRanges are the inclusive East Asian Wide/Fullwidth and emoji ranges.
var wideRanges = [...][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo
	{0x231A, 0x231B},   // watch, hourglass
	{0x2329, 0x232A},   // angle brackets
	{0x23E9, 0x23EC},   // media controls
	{0x23F0, 0x23F0},   // alarm clock
	{0x23F3, 0x23F3},   // hourglass
	{0x25FD, 0x25FE},   // small squares
	{0x2614, 0x2615},   // umbrella, hot beverage
	{0x2648, 0x2653},   // zodiac
	{0x267F, 0x267F},   // wheelchair
	{0x2693, 0x2693},   // anchor
	{0x26A1, 0x26A1},   // high voltage
	{0x26AA, 0x26AB},   // circles
	{0x26BD, 0x26BE},   // balls
	{0x26C4, 0x26C5},   // snowman, sun
	{0x26CE, 0x26CE},   // ophiuchus
	{0x26D4, 0x26D4},   // no entry
	{0x26EA, 0x26EA},   // church
	{0x26F2, 0x26F3},   // fountain, golf
	{0x26F5, 0x26F5},   // sailboat
	{0x26FA, 0x26FA},   // tent
	{0x26FD, 0x26FD},   // fuel pump
	{0x2705, 0x2705},   // check mark button
	{0x270A, 0x270B},   // fists
	{0x2728, 0x2728},   // sparkles
	{0x274C, 0x274C},   // cross mark
	{0x274E, 0x274E},   // cross mark button
	{0x2753, 0x2755},   // question marks
	{0x2757, 0x2757},   // exclamation
	{0x2795, 0x2797},   // plus, minus, divide
	{0x27B0, 0x27B0},   // curly loop
	{0x27BF, 0x27BF},   // double curly loop
	{0x2B1B, 0x2B1C},   // large squares
	{0x2B50, 0x2B50},   // star
	{0x2B55, 0x2B55},   // circle
	{0x2E80, 0x303E},   // CJK radicals .. CJK symbols
	{0x3041, 0x33FF},   // Hiragana .. CJK compatibility
	{0x3400, 0x4DBF},   // CJK extension A
	{0x4E00, 0x9FFF},   // CJK unified ideographs
	{0xA000, 0xA4CF},   // Yi
	{0xA960, 0xA97F},   // Hangul Jamo extended-A
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE10, 0xFE19},   // vertical forms
	{0xFE30, 0xFE6F},   // CJK compatibility forms
	{0xFF00, 0xFF60},   // fullwidth forms
	{0xFFE0, 0xFFE6},   // fullwidth signs
	{0x16FE0, 0x18AFF}, // Tangut etc.
	{0x1B000, 0x1B2FF}, // Kana supplement
	{0x1F004, 0x1F004}, // mahjong
	{0x1F0CF, 0x1F0CF}, // joker
	{0x1F18E, 0x1F18E}, // AB button
	{0x1F191, 0x1F19A}, // squared words
	{0x1F200, 0x1F251}, // enclosed ideographs
	{0x1F300, 0x1F64F}, // pictographs, emoticons
	{0x1F680, 0x1F6FF}, // transport and map
	{0x1F7E0, 0x1F7EB}, // colored circles/squares
	{0x1F90C, 0x1F9FF}, // supplemental symbols
	{0x1FA70, 0x1FAFF}, // symbols and pictographs extended-A
	{0x20000, 0x2FFFD}, // CJK extension B..
	{0x30000, 0x3FFFD}, // CJK extension G..
}

func isWide(r rune) bool {
	lo, hi := 0, len(wideRanges)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch {
		case r < wideRanges[m][0]:
			hi = m
		case r > wideRanges[m][1]:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}
