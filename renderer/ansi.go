package renderer

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"ella.to/microui"
)

// This file turns the cell surface into ANSI escape sequences. Only cells that
// differ from what the terminal already shows are written, the whole frame is
// built in one reused buffer, and it is wrapped in a synchronized update so the
// terminal swaps it in atomically (no tearing).

// ColorMode selects how colors are encoded for the terminal.
type ColorMode int

const (
	// ColorAuto detects the mode from the environment (see DetectColorMode).
	ColorAuto ColorMode = iota
	// ColorTrue emits 24-bit colors.
	ColorTrue
	// Color256 quantizes to the xterm 256-color palette.
	Color256
	// Color16 quantizes to the 16 basic ANSI colors.
	Color16
)

func (m ColorMode) String() string {
	switch m {
	case ColorTrue:
		return "truecolor"
	case Color256:
		return "256"
	case Color16:
		return "16"
	}
	return "auto"
}

// DetectColorMode picks the richest color mode the terminal advertises:
// truecolor when COLORTERM says so (iTerm2, Ghostty, Kitty, WezTerm, VS Code,
// Windows Terminal, ...) or TERM names a direct-color terminal, 16 colors for
// TERM=linux and other basic consoles, and 256 colors otherwise. Setting
// COLORTERM=truecolor forces 24-bit color.
func DetectColorMode() ColorMode {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return ColorTrue
	}
	term := strings.ToLower(os.Getenv("TERM"))
	switch {
	case strings.Contains(term, "direct"), strings.Contains(term, "truecolor"),
		strings.HasPrefix(term, "xterm-kitty"), strings.HasPrefix(term, "xterm-ghostty"),
		strings.HasPrefix(term, "wezterm"), strings.HasPrefix(term, "alacritty"), strings.HasPrefix(term, "foot"):
		return ColorTrue
	case os.Getenv("WT_SESSION") != "": // Windows Terminal
		return ColorTrue
	case term == "linux", term == "vt100", term == "vt220", term == "ansi", term == "cons25":
		return Color16
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty", "Hyper", "Tabby":
		return ColorTrue
	}
	return Color256
}

// ansi256 returns the xterm-256 palette index nearest to c, choosing between
// the 6x6x6 color cube (16-231) and the grayscale ramp (232-255).
func ansi256(c microui.Color) int {
	// Nearest cube channel: levels are 0, 95, 135, 175, 215, 255.
	cubeIdx := func(v uint8) int {
		switch {
		case v < 48:
			return 0
		case v < 115:
			return 1
		}
		return (int(v) - 35) / 40
	}
	cubeVal := func(i int) int {
		if i == 0 {
			return 0
		}
		return 55 + 40*i
	}
	qr, qg, qb := cubeIdx(c.R), cubeIdx(c.G), cubeIdx(c.B)
	cr, cg, cb := cubeVal(qr), cubeVal(qg), cubeVal(qb)

	// Nearest gray ramp entry: levels are 8, 18, ..., 238.
	avg := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi := min(max((avg-3)/10, 0), 23)
	gv := 8 + 10*gi

	if colorDist(c, gv, gv, gv) < colorDist(c, cr, cg, cb) {
		return 232 + gi
	}
	return 16 + 36*qr + 6*qg + qb
}

func colorDist(c microui.Color, r, g, b int) int {
	dr, dg, db := r-int(c.R), g-int(c.G), b-int(c.B)
	return dr*dr + dg*dg + db*db
}

// ansi16Palette is the conventional xterm rendering of the 16 basic colors.
var ansi16Palette = [16][3]int{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
	{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
	{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// ansi16 returns the index (0-15) of the basic ANSI color nearest to c.
func ansi16(c microui.Color) int {
	best, bestD := 0, 1<<31-1
	for i, p := range ansi16Palette {
		if d := colorDist(c, p[0], p[1], p[2]); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// colorKey reduces c to what mode can actually show, so two colors that
// quantize to the same palette entry don't cost a redundant SGR sequence.
// The ColorTrue fast path stays small so callers inline it.
func colorKey(mode ColorMode, c microui.Color) uint32 {
	if mode == ColorTrue {
		return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	}
	return colorKeySlow(mode, c)
}

func colorKeySlow(mode ColorMode, c microui.Color) uint32 {
	switch mode {
	case Color256:
		return uint32(ansi256(c))
	case Color16:
		return uint32(ansi16(c))
	}
	return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

// appendUint8 appends v in base 10 without strconv call overhead.
func appendUint8(buf []byte, v uint8) []byte {
	if v >= 100 {
		buf = append(buf, '0'+v/100)
		v %= 100
		return append(buf, '0'+v/10, '0'+v%10)
	}
	if v >= 10 {
		return append(buf, '0'+v/10, '0'+v%10)
	}
	return append(buf, '0'+v)
}

// encoder accumulates one frame of output.
type encoder struct {
	mode ColorMode
	buf  []byte

	// pen is the terminal's current SGR state; penOK is false when unknown.
	fg, bg uint32
	penOK  bool
	// cx, cy is the terminal cursor; cx < 0 means unknown.
	cx, cy int
}

func (e *encoder) reset() {
	e.buf = e.buf[:0]
	e.penOK = false
	e.cx = -1
}

func (e *encoder) str(s string) { e.buf = append(e.buf, s...) }

func (e *encoder) num(n int) {
	if uint(n) <= 255 {
		e.buf = appendUint8(e.buf, uint8(n))
		return
	}
	e.buf = strconv.AppendInt(e.buf, int64(n), 10)
}

// moveTo positions the cursor at (x, y) unless it is already there.
func (e *encoder) moveTo(x, y int) {
	if e.cx == x && e.cy == y {
		return
	}
	e.str("\x1b[")
	e.num(y + 1)
	e.buf = append(e.buf, ';')
	e.num(x + 1)
	e.buf = append(e.buf, 'H')
	e.cx, e.cy = x, y
}

// colorParams appends the SGR parameters selecting colorKey key as the
// foreground (or background when bg is set).
func (e *encoder) colorParams(key uint32, bg bool) {
	switch e.mode {
	case Color16:
		i := int(key)
		base := 30
		if i >= 8 {
			base, i = 90, i-8
		}
		if bg {
			base += 10
		}
		e.buf = appendUint8(e.buf, uint8(base+i))
	case Color256:
		if bg {
			e.str("48;5;")
		} else {
			e.str("38;5;")
		}
		e.buf = appendUint8(e.buf, uint8(key))
	default:
		if bg {
			e.str("48;2;")
		} else {
			e.str("38;2;")
		}
		e.buf = appendUint8(e.buf, uint8(key>>16))
		e.buf = append(e.buf, ';')
		e.buf = appendUint8(e.buf, uint8(key>>8))
		e.buf = append(e.buf, ';')
		e.buf = appendUint8(e.buf, uint8(key))
	}
}

// setPen switches the terminal to c's colors, emitting only what changed. A
// blank's foreground is invisible, so it is left alone.
func (e *encoder) setPen(c cell) {
	bgKey := colorKey(e.mode, c.bg)
	needBG := !e.penOK || e.bg != bgKey
	var fgKey uint32
	needFG := false
	if c.ch != ' ' {
		fgKey = colorKey(e.mode, c.fg)
		needFG = !e.penOK || e.fg != fgKey
	}
	if !needFG && !needBG {
		return
	}
	e.str("\x1b[")
	if needFG {
		e.colorParams(fgKey, false)
		e.fg = fgKey
	}
	if needBG {
		if needFG {
			e.buf = append(e.buf, ';')
		}
		e.colorParams(bgKey, true)
		e.bg = bgKey
	}
	e.buf = append(e.buf, 'm')
	if !e.penOK {
		// the first SGR of a frame must define both colors
		if !needFG {
			e.fg = ^uint32(0) // unknown: force the next glyph to set it
		}
		e.penOK = true
	}
}

// put writes cell c at (x, y), which spans w columns.
func (e *encoder) put(x, y int, c cell, w, screenW int) {
	e.moveTo(x, y)
	e.setPen(c)
	if c.ch < utf8.RuneSelf {
		e.buf = append(e.buf, byte(c.ch))
	} else {
		e.buf = utf8.AppendRune(e.buf, c.ch)
	}
	e.cx += w
	if e.cx >= screenW {
		e.cx = -1 // pending-wrap state differs between terminals: re-position
	}
}

// encodeDiff appends the escape sequences that turn the terminal from front
// into back (both normalized, same size). With full set every cell is written.
// It reports whether anything was emitted.
func (e *encoder) encodeDiff(front, back *screen, full bool) bool {
	if !full && bytes.Equal(cellBytes(back.cells), cellBytes(front.cells)) {
		return false // idle frame: whole screen matches in one memequal
	}
	start := len(e.buf)
	w := back.w
	if !front.hasWide && !back.hasWide {
		// Fast path: neither buffer contains wide runes or continuation cells,
		// so every cell spans exactly one column.
		if full {
			for y := range back.h {
				brow := back.cells[y*w : (y+1)*w]
				for x, c := range brow {
					e.put(x, y, c, 1, w)
				}
			}
			return len(e.buf) > start
		}
		const chunk = 16
		for y := range back.h {
			brow := back.cells[y*w : (y+1)*w]
			frow := front.cells[y*w : (y+1)*w]
			if bytes.Equal(cellBytes(brow), cellBytes(frow)) {
				continue
			}
			for x := 0; x < w; x += chunk {
				end := min(x+chunk, w)
				bc := brow[x:end]
				fc := frow[x:end]
				if bytes.Equal(cellBytes(bc), cellBytes(fc)) {
					continue
				}
				_ = fc[len(bc)-1]
				for i, c := range bc {
					if c != fc[i] {
						col := x + i
						e.fillGapNarrow(brow, col, y)
						e.put(col, y, c, 1, w)
					}
				}
			}
		}
		return len(e.buf) > start
	}
	for y := range back.h {
		brow := back.cells[y*w : (y+1)*w]
		frow := front.cells[y*w : (y+1)*w]
		if !full && bytes.Equal(cellBytes(brow), cellBytes(frow)) {
			continue // the common case: an unchanged row, compared with memequal
		}
		for x := 0; x < w; x++ {
			c := brow[x]
			if c.ch == 0 {
				continue // continuation: written together with its lead
			}
			cw := 1
			if c.ch >= 0x300 {
				if cw = runeWidth(c.ch); cw == 0 {
					c.ch, cw = ' ', 1
				}
			}
			dirty := full || c != frow[x]
			// a wide glyph is one unit on both sides: if either frame has a
			// two-column glyph here, its second column matters too
			if !dirty && x+1 < w && (cw == 2 || frow[x+1].ch == 0) {
				dirty = brow[x+1] != frow[x+1]
			}
			if dirty {
				e.fillGap(brow, x, y)
				e.put(x, y, c, cw, w)
			}
			if cw == 2 {
				x++
			}
		}
	}
	return len(e.buf) > start
}

// maxGap is the longest run of unchanged cells rewritten instead of jumping
// over them: a cursor move costs ~6 bytes, a plain cell one.
const maxGap = 4

// fillGapNarrow is fillGap for rows known to contain only single-column cells.
func (e *encoder) fillGapNarrow(row []cell, x, y int) {
	if e.cy != y || e.cx < 0 || x <= e.cx || x-e.cx > maxGap || !e.penOK {
		return
	}
	gap := row[e.cx:x]
	for _, c := range gap {
		if colorKey(e.mode, c.bg) != e.bg ||
			(c.ch != ' ' && colorKey(e.mode, c.fg) != e.fg) {
			return
		}
	}
	for _, c := range gap {
		if c.ch < utf8.RuneSelf {
			e.buf = append(e.buf, byte(c.ch))
		} else {
			e.buf = utf8.AppendRune(e.buf, c.ch)
		}
	}
	e.cx = x
}

// fillGap rewrites the few unchanged cells between the cursor and column x
// when that is cheaper than a cursor move, i.e. when they are narrow and
// already match the current pen.
func (e *encoder) fillGap(row []cell, x, y int) {
	if e.cy != y || e.cx < 0 || x <= e.cx || x-e.cx > maxGap || !e.penOK {
		return
	}
	gap := row[e.cx:x]
	for _, c := range gap {
		if c.ch == 0 || c.ch >= 0x300 || colorKey(e.mode, c.bg) != e.bg ||
			(c.ch != ' ' && colorKey(e.mode, c.fg) != e.fg) {
			return
		}
	}
	for _, c := range gap {
		if c.ch < utf8.RuneSelf {
			e.buf = append(e.buf, byte(c.ch))
		} else {
			e.buf = utf8.AppendRune(e.buf, c.ch)
		}
	}
	e.cx = x
}

// cellBytes views a row of cells as raw bytes for fast comparison. cell is
// three 4-byte fields with no padding, so equal bytes mean equal cells.
func cellBytes(row []cell) []byte {
	if len(row) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&row[0])), len(row)*int(unsafe.Sizeof(cell{})))
}
