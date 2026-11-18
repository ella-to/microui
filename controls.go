package microui

import (
	"fmt"
	"math"
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
	intFmt    = "%d"
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
// hover/focus state unless OptNoFrame is set. colorid must be one of the base
// slots that has Hover/Focus variants after it (ColorButton, ColorBase).
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
	return rect.Contains(c.mousePos) &&
		c.GetClipRect().Contains(c.mousePos) &&
		c.inHoverRoot()
}

// UpdateControl updates hover/focus state for the control identified by id
// occupying rect. Call it once per frame for every custom control, before
// reading Hover/Focus for that control.
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

// clicked reports whether id was clicked with the left button this frame.
func (c *Context) clicked(id ID) bool {
	return c.mousePressed == MouseLeft && c.focus == id
}

// ---- controls ---------------------------------------------------------------

// Text draws word-wrapped text within the current layout column. Explicit
// newlines start a new line.
func (c *Context) Text(text string) {
	font := c.Style.Font
	color := c.Style.Colors[ColorText]
	c.LayoutBeginColumn()
	c.layoutRow(1, []int{-1}, c.TextHeight(font))
	for line := range strings.SplitSeq(text, "\n") {
		c.wrapLine(font, line, color)
	}
	c.LayoutEndColumn()
}

// wrapLine lays out one newline-free line, breaking at spaces so each row fits
// the column width. A single word wider than the column gets its own row.
func (c *Context) wrapLine(font Font, line string, color Color) {
	r := c.LayoutNext()
	start, end, w := 0, 0, 0 // current row is line[start:end], w wide
	spaceW := c.TextWidth(font, " ")
	p := 0
	for {
		// find the next word line[p:q]
		q := p
		for q < len(line) && line[q] != ' ' {
			q++
		}
		ww := c.TextWidth(font, line[p:q])
		if end > start && w+ww > r.W {
			// the word does not fit: flush the row and start a new one at it
			c.DrawText(font, line[start:end], Vec2{X: r.X, Y: r.Y}, color)
			r = c.LayoutNext()
			start, w = p, 0
		}
		w += ww
		end = q
		if q >= len(line) {
			break
		}
		w += spaceW
		p = q + 1
	}
	if end > start {
		c.DrawText(font, line[start:end], Vec2{X: r.X, Y: r.Y}, color)
	}
}

// Label draws a single-line, left-aligned text label in the next layout cell.
func (c *Context) Label(text string) {
	c.DrawControlText(text, c.LayoutNext(), ColorText, 0)
}

// Button draws a clickable, center-aligned button. The result is Submitted
// on the frame it is clicked. The label doubles as the button's identity:
// scope repeated labels with PushID.
func (c *Context) Button(label string) Result {
	return c.ButtonEx(label, 0, OptAlignCenter)
}

// ButtonEx draws a button with an optional icon and options.
func (c *Context) ButtonEx(label string, icon Icon, opt Option) Result {
	var res Result
	var id ID
	if label != "" {
		id = c.getIDString(label)
	} else {
		id = c.idFromIcon(icon)
	}
	r := c.LayoutNext()
	c.UpdateControl(id, r, opt)
	if c.clicked(id) {
		res |= ResSubmit
	}
	c.DrawControlFrame(id, r, ColorButton, opt)
	if label != "" {
		c.DrawControlText(label, r, ColorText, opt)
	}
	if icon != 0 {
		c.DrawIcon(icon, r, c.Style.Colors[ColorText])
	}
	return res
}

// Checkbox draws a labelled checkbox bound to state. The result is Changed on
// the frame its value toggles. state must persist across frames: its address
// is the control's identity.
func (c *Context) Checkbox(label string, state *bool) Result {
	var res Result
	id := c.idFromPtr(unsafe.Pointer(state))
	r := c.LayoutNext()
	box := Rect{X: r.X, Y: r.Y, W: r.H, H: r.H}
	c.UpdateControl(id, r, 0)
	if c.clicked(id) {
		res |= ResChange
		*state = !*state
	}
	c.DrawControlFrame(id, box, ColorBase, 0)
	if *state {
		c.DrawIcon(IconCheck, box, c.Style.Colors[ColorText])
	}
	r = Rect{X: r.X + box.W, Y: r.Y, W: r.W - box.W, H: r.H}
	c.DrawControlText(label, r, ColorText, 0)
	return res
}

// ---- textbox ------------------------------------------------------------------

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

// replaceSel replaces the selected rune range of s (or splices at the caret
// when nothing is selected) with ins, leaving the caret after the insertion.
func (e *textEdit) replaceSel(s, ins string) string {
	lo, hi := e.selMin(), e.selMax()
	out := s[:runeOffset(s, lo)] + ins + s[runeOffset(s, hi):]
	e.cursor = lo + utf8.RuneCountInString(ins)
	e.anchor = e.cursor
	return out
}

// runeOffset returns the byte offset of the i-th rune of s (len(s) when i is
// past the end). Slicing a string at it does not allocate.
func runeOffset(s string, i int) int {
	if i <= 0 {
		return 0
	}
	for bi := range s {
		if i == 0 {
			return bi
		}
		i--
	}
	return len(s)
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
func (c *Context) textIndexAt(disp string, font Font, textx, x int) int {
	best, bestDist := 0, math.MaxInt
	i := 0
	check := func(prefix string) {
		d := textx + c.TextWidth(font, prefix) - x
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = i, d
		}
		i++
	}
	for bi := range disp {
		check(disp[:bi])
	}
	check(disp)
	return best
}

// wordBoundsAt returns the rune boundaries of the word containing rune index
// i: a run of non-space runes, or the run of spaces i falls in.
func wordBoundsAt(s string, i int) (lo, hi int) {
	disp := []rune(s)
	if len(disp) == 0 {
		return 0, 0
	}
	i = min(i, len(disp)-1)
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

// TextboxRaw is the low-level text box: it edits buf for the control id at
// rect r. Unlike microui there is no fixed buffer size; the string grows as
// typed.
//
// While focused it supports full caret editing: arrows (with Shift extending
// the selection and Ctrl jumping to the ends), Home/End, Delete/Backspace,
// KeySelectAll, click to place the caret, drag to select, and double-click to
// select a word. Long values scroll horizontally to keep the caret visible.
// Backends that only report the classic key bits keep the old append/erase
// behavior, since the caret then simply stays at the end.
func (c *Context) TextboxRaw(buf *string, id ID, r Rect, opt Option) Result {
	var res Result
	c.UpdateControl(id, r, opt|OptHoldFocus)

	font := c.Style.Font
	ed := &c.textEdit

	if c.focus == id {
		s := *buf
		n := utf8.RuneCountInString(s)
		if ed.id != id {
			// gaining focus: caret at the end, nothing selected
			*ed = textEdit{id: id, cursor: n, anchor: n}
		}
		ed.clampTo(n)

		disp := textDisplay(s, opt)
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
			ed.cursor = clampI(i, 0, n)
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
				moveTo(n)
			case ed.hasSel() && !shift:
				ed.cursor = ed.selMax()
				ed.anchor = ed.cursor
			default:
				moveTo(ed.cursor + 1)
			}
		case k&KeyHome != 0:
			moveTo(0)
		case k&KeyEnd != 0:
			moveTo(n)
		}
		if k&KeySelectAll != 0 {
			ed.anchor, ed.cursor = 0, n
		}

		// handle text input
		if c.inputText != "" {
			s = ed.replaceSel(s, c.inputText)
			n = utf8.RuneCountInString(s)
			res |= ResChange
		}
		// handle backspace / delete (the selection if any, one rune otherwise)
		if k&KeyBackspace != 0 {
			if !ed.hasSel() && ed.cursor > 0 {
				ed.anchor = ed.cursor - 1
			}
			if ed.hasSel() {
				s = ed.replaceSel(s, "")
				n = utf8.RuneCountInString(s)
				res |= ResChange
			}
		}
		if k&KeyDelete != 0 {
			if !ed.hasSel() && ed.cursor < n {
				ed.anchor = ed.cursor + 1
			}
			if ed.hasSel() {
				s = ed.replaceSel(s, "")
				res |= ResChange
			}
		}
		// handle return
		if k&KeyReturn != 0 {
			c.SetFocus(0)
			res |= ResSubmit
		}
		if res.Changed() {
			*buf = s
		}
	} else if ed.id == id {
		ed.id = 0 // lost focus: forget the editing state
	}

	// draw
	c.DrawControlFrame(id, r, ColorBase, opt)
	display := textDisplay(*buf, opt)
	if c.focus != id {
		c.DrawControlText(display, r, ColorText, opt)
		return res
	}

	color := c.Style.Colors[ColorText]
	texth := c.TextHeight(font)
	texty := r.Y + (r.H-texth)/2
	pad := c.Style.Padding

	// scroll to keep the caret inside the visible span
	cw := c.TextWidth(font, display[:runeOffset(display, ed.cursor)])
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
		x0 := textx + c.TextWidth(font, display[:runeOffset(display, ed.selMin())])
		x1 := textx + c.TextWidth(font, display[:runeOffset(display, ed.selMax())])
		c.DrawRect(Rect{X: x0, Y: texty, W: x1 - x0, H: texth}, c.Style.Colors[ColorSelection])
	}
	c.DrawText(font, display, Vec2{X: textx, Y: texty}, color)
	c.DrawRect(Rect{X: textx + cw, Y: texty, W: 1, H: texth}, color)
	c.PopClipRect()
	return res
}

// Textbox draws an editable single-line text box bound to buf. The result is
// Changed when the text was edited and Submitted when Enter was pressed. buf
// must persist across frames: its address is the control's identity.
func (c *Context) Textbox(buf *string) Result {
	return c.TextboxEx(buf, 0)
}

// TextboxPassword draws a textbox that shows PasswordMask in place of each
// rune while editing buf normally.
func (c *Context) TextboxPassword(buf *string) Result {
	return c.TextboxEx(buf, OptPassword)
}

// TextboxEx draws an editable text box with options.
func (c *Context) TextboxEx(buf *string, opt Option) Result {
	id := c.idFromPtr(unsafe.Pointer(buf))
	r := c.LayoutNext()
	return c.TextboxRaw(buf, id, r, opt)
}

// ---- numeric controls -------------------------------------------------------

// isInteger reports whether T is an integer type.
func isInteger[T Number]() bool {
	half := 0.5
	return T(half) == 0
}

// isUnsigned reports whether T is an unsigned integer type.
func isUnsigned[T Number]() bool {
	var zero T
	return zero-1 > zero
}

// fromFloat converts v to T, rounding for integer types and clamping at zero
// for unsigned ones.
func fromFloat[T Number](v float64) T {
	if isInteger[T]() {
		v = math.Round(v)
		if isUnsigned[T]() && v < 0 {
			v = 0
		}
	}
	return T(v)
}

// defaultFormat returns the value format used when the caller passes "".
func defaultFormat[T Number]() string {
	if isInteger[T]() {
		return intFmt
	}
	return sliderFmt
}

// numberTextbox implements shift+click numeric text entry for sliders/numbers.
// It returns true while the value is being edited as text.
func (c *Context) numberTextbox(value *float64, r Rect, id ID) bool {
	if c.mousePressed == MouseLeft && c.keyDown&KeyShift != 0 && c.hover == id {
		c.numberEdit = id
		c.numberEditBuf = fmt.Sprintf(realFmt, *value)
	}
	if c.numberEdit == id {
		res := c.TextboxRaw(&c.numberEditBuf, id, r, 0)
		if !res.Submitted() && c.focus == id {
			return true
		}
		*value = parseFloatPrefix(c.numberEditBuf)
		c.numberEdit = 0
	}
	return false
}

// Slider draws a horizontal slider over [lo, hi] bound to value, which may be
// any numeric type: integer sliders snap to whole numbers. The result is
// Changed while the value changes. Shift-click it to type an exact value.
//
//	var volume float64 = 0.5
//	var level int = 3
//	ctx.Slider(&volume, 0, 1)
//	ctx.Slider(&level, 1, 10)
func (c *Context) Slider[T Number](value *T, lo, hi T) Result {
	return c.SliderEx(value, lo, hi, 0, "", OptAlignCenter)
}

// SliderEx draws a slider with a step (0 for continuous; integer types
// default to 1), a fmt value format ("" picks %d or %.2f by type) and
// options.
func (c *Context) SliderEx[T Number](value *T, low, high, step T, format string, opt Option) Result {
	var res Result
	last := *value
	v := float64(last)
	lo, hi, st := float64(low), float64(high), float64(step)
	if st == 0 && isInteger[T]() {
		st = 1
	}
	id := c.idFromPtr(unsafe.Pointer(value))
	base := c.LayoutNext()

	// handle text input mode
	if c.numberTextbox(&v, base, id) {
		return res
	}

	// handle normal mode
	c.UpdateControl(id, base, opt)

	// handle input
	if c.focus == id && (c.mouseDown|c.mousePressed) == MouseLeft && base.W > 0 {
		v = lo + float64(c.mousePos.X-base.X)*(hi-lo)/float64(base.W)
		if st != 0 {
			v = math.Round(v/st) * st
		}
	}
	// clamp and store value, update res
	nv := fromFloat[T](min(max(v, lo), hi))
	*value = nv
	if last != nv {
		res |= ResChange
	}

	// draw base
	c.DrawControlFrame(id, base, ColorBase, opt)
	// draw thumb
	w := c.Style.ThumbSize
	x := 0
	if hi > lo {
		x = int((float64(nv) - lo) * float64(base.W-w) / (hi - lo))
	}
	thumb := Rect{X: base.X + x, Y: base.Y, W: w, H: base.H}
	c.DrawControlFrame(id, thumb, ColorButton, opt)
	// draw text
	if format == "" {
		format = defaultFormat[T]()
	}
	c.DrawControlText(fmt.Sprintf(format, nv), base, ColorText, opt)
	return res
}

// Number draws a draggable numeric field bound to value (any numeric type),
// changing by step per unit of horizontal drag. Shift-click it to type an
// exact value.
func (c *Context) Number[T Number](value *T, step T) Result {
	return c.NumberEx(value, step, "", OptAlignCenter)
}

// NumberEx draws a numeric field with a fmt value format ("" picks %d or %.2f
// by type) and options.
func (c *Context) NumberEx[T Number](value *T, step T, format string, opt Option) Result {
	var res Result
	id := c.idFromPtr(unsafe.Pointer(value))
	base := c.LayoutNext()
	last := *value

	// handle text input mode
	v := float64(last)
	if c.numberTextbox(&v, base, id) {
		return res
	}
	if v != float64(last) {
		*value = fromFloat[T](v)
	}

	// handle normal mode
	c.UpdateControl(id, base, opt)

	// handle input
	if c.focus == id && c.mouseDown == MouseLeft && c.mouseDelta.X != 0 {
		*value = fromFloat[T](float64(*value) + float64(c.mouseDelta.X)*float64(step))
	}
	if *value != last {
		res |= ResChange
	}

	c.DrawControlFrame(id, base, ColorBase, opt)
	if format == "" {
		format = defaultFormat[T]()
	}
	c.DrawControlText(fmt.Sprintf(format, *value), base, ColorText, opt)
	return res
}

// ---- headers and tree nodes -------------------------------------------------

// header implements both Header (istreenode=false) and BeginTreenode
// (istreenode=true). It returns ResActive when expanded.
func (c *Context) header(label string, istreenode bool, opt Option) Result {
	id := c.getIDString(label)
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
	if c.clicked(id) {
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

// Header draws a collapsible header. The result is Active while expanded.
func (c *Context) Header(label string) Result {
	return c.HeaderEx(label, 0)
}

// HeaderEx draws a collapsible header with options (e.g. OptExpanded to
// start open).
func (c *Context) HeaderEx(label string, opt Option) Result {
	return c.header(label, false, opt)
}

// BeginTreenode starts a collapsible, indented tree node. When the result is
// Active, emit child controls and call EndTreenode.
func (c *Context) BeginTreenode(label string) Result {
	return c.BeginTreenodeEx(label, 0)
}

// BeginTreenodeEx starts a tree node with options.
func (c *Context) BeginTreenodeEx(label string, opt Option) Result {
	res := c.header(label, true, opt)
	if res.Active() {
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
func parseFloatPrefix(s string) float64 {
	s = strings.TrimSpace(s)
	n := len(s)
	digits := func(i int) int {
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i
	}
	i := 0
	if i < n && (s[i] == '+' || s[i] == '-') {
		i++
	}
	i = digits(i)
	if i < n && s[i] == '.' {
		i = digits(i + 1)
	}
	if i < n && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < n && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < n && s[j] >= '0' && s[j] <= '9' {
			i = digits(j)
		}
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	return f
}
