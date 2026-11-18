package renderer

import (
	"strconv"
	"strings"

	miniui "ella.to/microui"
)

// This file decodes terminal input bytes (keyboard + SGR mouse reports) into
// miniui input events.

// parseInput consumes as many complete input tokens from buf as possible,
// feeding them to ctx, and returns the number of bytes consumed. Any trailing
// incomplete escape sequence is left for the next call. It sets *quit on Ctrl-C.
func parseInput(ctx *miniui.Context, buf []byte, quit *bool) int {
	i := 0
	for i < len(buf) {
		c := buf[i]
		switch {
		case c == 0x1b: // ESC: control sequence
			n, ok := parseEscape(ctx, buf[i:])
			if !ok {
				return i // incomplete sequence; keep it pending
			}
			i += n

		case c == 0x03: // Ctrl-C
			*quit = true
			i++

		case c == 0x01: // Ctrl-A: select all in a focused textbox
			ctx.InputKeyDown(miniui.KeySelectAll)
			ctx.InputKeyUp(miniui.KeySelectAll)
			i++

		case c == '\r' || c == '\n': // Enter
			ctx.InputKeyDown(miniui.KeyReturn)
			ctx.InputKeyUp(miniui.KeyReturn)
			i++

		case c == 0x7f || c == 0x08: // Backspace / Delete
			ctx.InputKeyDown(miniui.KeyBackspace)
			ctx.InputKeyUp(miniui.KeyBackspace)
			i++

		case c >= 0x20: // printable text (including UTF-8 multibyte runs)
			j := i
			for j < len(buf) && buf[j] >= 0x20 && buf[j] != 0x7f && buf[j] != 0x1b {
				j++
			}
			ctx.InputText(string(buf[i:j]))
			i = j

		default: // other control byte: ignore
			i++
		}
	}
	return i
}

// parseEscape parses a single escape sequence at the start of buf. It returns
// the number of bytes consumed and whether a complete sequence was found.
func parseEscape(ctx *miniui.Context, buf []byte) (int, bool) {
	if len(buf) < 2 {
		return 0, false
	}
	if buf[1] != '[' {
		// lone ESC or ESC-prefixed key we don't handle; consume the ESC only.
		return 1, true
	}
	// CSI sequence.
	if len(buf) >= 3 && buf[2] == '<' {
		// SGR mouse: ESC [ < params (M|m)
		k := 3
		for k < len(buf) && buf[k] != 'M' && buf[k] != 'm' {
			k++
		}
		if k >= len(buf) {
			return 0, false // wait for the terminating M/m
		}
		parseMouse(ctx, string(buf[3:k]), buf[k] == 'M')
		return k + 1, true
	}
	// other CSI sequence: consume up to the final byte (0x40..0x7e).
	k := 2
	for k < len(buf) && !(buf[k] >= 0x40 && buf[k] <= 0x7e) {
		k++
	}
	if k >= len(buf) {
		return 0, false
	}
	parseCSIKey(ctx, string(buf[2:k]), buf[k])
	return k + 1, true
}

// parseCSIKey translates a non-mouse CSI sequence (parameter bytes + final
// byte) into key events: arrows, Home/End and Delete, honoring xterm-style
// modifier parameters ("1;2"=Shift, "1;5"=Ctrl, "1;6"=both). Terminals report
// no key releases, so modifiers are synthesized down around the key and up
// again; the textbox reads them from keyPressed as well as keyDown.
func parseCSIKey(ctx *miniui.Context, params string, final byte) {
	key := 0
	switch final {
	case 'C':
		key = miniui.KeyRight
	case 'D':
		key = miniui.KeyLeft
	case 'H':
		key = miniui.KeyHome
	case 'F':
		key = miniui.KeyEnd
	case '~': // legacy "CSI n ~" keys
		num := params
		if i := strings.IndexByte(num, ';'); i >= 0 {
			num = num[:i]
		}
		switch num {
		case "1", "7":
			key = miniui.KeyHome
		case "3":
			key = miniui.KeyDelete
		case "4", "8":
			key = miniui.KeyEnd
		}
	}
	if key == 0 {
		return // unhandled sequence (function keys, arrows up/down, ...)
	}

	mods := 0
	if i := strings.IndexByte(params, ';'); i >= 0 {
		if m, err := strconv.Atoi(params[i+1:]); err == nil {
			m-- // xterm encodes the modifier bitmask plus one
			if m&1 != 0 {
				mods |= miniui.KeyShift
			}
			if m&2 != 0 {
				mods |= miniui.KeyAlt
			}
			if m&4 != 0 {
				mods |= miniui.KeyCtrl
			}
		}
	}
	if mods != 0 {
		ctx.InputKeyDown(mods)
	}
	ctx.InputKeyDown(key)
	ctx.InputKeyUp(key)
	if mods != 0 {
		ctx.InputKeyUp(mods)
	}
}

// parseMouse decodes an SGR mouse report ("cb;cx;cy") and feeds it to ctx.
func parseMouse(ctx *miniui.Context, params string, press bool) {
	cb, cx, cy, ok := threeInts(params)
	if !ok {
		return
	}
	x, y := cx-1, cy-1 // SGR coordinates are 1-based

	switch {
	case cb&64 != 0: // scroll wheel
		switch cb {
		case 64: // wheel up
			ctx.InputScroll(0, -3)
		case 65: // wheel down
			ctx.InputScroll(0, 3)
		}
	case cb&32 != 0: // motion (with or without a button held)
		ctx.InputMouseMove(x, y)
	default: // button press / release
		btn := 0
		switch cb & 3 {
		case 0:
			btn = miniui.MouseLeft
		case 1:
			btn = miniui.MouseMiddle
		case 2:
			btn = miniui.MouseRight
		}
		if btn == 0 {
			return
		}
		if press {
			ctx.InputMouseDown(x, y, btn)
		} else {
			ctx.InputMouseUp(x, y, btn)
		}
	}
}

// threeInts parses "a;b;c" into three ints.
func threeInts(s string) (a, b, c int, ok bool) {
	parts := strings.Split(s, ";")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if a, err = strconv.Atoi(parts[0]); err != nil {
		return
	}
	if b, err = strconv.Atoi(parts[1]); err != nil {
		return
	}
	if c, err = strconv.Atoi(parts[2]); err != nil {
		return
	}
	return a, b, c, true
}
