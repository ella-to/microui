package renderer

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"

	"ella.to/microui"
)

// This file decodes terminal input bytes (keys, SGR mouse reports, bracketed
// paste) into microui input events.
//
// microui samples input once per frame: a key is either "pressed this frame"
// or not, and text typed this frame is applied in one go. Feeding a frame two
// presses of the same key, or text and a key whose order matters, would lose
// or reorder input. The parser therefore groups events into batches that one
// frame represents faithfully, and stops at the first event that would
// conflict; the caller runs another frame for the rest.

// maxPaste caps how much of an unterminated bracketed paste is buffered
// before it is delivered anyway.
const maxPaste = 1 << 20

// eventKind classifies a decoded input token for batching.
type eventKind int

const (
	evNone   eventKind = iota // consumed without producing input
	evText                    // typed or pasted text
	evKey                     // a key press
	evButton                  // a mouse button press or release
	evMotion                  // mouse movement
	evScroll                  // wheel movement (accumulates freely)
)

// inputParser holds state that spans parse calls.
type inputParser struct {
	escSince time.Time // when a still-incomplete escape sequence started waiting
}

// parseResult reports what parse consumed and what the driver should do.
type parseResult struct {
	n        int         // bytes consumed
	split    bool        // stopped at a batch boundary: more complete input pending
	escWait  bool        // an incomplete escape sequence is waiting for bytes
	quit     bool        // Ctrl-C
	suspend  bool        // Ctrl-Z
	redraw   bool        // Ctrl-L
	heldMods microui.Key // modifiers held down for a mouse click this frame
}

// batch tracks what the current frame already contains.
type batch struct {
	text, key, button bool
}

// conflicts reports whether an event of kind k must wait for the next frame.
func (b *batch) conflicts(k eventKind) bool {
	switch k {
	case evText:
		return b.key || b.button
	case evKey, evButton:
		return b.key || b.text || b.button
	case evMotion, evScroll:
		return b.button // both move the pointer: let a click register where it happened
	}
	return false
}

func (b *batch) add(k eventKind) {
	switch k {
	case evText:
		b.text = true
	case evKey:
		b.key = true
	case evButton:
		b.button = true
	}
}

// token is one decoded input event, applied to the context only once the
// batch accepts it.
type token struct {
	n    int // bytes it spans; 0 means incomplete
	kind eventKind
	// payload
	key    microui.Key
	mods   microui.Key
	text   string
	btn    microui.MouseButton
	press  bool
	x, y   int
	dx, dy int
	// control actions
	quit, suspend, redraw bool
}

// parse consumes as many complete tokens from buf as fit in one frame,
// feeding them to ctx. now drives the lone-ESC timeout.
func (p *inputParser) parse(ctx *microui.Context, buf []byte, now time.Time) parseResult {
	var res parseResult
	var b batch
	for res.n < len(buf) {
		tok := p.next(buf[res.n:], now)
		if tok.n == 0 {
			res.escWait = buf[res.n] == 0x1b
			return res
		}
		if b.conflicts(tok.kind) {
			res.split = true
			return res
		}
		b.add(tok.kind)
		res.n += tok.n
		switch {
		case tok.quit:
			res.quit = true
			return res
		case tok.suspend:
			res.suspend = true
			return res
		case tok.redraw:
			res.redraw = true
		}
		res.heldMods |= apply(ctx, tok)
	}
	return res
}

// apply feeds one token to ctx and returns any modifiers it left held.
func apply(ctx *microui.Context, t token) (held microui.Key) {
	switch t.kind {
	case evText:
		ctx.InputText(t.text)
	case evKey:
		// Terminals report no key releases, so modifiers are synthesized down
		// around the key and up again; the textbox reads them from
		// KeyPressed as well as KeyDown.
		ctx.InputKeyDown(t.mods | t.key)
		ctx.InputKeyUp(t.mods | t.key)
	case evButton:
		if t.press {
			// keep modifiers held through the frame so shift-click (exact
			// value entry on sliders) sees KeyShift in KeyDown
			if t.mods != 0 {
				ctx.InputKeyDown(t.mods)
				held = t.mods
			}
			ctx.InputMouseDown(t.x, t.y, t.btn)
		} else {
			ctx.InputMouseUp(t.x, t.y, t.btn)
		}
	case evMotion:
		ctx.InputMouseMove(t.x, t.y)
	case evScroll:
		ctx.InputMouseMove(t.x, t.y)
		ctx.InputScroll(t.dx, t.dy)
	}
	return held
}

// next decodes the token at the start of buf (non-empty).
func (p *inputParser) next(buf []byte, now time.Time) token {
	c := buf[0]
	switch c {
	case 0x1b:
		tok := p.escape(buf)
		if tok.n > 0 {
			p.escSince = time.Time{}
			return tok
		}
		// incomplete: give the rest of the sequence a moment to arrive, then
		// treat the ESC as the Escape key
		if p.escSince.IsZero() {
			p.escSince = now
		}
		if now.Sub(p.escSince) >= escTimeout && !bytes.HasPrefix(buf, pasteStartBytes) {
			p.escSince = time.Time{}
			return token{n: 1, kind: evKey, key: microui.KeyEscape}
		}
		return token{}
	case 0x03:
		return token{n: 1, quit: true}
	case 0x1a:
		return token{n: 1, suspend: true}
	case 0x0c:
		return token{n: 1, redraw: true}
	case 0x01: // Ctrl-A: select all in a focused textbox
		return token{n: 1, kind: evKey, key: microui.KeySelectAll}
	case '\t':
		return token{n: 1, kind: evKey, key: microui.KeyTab}
	case '\r', '\n':
		return token{n: 1, kind: evKey, key: microui.KeyReturn}
	case 0x7f, 0x08:
		return token{n: 1, kind: evKey, key: microui.KeyBackspace}
	}
	if c < 0x20 {
		return token{n: 1} // other control byte: ignore
	}
	// a run of printable text, stopping before control bytes and before an
	// incomplete trailing UTF-8 sequence
	j := 0
	for j < len(buf) && buf[j] >= 0x20 && buf[j] != 0x7f {
		j++
	}
	if j == len(buf) {
		for k := max(j-utf8.UTFMax+1, 0); k < j; k++ {
			if utf8.RuneStart(buf[k]) && !utf8.FullRune(buf[k:j]) {
				j = k
				break
			}
		}
		if j == 0 {
			return token{} // wait for the rest of the rune
		}
	}
	return token{n: j, kind: evText, text: string(buf[:j])}
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

var (
	pasteStartBytes = []byte(pasteStart)
	pasteEndBytes   = []byte(pasteEnd)
	semicolonBytes  = []byte{';'}
)

// escape decodes an escape sequence at the start of buf. n == 0 means it is
// incomplete.
func (p *inputParser) escape(buf []byte) token {
	if len(buf) < 2 {
		return token{}
	}
	switch buf[1] {
	case '[':
		return p.csi(buf)
	case 'O': // SS3: ESC O <final>, sent for arrows/Home/End in application mode
		if len(buf) < 3 {
			return token{}
		}
		if k := finalKey(buf[2]); k != 0 {
			return token{n: 3, kind: evKey, key: k}
		}
		return token{n: 3}
	case 0x1b: // ESC ESC: the first one is a real Escape key press
		return token{n: 1, kind: evKey, key: microui.KeyEscape}
	}
	// ESC + key is how terminals send Alt+key; report the Alt chord only
	return token{n: 2, kind: evKey, key: microui.KeyAlt}
}

// csi decodes "ESC [ params final".
func (p *inputParser) csi(buf []byte) token {
	if bytes.HasPrefix(buf, pasteStartBytes) {
		return paste(buf)
	}
	k := 2
	for k < len(buf) && (buf[k] < 0x40 || buf[k] > 0x7e) {
		k++
		if k > 64 {
			return token{n: 2} // runaway sequence: drop the introducer
		}
	}
	if k >= len(buf) {
		return token{}
	}
	params, final := buf[2:k], buf[k]
	n := k + 1

	if len(params) > 0 && params[0] == '<' && (final == 'M' || final == 'm') {
		tok := mouse(params[1:], final == 'M')
		tok.n = n
		return tok
	}

	// split "a;b" into the key number and the xterm modifier parameter
	first, mod, _ := bytes.Cut(params, semicolonBytes)
	mods := xtermMods(mod)
	key := finalKey(final)
	switch final {
	case 'Z': // Shift-Tab
		key, mods = microui.KeyTab, mods|microui.KeyShift
	case '~': // legacy "CSI n ~" keys
		if len(first) == 1 {
			switch first[0] {
			case '1', '7':
				key = microui.KeyHome
			case '3':
				key = microui.KeyDelete
			case '4', '8':
				key = microui.KeyEnd
			case '5':
				key = microui.KeyPageUp
			case '6':
				key = microui.KeyPageDown
			}
		}
	case 'u': // CSI u ("fixterms"/kitty) encoding of otherwise ambiguous keys
		return csiU(first, mods, n)
	}
	if key == 0 {
		return token{n: n} // unhandled (function keys, focus events, ...)
	}
	return token{n: n, kind: evKey, key: key, mods: mods}
}

// finalKey maps the final byte of a cursor-key sequence to a key.
func finalKey(b byte) microui.Key {
	switch b {
	case 'A':
		return microui.KeyUp
	case 'B':
		return microui.KeyDown
	case 'C':
		return microui.KeyRight
	case 'D':
		return microui.KeyLeft
	case 'H':
		return microui.KeyHome
	case 'F':
		return microui.KeyEnd
	}
	return 0
}

// xtermMods decodes an xterm modifier parameter (bitmask plus one).
func xtermMods(s []byte) microui.Key {
	m, ok := atoi(s)
	if !ok || m < 1 {
		return 0
	}
	m--
	var mods microui.Key
	if m&1 != 0 {
		mods |= microui.KeyShift
	}
	if m&2 != 0 {
		mods |= microui.KeyAlt
	}
	if m&4 != 0 {
		mods |= microui.KeyCtrl
	}
	return mods
}

// csiU decodes "CSI codepoint ; mods u".
func csiU(code []byte, mods microui.Key, n int) token {
	cp, ok := atoi(code)
	if !ok {
		return token{n: n}
	}
	key := microui.Key(0)
	switch cp {
	case 9:
		key = microui.KeyTab
	case 13:
		key = microui.KeyReturn
	case 27:
		key = microui.KeyEscape
	case 127:
		key = microui.KeyBackspace
	default:
		switch {
		case mods&microui.KeyCtrl != 0 && (cp == 'a' || cp == 'A'):
			key, mods = microui.KeySelectAll, 0
		case mods&(microui.KeyCtrl|microui.KeyAlt) == 0 && cp >= 0x20:
			return token{n: n, kind: evText, text: string(rune(cp))}
		default:
			return token{n: n}
		}
	}
	return token{n: n, kind: evKey, key: key, mods: mods}
}

// paste decodes a bracketed paste, delivering it as text. Newlines and tabs
// become spaces (the built-in textbox is single-line) and other control
// characters are dropped.
func paste(buf []byte) token {
	body := buf[len(pasteStart):]
	end := bytes.Index(body, pasteEndBytes)
	n := len(pasteStart) + end + len(pasteEnd)
	if end < 0 {
		if len(buf) < maxPaste {
			return token{} // wait for the terminator
		}
		end, n = len(body), len(buf)
	}
	s := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, strings.ReplaceAll(string(body[:end]), "\r\n", "\n"))
	if s == "" {
		return token{n: n}
	}
	return token{n: n, kind: evText, text: s}
}

// mouse decodes an SGR mouse report "cb;cx;cy".
func mouse(params []byte, press bool) token {
	a, rest, _ := bytes.Cut(params, semicolonBytes)
	b, c, _ := bytes.Cut(rest, semicolonBytes)
	cb, ok1 := atoi(a)
	cx, ok2 := atoi(b)
	cy, ok3 := atoi(c)
	if !ok1 || !ok2 || !ok3 {
		return token{}
	}
	t := token{x: cx - 1, y: cy - 1} // SGR coordinates are 1-based
	switch {
	case cb&64 != 0: // wheel
		t.kind = evScroll
		switch cb & 3 {
		case 0:
			t.dy = -3
		case 1:
			t.dy = 3
		case 2:
			t.dx = -3
		case 3:
			t.dx = 3
		}
	case cb&32 != 0: // motion, with or without a button held
		t.kind = evMotion
	case cb&128 != 0: // extra buttons: ignore
	default:
		switch cb & 3 {
		case 0:
			t.btn = microui.MouseLeft
		case 1:
			t.btn = microui.MouseMiddle
		case 2:
			t.btn = microui.MouseRight
		default:
			return t
		}
		t.kind, t.press = evButton, press
		if cb&4 != 0 {
			t.mods |= microui.KeyShift
		}
		if cb&8 != 0 {
			t.mods |= microui.KeyAlt
		}
		if cb&16 != 0 {
			t.mods |= microui.KeyCtrl
		}
	}
	return t
}

// atoi parses a small non-negative decimal without allocating.
func atoi(s []byte) (int, bool) {
	if len(s) == 0 || len(s) > 9 {
		return 0, false
	}
	n := 0
	for _, b := range s {
		if b < '0' || b > '9' {
			return 0, false
		}
		n = n*10 + int(b-'0')
	}
	return n, true
}
