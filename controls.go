package microui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// number formatting defaults, matching microui's MU_REAL_FMT / MU_SLIDER_FMT.
const (
	realFmt   = "%.3g"
	sliderFmt = "%.2f"
)

// ---- control helpers --------------------------------------------------------

// inHoverRoot reports whether the current control belongs to the hovered root
// container (so controls behind a window are not interactive).
func (c *Context) inHoverRoot() bool {
	for i := len(c.containerStack) - 1; i >= 0; i-- {
		if c.containerStack[i] == c.hoverRoot {
			return true
		}
		// only root containers have their head set; stop searching once we've
		// reached the current root container.
		if c.containerStack[i].head >= 0 {
			break
		}
	}
	return false
}

// DrawControlFrame draws a control's background frame, shifting the color id for
// hover/focus state unless OptNoFrame is set.
func (c *Context) DrawControlFrame(id ID, rect Rect, colorid ColorID, opt Option) {
	if opt.has(OptNoFrame) {
		return
	}
	switch {
	case c.focus == id:
		colorid += 2
	case c.hover == id:
		colorid++
	}
	c.DrawFrame(c, rect, colorid)
}

// DrawControlText draws str inside rect, aligned per opt (left by default,
// OptAlignCenter or OptAlignRight otherwise), clipped to rect.
func (c *Context) DrawControlText(str string, rect Rect, colorid ColorID, opt Option) {
	font := c.Style.Font
	tw := c.TextWidth(font, str)
	c.PushClipRect(rect)
	pos := Vec2{Y: rect.Y + (rect.H-c.TextHeight(font))/2}
	switch {
	case opt.has(OptAlignCenter):
		pos.X = rect.X + (rect.W-tw)/2
	case opt.has(OptAlignRight):
		pos.X = rect.X + rect.W - tw - c.Style.Padding
	default:
		pos.X = rect.X + c.Style.Padding
	}
	c.DrawText(font, str, pos, c.Style.Colors[colorid])
	c.PopClipRect()
}

// MouseOver reports whether the mouse is over rect, within the clip rectangle,
// and inside the hovered root container.
func (c *Context) MouseOver(rect Rect) bool {
	return rectOverlapsVec2(rect, c.mousePos) &&
		rectOverlapsVec2(c.GetClipRect(), c.mousePos) &&
		c.inHoverRoot()
}

// UpdateControl updates hover/focus state for the control identified by id
// occupying rect.
func (c *Context) UpdateControl(id ID, rect Rect, opt Option) {
	mouseover := c.MouseOver(rect)

	if c.focus == id {
		c.updatedFocus = true
	}
	if opt.has(OptNoInteract) {
		return
	}
	if mouseover && c.mouseDown == 0 {
		c.hover = id
	}

	if c.focus == id {
		if c.mousePressed != 0 && !mouseover {
			c.SetFocus(0)
		}
		if c.mouseDown == 0 && !opt.has(OptHoldFocus) {
			c.SetFocus(0)
		}
	}

	if c.hover == id {
		if c.mousePressed != 0 {
			c.SetFocus(id)
		} else if !mouseover {
			c.hover = 0
		}
	}
}

// ---- controls ---------------------------------------------------------------

// charAt returns the byte at index i, or 0 if i is out of range (mirroring a
// C NUL-terminated string).
func charAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// chSlice returns the single-byte substring at i, or "" at the end of s.
func chSlice(s string, i int) string {
	if i < len(s) {
		return s[i : i+1]
	}
	return ""
}

// Text draws word-wrapped text within the current layout column.
func (c *Context) Text(text string) {
	font := c.Style.Font
	color := c.Style.Colors[ColorText]
	c.LayoutBeginColumn()
	c.layoutRow(1, []int{-1}, c.TextHeight(font))
	s := text
	p := 0
	for {
		r := c.LayoutNext()
		w := 0
		start := p
		end := p
		for {
			word := p
			for charAt(s, p) != 0 && charAt(s, p) != ' ' && charAt(s, p) != '\n' {
				p++
			}
			w += c.TextWidth(font, s[word:p])
			if w > r.W && end != start {
				break
			}
			w += c.TextWidth(font, chSlice(s, p))
			end = p
			p++
			if charAt(s, end) == 0 || charAt(s, end) == '\n' {
				break
			}
		}
		c.DrawText(font, s[start:end], Vec2{X: r.X, Y: r.Y}, color)
		p = end + 1
		if charAt(s, end) == 0 {
			break
		}
	}
	c.LayoutEndColumn()
}

// Label draws a single-line, left-aligned text label in the next layout cell.
func (c *Context) Label(text string) {
	c.DrawControlText(text, c.LayoutNext(), ColorText, 0)
}

// Button draws a clickable, center-aligned button. It returns ResSubmit on the
// frame it is clicked.
func (c *Context) Button(label string) int {
	return c.ButtonEx(label, 0, OptAlignCenter)
}

// ButtonEx draws a button with an optional icon and options.
func (c *Context) ButtonEx(label string, icon int, opt Option) int {
	res := 0
	var id ID
	if label != "" {
		id = c.GetID([]byte(label))
	} else {
		id = c.idFromInt(icon)
	}
	r := c.LayoutNext()
	c.UpdateControl(id, r, opt)
	// handle click
	if c.mousePressed == MouseLeft && c.focus == id {
		res |= ResSubmit
	}
	// draw
	c.DrawControlFrame(id, r, ColorButton, opt)
	if label != "" {
		c.DrawControlText(label, r, ColorText, opt)
	}
	if icon != 0 {
		c.DrawIcon(icon, r, c.Style.Colors[ColorText])
	}
	return res
}

// Checkbox draws a labelled checkbox bound to state. It returns ResChange on
// the frame its value toggles.
func (c *Context) Checkbox(label string, state *bool) int {
	res := 0
	id := c.idFromPtr(unsafe.Pointer(state))
	r := c.LayoutNext()
	box := Rect{X: r.X, Y: r.Y, W: r.H, H: r.H}
	c.UpdateControl(id, r, 0)
	// handle click
	if c.mousePressed == MouseLeft && c.focus == id {
		res |= ResChange
		*state = !*state
	}
	// draw
	c.DrawControlFrame(id, box, ColorBase, 0)
	if *state {
		c.DrawIcon(IconCheck, box, c.Style.Colors[ColorText])
	}
	r = Rect{X: r.X + box.W, Y: r.Y, W: r.W - box.W, H: r.H}
	c.DrawControlText(label, r, ColorText, 0)
	return res
}

// now is the textbox's clock, indirect so tests can control double-click
// timing.
var now = time.Now

// Double-click detection: two left presses within doubleClickWindow landing
// at most doubleClickSlop text units apart select the word under the caret.
const (
	doubleClickWindow = 400 * time.Millisecond
	doubleClickSlop   = 5
)

func (e *textEdit) hasSel() bool { return e.cursor != e.anchor }
func (e *textEdit) selMin() int  { return min(e.cursor, e.anchor) }
func (e *textEdit) selMax() int  { return max(e.cursor, e.anchor) }

func (e *textEdit) clampTo(n int) {
	e.cursor = clampI(e.cursor, 0, n)
	e.anchor = clampI(e.anchor, 0, n)
}

// replaceSel replaces the selected range (or splices at the caret when
// nothing is selected) with ins, leaving the caret after the insertion.
func (e *textEdit) replaceSel(runes, ins []rune) []rune {
	lo, hi := e.selMin(), e.selMax()
	out := make([]rune, 0, len(runes)-(hi-lo)+len(ins))
	out = append(out, runes[:lo]...)
	out = append(out, ins...)
	out = append(out, runes[hi:]...)
	e.cursor = lo + len(ins)
	e.anchor = e.cursor
	return out
}

// textDisplay returns what a textbox draws: the value itself, or one
// PasswordMask rune per rune when OptPassword is set. Editing indices are
// interchangeable between the two because the mapping is rune-for-rune.
func textDisplay(s string, opt Option) string {
	if !opt.has(OptPassword) {
		return s
	}
	return strings.Repeat(string(PasswordMask), utf8.RuneCountInString(s))
}

// textIndexAt returns the rune index in disp whose caret position is nearest
// to x, given the text's left edge textx.
func (c *Context) textIndexAt(disp []rune, font Font, textx, x int) int {
	best, bestDist := 0, int(^uint(0)>>1)
	for i := 0; i <= len(disp); i++ {
		d := textx + c.TextWidth(font, string(disp[:i])) - x
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// wordBoundsAt returns the boundaries of the word containing index i: a run
// of non-space runes, or the run of spaces i falls in.
func wordBoundsAt(disp []rune, i int) (lo, hi int) {
	if len(disp) == 0 {
		return 0, 0
	}
	if i >= len(disp) {
		i = len(disp) - 1
	}
	space := unicode.IsSpace(disp[i])
	lo, hi = i, i+1
	for lo > 0 && unicode.IsSpace(disp[lo-1]) == space {
		lo--
	}
	for hi < len(disp) && unicode.IsSpace(disp[hi]) == space {
		hi++
	}
	return lo, hi
}

// TextboxRaw is the low-level text box: it edits buf for the control id at rect.
// Unlike microui there is no fixed buffer size; the string grows as typed.
//
// While focused it supports full caret editing: arrows (with Shift extending
// the selection and Ctrl jumping to the ends), Home/End, Delete/Backspace,
// KeySelectAll, click to place the caret, drag to select, and double-click to
// select a word. Long values scroll horizontally to keep the caret visible.
// Backends that only report the classic key bits keep the old append/erase
// behavior, since the caret then simply stays at the end.
func (c *Context) TextboxRaw(buf *string, id ID, r Rect, opt Option) int {
	res := 0
	c.UpdateControl(id, r, opt|OptHoldFocus)

	font := c.Style.Font
	ed := &c.textEdit

	if c.focus == id {
		runes := []rune(*buf)
		if ed.id != id {
			// gaining focus: caret at the end, nothing selected
			*ed = textEdit{id: id, cursor: len(runes), anchor: len(runes)}
		}
		ed.clampTo(len(runes))

		disp := []rune(textDisplay(*buf, opt))
		textx := r.X + c.Style.Padding - ed.scroll

		// mouse: click to place the caret, drag to select, double-click for
		// the word under the caret
		if c.mousePressed&MouseLeft != 0 && c.hover == id {
			i := c.textIndexAt(disp, font, textx, c.mousePos.X)
			t := now()
			dx := c.mousePos.X - ed.lastClickX
			if dx < 0 {
				dx = -dx
			}
			if t.Sub(ed.lastClick) < doubleClickWindow && dx <= doubleClickSlop {
				ed.anchor, ed.cursor = wordBoundsAt(disp, i)
			} else {
				ed.cursor, ed.anchor = i, i
				ed.drag = true
			}
			ed.lastClick, ed.lastClickX = t, c.mousePos.X
		}
		if ed.drag {
			if c.mouseDown&MouseLeft != 0 {
				ed.cursor = c.textIndexAt(disp, font, textx, c.mousePos.X)
			} else {
				ed.drag = false
			}
		}

		// keyboard: modifiers may arrive either held (keyDown) or synthesized
		// around a key event (keyPressed), e.g. by the terminal backend
		mods := c.keyDown | c.keyPressed
		shift := mods&KeyShift != 0
		ctrl := mods&KeyCtrl != 0
		k := c.keyPressed

		// moveTo places the caret, extending the selection while Shift is
		// held and collapsing it otherwise.
		moveTo := func(i int) {
			ed.cursor = clampI(i, 0, len(runes))
			if !shift {
				ed.anchor = ed.cursor
			}
		}
		switch {
		case k&KeyLeft != 0:
			switch {
			case ctrl:
				moveTo(0)
			case ed.hasSel() && !shift:
				ed.cursor = ed.selMin()
				ed.anchor = ed.cursor
			default:
				moveTo(ed.cursor - 1)
			}
		case k&KeyRight != 0:
			switch {
			case ctrl:
				moveTo(len(runes))
			case ed.hasSel() && !shift:
				ed.cursor = ed.selMax()
				ed.anchor = ed.cursor
			default:
				moveTo(ed.cursor + 1)
			}
		case k&KeyHome != 0:
			moveTo(0)
		case k&KeyEnd != 0:
			moveTo(len(runes))
		}
		if k&KeySelectAll != 0 {
			ed.anchor, ed.cursor = 0, len(runes)
		}

		// handle text input
		if c.inputText != "" {
			runes = ed.replaceSel(runes, []rune(c.inputText))
			res |= ResChange
		}
		// handle backspace / delete (the selection if any, one rune otherwise)
		if k&KeyBackspace != 0 {
			if !ed.hasSel() && ed.cursor > 0 {
				ed.anchor = ed.cursor - 1
			}
			if ed.hasSel() {
				runes = ed.replaceSel(runes, nil)
				res |= ResChange
			}
		}
		if k&KeyDelete != 0 {
			if !ed.hasSel() && ed.cursor < len(runes) {
				ed.anchor = ed.cursor + 1
			}
			if ed.hasSel() {
				runes = ed.replaceSel(runes, nil)
				res |= ResChange
			}
		}
		// handle return
		if k&KeyReturn != 0 {
			c.SetFocus(0)
			res |= ResSubmit
		}
		*buf = string(runes)
	} else if ed.id == id {
		ed.id = 0 // lost focus: forget the editing state
	}

	// draw
	c.DrawControlFrame(id, r, ColorBase, opt)
	display := textDisplay(*buf, opt)
	if c.focus == id {
		dispRunes := []rune(display)
		color := c.Style.Colors[ColorText]
		texth := c.TextHeight(font)
		texty := r.Y + (r.H-texth)/2
		pad := c.Style.Padding

		// scroll to keep the caret inside the visible span
		cw := c.TextWidth(font, string(dispRunes[:ed.cursor]))
		inner := max(r.W-pad*2-1, 1)
		if cw-ed.scroll > inner {
			ed.scroll = cw - inner
		}
		if cw-ed.scroll < 0 {
			ed.scroll = cw
		}
		textx := r.X + pad - ed.scroll

		c.PushClipRect(r)
		if ed.hasSel() {
			x0 := textx + c.TextWidth(font, string(dispRunes[:ed.selMin()]))
			x1 := textx + c.TextWidth(font, string(dispRunes[:ed.selMax()]))
			c.DrawRect(Rect{X: x0, Y: texty, W: x1 - x0, H: texth}, c.Style.Colors[ColorSelection])
		}
		c.DrawText(font, display, Vec2{X: textx, Y: texty}, color)
		c.DrawRect(Rect{X: textx + cw, Y: texty, W: 1, H: texth}, color)
		c.PopClipRect()
	} else {
		c.DrawControlText(display, r, ColorText, opt)
	}

	return res
}

// Textbox draws an editable single-line text box bound to buf.
func (c *Context) Textbox(buf *string) int {
	return c.TextboxEx(buf, 0)
}

// TextboxPassword draws a textbox that shows PasswordMask in place of each
// rune while editing buf normally.
func (c *Context) TextboxPassword(buf *string) int {
	return c.TextboxEx(buf, OptPassword)
}

// TextboxEx draws an editable text box with options.
func (c *Context) TextboxEx(buf *string, opt Option) int {
	id := c.idFromPtr(unsafe.Pointer(buf))
	r := c.LayoutNext()
	return c.TextboxRaw(buf, id, r, opt)
}

// numberTextbox implements shift+click numeric text entry for sliders/numbers.
// It returns true while the value is being edited as text.
func (c *Context) numberTextbox(value *Real, r Rect, id ID) bool {
	if c.mousePressed == MouseLeft && c.keyDown&KeyShift != 0 && c.hover == id {
		c.numberEdit = id
		c.numberEditBuf = fmt.Sprintf(realFmt, *value)
	}
	if c.numberEdit == id {
		res := c.TextboxRaw(&c.numberEditBuf, id, r, 0)
		if res&ResSubmit != 0 || c.focus != id {
			*value = parseFloatPrefix(c.numberEditBuf)
			c.numberEdit = 0
		} else {
			return true
		}
	}
	return false
}

// Slider draws a horizontal slider over [lo, hi] bound to value.
func (c *Context) Slider(value *Real, lo, hi Real) int {
	return c.SliderEx(value, lo, hi, 0, sliderFmt, OptAlignCenter)
}

// SliderEx draws a slider with a step, value format and options.
func (c *Context) SliderEx(value *Real, low, high, step Real, format string, opt Option) int {
	res := 0
	last := *value
	v := last
	id := c.idFromPtr(unsafe.Pointer(value))
	base := c.LayoutNext()

	// handle text input mode
	if c.numberTextbox(&v, base, id) {
		return res
	}

	// handle normal mode
	c.UpdateControl(id, base, opt)

	// handle input
	if c.focus == id && (c.mouseDown|c.mousePressed) == MouseLeft {
		v = low + Real(c.mousePos.X-base.X)*(high-low)/Real(base.W)
		if step != 0 {
			v = Real(int64((v+step/2)/step)) * step
		}
	}
	// clamp and store value, update res
	v = clampF(v, low, high)
	*value = v
	if last != v {
		res |= ResChange
	}

	// draw base
	c.DrawControlFrame(id, base, ColorBase, opt)
	// draw thumb
	w := c.Style.ThumbSize
	x := int((v - low) * Real(base.W-w) / (high - low))
	thumb := Rect{X: base.X + x, Y: base.Y, W: w, H: base.H}
	c.DrawControlFrame(id, thumb, ColorButton, opt)
	// draw text
	c.DrawControlText(fmt.Sprintf(format, v), base, ColorText, opt)

	return res
}

// Number draws a draggable numeric field bound to value, changing by step per
// pixel of horizontal drag.
func (c *Context) Number(value *Real, step Real) int {
	return c.NumberEx(value, step, sliderFmt, OptAlignCenter)
}

// NumberEx draws a numeric field with a value format and options.
func (c *Context) NumberEx(value *Real, step Real, format string, opt Option) int {
	res := 0
	id := c.idFromPtr(unsafe.Pointer(value))
	base := c.LayoutNext()
	last := *value

	// handle text input mode
	if c.numberTextbox(value, base, id) {
		return res
	}

	// handle normal mode
	c.UpdateControl(id, base, opt)

	// handle input
	if c.focus == id && c.mouseDown == MouseLeft {
		*value += Real(c.mouseDelta.X) * step
	}
	// set flag if value changed
	if *value != last {
		res |= ResChange
	}

	// draw base
	c.DrawControlFrame(id, base, ColorBase, opt)
	// draw text
	c.DrawControlText(fmt.Sprintf(format, *value), base, ColorText, opt)

	return res
}

// header implements both Header (istreenode=false) and BeginTreenode
// (istreenode=true). It returns ResActive when expanded.
func (c *Context) header(label string, istreenode bool, opt Option) int {
	id := c.GetID([]byte(label))
	idx := poolGet(c.treenodePool[:], id)
	c.layoutRow(1, []int{-1}, 0)

	active := idx >= 0
	expanded := active
	if opt.has(OptExpanded) {
		expanded = !active
	}
	r := c.LayoutNext()
	c.UpdateControl(id, r, 0)

	// handle click
	if c.mousePressed == MouseLeft && c.focus == id {
		active = !active
	}

	// update pool ref
	if idx >= 0 {
		if active {
			c.poolUpdate(c.treenodePool[:], idx)
		} else {
			c.treenodePool[idx] = poolItem{}
		}
	} else if active {
		c.poolInit(c.treenodePool[:], id)
	}

	// draw
	if istreenode {
		if c.hover == id {
			c.DrawFrame(c, r, ColorButtonHover)
		}
	} else {
		c.DrawControlFrame(id, r, ColorButton, 0)
	}
	icon := IconCollapsed
	if expanded {
		icon = IconExpanded
	}
	c.DrawIcon(icon, Rect{X: r.X, Y: r.Y, W: r.H, H: r.H}, c.Style.Colors[ColorText])
	r.X += r.H - c.Style.Padding
	r.W -= r.H - c.Style.Padding
	c.DrawControlText(label, r, ColorText, 0)

	if expanded {
		return ResActive
	}
	return 0
}

// Header draws a collapsible header. It returns ResActive while expanded.
func (c *Context) Header(label string) int {
	return c.HeaderEx(label, 0)
}

// HeaderEx draws a collapsible header with options (e.g. OptExpanded).
func (c *Context) HeaderEx(label string, opt Option) int {
	return c.header(label, false, opt)
}

// BeginTreenode starts a collapsible, indented tree node. When it returns
// ResActive, emit child controls and call EndTreenode.
func (c *Context) BeginTreenode(label string) int {
	return c.BeginTreenodeEx(label, 0)
}

// BeginTreenodeEx starts a tree node with options.
func (c *Context) BeginTreenodeEx(label string, opt Option) int {
	res := c.header(label, true, opt)
	if res&ResActive != 0 {
		c.getLayout().indent += c.Style.Indent
		c.idStack = append(c.idStack, c.lastID)
	}
	return res
}

// EndTreenode closes a tree node opened with BeginTreenode.
func (c *Context) EndTreenode() {
	c.getLayout().indent -= c.Style.Indent
	c.PopID()
}

// parseFloatPrefix parses the leading floating-point number in s, like C's
// strtod, returning 0 if there is none.
func parseFloatPrefix(s string) Real {
	s = strings.TrimSpace(s)
	n := len(s)
	i := 0
	if i < n && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < n && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < n && s[i] == '.' {
		i++
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < n && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < n && s[j] >= '0' && s[j] <= '9' {
			i = j
			for i < n && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
	}
	f, err := strconv.ParseFloat(s[:i], 32)
	if err != nil {
		return 0
	}
	return Real(f)
}
