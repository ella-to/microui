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
	w, h    int
	cells   []cell
	hasWide bool // true if any cell may hold ch == 0 or a width-2 rune
}

func newScreen(w, h int) *screen {
	w, h = max(w, 1), max(h, 1)
	return &screen{w: w, h: h, cells: make([]cell, w*h), hasWide: true}
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
	s.hasWide = true
}

func (s *screen) at(x, y int) *cell { return &s.cells[y*s.w+x] }

func (s *screen) bounds() microui.Rect { return microui.Rect{W: s.w, H: s.h} }

// clear fills every cell with a blank of color bg.
func (s *screen) clear(bg microui.Color) {
	s.hasWide = false
	if len(s.cells) == 0 {
		return
	}
	fill := cell{ch: ' ', fg: bg, bg: bg}
	// Fill a small L1-resident seed block and copy it across the buffer so
	// subsequent copies always read from L1d instead of streaming the entire
	// screen through L2.
	seedLen := min(len(s.cells), 256)
	seed := s.cells[:seedLen]
	initLen := min(seedLen, 8)
	for i := range initLen {
		seed[i] = fill
	}
	for n := initLen; n < seedLen; n *= 2 {
		copy(seed[n:], seed[:n])
	}
	for off := seedLen; off < len(s.cells); off += seedLen {
		copy(s.cells[off:], seed)
	}
}

// blend composites src over dst using src's alpha. The result is opaque.
func blend(dst, src microui.Color) microui.Color {
	if src.A == 255 {
		return src
	}
	if src.A == 0 {
		return dst
	}
	a := uint32(src.A)
	inv := 255 - a
	return microui.Color{
		R: uint8((uint32(src.R)*a + uint32(dst.R)*inv + 127) / 255),
		G: uint8((uint32(src.G)*a + uint32(dst.G)*inv + 127) / 255),
		B: uint8((uint32(src.B)*a + uint32(dst.B)*inv + 127) / 255),
		A: 255,
	}
}

// fillRectClipped paints rect clipped to clip (which must lie within s.bounds()).
// An opaque color replaces the cells; a translucent one tints both their
// background and their glyphs, so overlays such as a modal backdrop dim what is
// beneath.
func (s *screen) fillRectClipped(rect, clip microui.Rect, color microui.Color) {
	if color.A == 0 {
		return
	}
	r := rect.Intersect(clip)
	if r.Empty() {
		return // an empty intersection may still carry an off-screen X
	}
	if color.A == 255 {
		fill := cell{ch: ' ', fg: color, bg: color}
		if r.W == 1 {
			idx := r.Y*s.w + r.X
			for range r.H {
				s.cells[idx] = fill
				idx += s.w
			}
			return
		}
		if r.W == s.w {
			dst := s.cells[r.Y*s.w : (r.Y+r.H)*s.w]
			seedLen := min(len(dst), 256)
			seed := dst[:seedLen]
			initLen := min(seedLen, 8)
			for i := range initLen {
				seed[i] = fill
			}
			for n := initLen; n < seedLen; n *= 2 {
				copy(seed[n:], seed[:n])
			}
			for off := seedLen; off < len(dst); off += seedLen {
				copy(dst[off:], seed)
			}
			return
		}
		off := r.Y*s.w + r.X
		row0 := s.cells[off : off+r.W]
		if r.W <= 16 {
			for i := range row0 {
				row0[i] = fill
			}
		} else {
			for i := range 8 {
				row0[i] = fill
			}
			for n := 8; n < len(row0); n *= 2 {
				copy(row0[n:], row0[:n])
			}
		}
		for y := 1; y < r.H; y++ {
			off += s.w
			copy(s.cells[off:off+r.W], row0)
		}
		return
	}

	a := uint32(color.A)
	inv := 255 - a
	sr := uint32(color.R)*a + 127
	sg := uint32(color.G)*a + 127
	sb := uint32(color.B)*a + 127
	for y := r.Y; y < r.Y+r.H; y++ {
		row := s.cells[y*s.w+r.X : y*s.w+r.X+r.W]
		for i := range row {
			c := &row[i]
			c.bg = microui.Color{
				R: uint8((sr + uint32(c.bg.R)*inv) / 255),
				G: uint8((sg + uint32(c.bg.G)*inv) / 255),
				B: uint8((sb + uint32(c.bg.B)*inv) / 255),
				A: 255,
			}
			c.fg = microui.Color{
				R: uint8((sr + uint32(c.fg.R)*inv) / 255),
				G: uint8((sg + uint32(c.fg.G)*inv) / 255),
				B: uint8((sb + uint32(c.fg.B)*inv) / 255),
				A: 255,
			}
		}
	}
}

// drawTextClipped writes str starting at pos within clip (which must lie
// within s.bounds()), keeping each cell's background so text sits on top of
// frames. Wide runes take two cells; zero-width runes (combining marks,
// joiners, control characters) are skipped.
func (s *screen) drawTextClipped(str string, pos microui.Vec2, clip microui.Rect, color microui.Color) {
	y := pos.Y
	if y < clip.Y || y >= clip.Y+clip.H || len(str) == 0 {
		return
	}
	clipRight := clip.X + clip.W
	x := pos.X
	if x >= clipRight {
		return
	}
	row := s.cells[y*s.w : (y+1)*s.w]
	opaque := color.A == 255
	for i := 0; i < len(str); i++ {
		b := str[i]
		if b >= 0x80 {
			for _, r := range str[i:] {
				w := runeWidth(r)
				if w == 0 {
					continue
				}
				if x >= clipRight {
					return
				}
				if x >= clip.X && x+w <= clipRight {
					c := &row[x]
					c.ch = r
					if opaque {
						c.fg = color
					} else {
						c.fg = blend(c.bg, color)
					}
					if w == 2 {
						s.hasWide = true
						next := &row[x+1]
						next.ch = 0
						next.bg = c.bg
					}
				}
				x += w
			}
			return
		}
		if b < 0x20 || b == 0x7f {
			continue
		}
		if x >= clipRight {
			return
		}
		if x >= clip.X {
			c := &row[x]
			c.ch = rune(b)
			if opaque {
				c.fg = color
			} else {
				c.fg = blend(c.bg, color)
			}
		}
		x++
	}
}

// drawIconClipped draws a single glyph centered within rect, clipped to clip
// (which must lie within s.bounds()).
func (s *screen) drawIconClipped(id microui.Icon, rect, clip microui.Rect, color microui.Color) {
	p := microui.Vec2{X: rect.X + (rect.W-1)/2, Y: rect.Y + (rect.H-1)/2}
	if clip.Contains(p) {
		c := s.at(p.X, p.Y)
		c.ch = iconGlyph(id)
		if color.A == 255 {
			c.fg = color
		} else {
			c.fg = blend(c.bg, color)
		}
	}
}

// normalize repairs wide glyphs that later draws cut in half: a lead cell
// without its continuation, or a continuation without its lead, becomes a
// blank. Afterwards every lead is followed by exactly one continuation, which
// is what the ANSI encoder relies on.
func (s *screen) normalize() {
	if !s.hasWide {
		return
	}
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
