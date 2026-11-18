package microui

import (
	"cmp"
	"reflect"
	"slices"
	"time"
	"unsafe"
)

// Container is the retained state for a window, popup or panel: its rectangle,
// scroll offset, content extent and z-index. Returned by GetContainer /
// GetCurrentContainer so callers can inspect or adjust it.
type Container struct {
	Rect        Rect
	Body        Rect
	ContentSize Vec2
	Scroll      Vec2
	ZIndex      int
	Open        bool

	// head/tail are command-list indices delimiting this container's commands
	// when it acts as a root container during a frame; -1 means "not a root".
	head, tail int
}

// textEdit is the editing state (caret, selection, horizontal scroll) of the
// focused textbox. Only one textbox can be focused at a time, so the context
// retains a single instance; id records which control owns it, and the state
// is discarded when that control loses focus.
type textEdit struct {
	id     ID
	cursor int  // rune index of the insertion point
	anchor int  // other end of the selection; == cursor when none
	scroll int  // text units scrolled off the left edge
	drag   bool // a mouse selection drag is in progress

	// double-click tracking, in wall-clock time (see now).
	lastClick  time.Time
	lastClickX int
}

// poolItem is a slot in a retained-state pool, keyed by id and aged by the
// frame it was last touched.
type poolItem struct {
	id         ID
	lastUpdate int
}

// Context holds all state for a microui instance. Create one with New, set the
// TextWidth and TextHeight callbacks, then drive it with Begin / controls / End
// each frame. A Context is not safe for concurrent use: build every frame from
// a single goroutine.
type Context struct {
	// Callbacks the caller must provide. TextWidth returns the rendered width
	// of str in the given font; TextHeight returns the line height. DrawFrame
	// draws a control's background+border and may be replaced to restyle.
	TextWidth  func(font Font, str string) int
	TextHeight func(font Font) int
	DrawFrame  func(c *Context, rect Rect, colorid ColorID)

	// Style points at the active style. It starts at an internal copy of
	// DefaultStyle and may be repointed or mutated by the caller.
	Style *Style
	style Style // backing storage for the default Style

	// LastRect is the rectangle of the most recently laid out control.
	LastRect Rect

	hover        ID
	focus        ID
	lastID       ID
	lastZIndex   int
	updatedFocus bool
	frame        int

	hoverRoot     *Container
	nextHoverRoot *Container
	scrollTarget  *Container

	numberEditBuf string
	numberEdit    ID
	textEdit      textEdit

	// stacks (grow as needed)
	commandList    []Command
	rootList       []*Container
	containerStack []*Container
	clipStack      []Rect
	idStack        []ID
	layoutStack    []layout

	// retained-state pools (fixed size, LRU reclaimed)
	containerPool [containerPoolSize]poolItem
	containers    [containerPoolSize]Container
	treenodePool  [treenodePoolSize]poolItem

	// input state
	mousePos     Vec2
	lastMousePos Vec2
	mouseDelta   Vec2
	scrollDelta  Vec2
	mouseDown    MouseButton
	mousePressed MouseButton
	keyDown      Key
	keyPressed   Key
	inputText    string
}

// New allocates and initializes a Context with the default style.
func New() *Context {
	c := &Context{}
	c.DrawFrame = defaultDrawFrame
	c.style = DefaultStyle()
	c.Style = &c.style
	return c
}

// defaultDrawFrame is the default DrawFrame implementation: a filled rect plus
// a border, except for scrollbar and title backgrounds.
func defaultDrawFrame(c *Context, rect Rect, colorid ColorID) {
	c.DrawRect(rect, c.Style.Colors[colorid])
	if colorid == ColorScrollBase || colorid == ColorScrollThumb || colorid == ColorTitleBG {
		return
	}
	if c.Style.Colors[ColorBorder].A != 0 {
		c.DrawBox(rect.Expand(1), c.Style.Colors[ColorBorder])
	}
}

// Begin starts a new frame. TextWidth and TextHeight must be set first.
func (c *Context) Begin() {
	if c.TextWidth == nil || c.TextHeight == nil {
		panic("microui: TextWidth and TextHeight must be set before Begin")
	}
	c.commandList = c.commandList[:0]
	c.rootList = c.rootList[:0]
	c.scrollTarget = nil
	c.hoverRoot = c.nextHoverRoot
	c.nextHoverRoot = nil
	c.mouseDelta = c.mousePos.Sub(c.lastMousePos)
	c.frame++
}

// End finishes the frame: it resolves scrolling and focus, sorts root
// containers by z-index and stitches their command ranges together so that
// Commands iterates them back-to-front.
func (c *Context) End() {
	switch {
	case len(c.containerStack) != 0:
		panic("microui: unbalanced container stack at End (missing EndWindow/EndPanel/EndPopup?)")
	case len(c.clipStack) != 0:
		panic("microui: unbalanced clip stack at End (missing PopClipRect?)")
	case len(c.idStack) != 0:
		panic("microui: unbalanced id stack at End (missing PopID/EndTreenode?)")
	case len(c.layoutStack) != 0:
		panic("microui: unbalanced layout stack at End (missing LayoutEndColumn?)")
	}

	// handle scroll input
	if c.scrollTarget != nil {
		c.scrollTarget.Scroll = c.scrollTarget.Scroll.Add(c.scrollDelta)
	}

	// unset focus if focus id was not touched this frame
	if !c.updatedFocus {
		c.focus = 0
	}
	c.updatedFocus = false

	// bring hover root to front if mouse was pressed
	if c.mousePressed != 0 && c.nextHoverRoot != nil &&
		c.nextHoverRoot.ZIndex < c.lastZIndex &&
		c.nextHoverRoot.ZIndex >= 0 {
		c.BringToFront(c.nextHoverRoot)
	}

	// reset input state
	c.keyPressed = 0
	c.inputText = ""
	c.mousePressed = 0
	c.scrollDelta = Vec2{}
	c.lastMousePos = c.mousePos

	// sort root containers by zindex
	slices.SortStableFunc(c.rootList, func(a, b *Container) int {
		return cmp.Compare(a.ZIndex, b.ZIndex)
	})

	// set root container jump commands: the first command jumps to the
	// back-most container, each container's tail jumps to the next one, and
	// the front-most container's tail jumps to the end of the list.
	for i, cnt := range c.rootList {
		if i == 0 {
			c.commandList[0].jump = cnt.head + 1
		} else {
			c.commandList[c.rootList[i-1].tail].jump = cnt.head + 1
		}
		if i == len(c.rootList)-1 {
			c.commandList[cnt.tail].jump = len(c.commandList)
		}
	}
}

// SetFocus sets the focused control id and marks focus as updated this frame.
// Pass 0 to clear focus.
func (c *Context) SetFocus(id ID) {
	c.focus = id
	c.updatedFocus = true
}

// LastID returns the id of the most recently created control.
func (c *Context) LastID() ID { return c.lastID }

// ---- interaction state (for building custom controls) -----------------------
//
// These read-only accessors expose the input/focus state that the built-in
// controls use, so callers can implement their own controls from the public
// primitives (LayoutNext, UpdateControl, DrawControlFrame, ...).

// Focus returns the id of the currently focused control, or 0 if none.
func (c *Context) Focus() ID { return c.focus }

// Hover returns the id of the currently hovered control, or 0 if none.
func (c *Context) Hover() ID { return c.hover }

// MousePressed returns the mouse buttons pressed this frame.
func (c *Context) MousePressed() MouseButton { return c.mousePressed }

// MouseDown returns the mouse buttons currently held.
func (c *Context) MouseDown() MouseButton { return c.mouseDown }

// MousePos returns the current mouse position.
func (c *Context) MousePos() Vec2 { return c.mousePos }

// MouseDelta returns the mouse movement since the previous frame.
func (c *Context) MouseDelta() Vec2 { return c.mouseDelta }

// ScrollDelta returns the scroll-wheel movement accumulated this frame.
func (c *Context) ScrollDelta() Vec2 { return c.scrollDelta }

// KeyPressed returns the keys pressed this frame.
func (c *Context) KeyPressed() Key { return c.keyPressed }

// KeyDown returns the keys currently held.
func (c *Context) KeyDown() Key { return c.keyDown }

// TextInput returns the text typed this frame (consumed by the focused
// textbox, but readable by custom controls too).
func (c *Context) TextInput() string { return c.inputText }

// ---- id hashing -------------------------------------------------------------

const (
	hashInitial ID = 2166136261 // 32-bit FNV-1a offset basis
	hashPrime   ID = 16777619
)

// hashBytes folds data into h using 32-bit FNV-1a, matching microui.
func hashBytes(h ID, data []byte) ID {
	for _, b := range data {
		h = (h ^ ID(b)) * hashPrime
	}
	return h
}

// hashString is hashBytes for a string, without converting it.
func hashString(h ID, s string) ID {
	for i := 0; i < len(s); i++ {
		h = (h ^ ID(s[i])) * hashPrime
	}
	return h
}

// hashUint64 folds the 8 little-endian bytes of v into h.
func hashUint64(h ID, v uint64) ID {
	for range 8 {
		h = (h ^ ID(byte(v))) * hashPrime
		v >>= 8
	}
	return h
}

// hashKey hashes any IDKey into h. The common concrete types take a fast
// path; named types (e.g. `type RowID int`) fall back to reflection.
func hashKey[K IDKey](h ID, key K) ID {
	switch k := any(key).(type) {
	case string:
		return hashString(h, k)
	case []byte:
		return hashBytes(h, k)
	case int:
		return hashUint64(h, uint64(k))
	case int64:
		return hashUint64(h, uint64(k))
	case uint64:
		return hashUint64(h, k)
	case uint32:
		return hashUint64(h, uint64(k))
	case int32:
		return hashUint64(h, uint64(k))
	}
	v := reflect.ValueOf(key)
	switch v.Kind() {
	case reflect.String:
		return hashString(h, v.String())
	case reflect.Slice:
		return hashBytes(h, v.Bytes())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return hashUint64(h, uint64(v.Int()))
	default:
		return hashUint64(h, v.Uint())
	}
}

// idSeed returns the hash seed: the top of the id stack, or the FNV basis.
func (c *Context) idSeed() ID {
	if n := len(c.idStack); n > 0 {
		return c.idStack[n-1]
	}
	return hashInitial
}

// GetID hashes key (a string, []byte or integer) together with the top of
// the id stack to produce a control id, and records it as the last id.
//
//	id := ctx.GetID("my-control")
//	id := ctx.GetID(rowIndex)
func (c *Context) GetID[K IDKey](key K) ID {
	c.lastID = hashKey(c.idSeed(), key)
	return c.lastID
}

// getIDString is the non-generic string form of GetID used internally.
func (c *Context) getIDString(s string) ID {
	c.lastID = hashString(c.idSeed(), s)
	return c.lastID
}

// idFromPtr derives an id from a pointer's address, matching microui's habit of
// keying value controls by the address of the caller's variable. The variable
// must persist across frames for retained state to remain stable.
func (c *Context) idFromPtr(p unsafe.Pointer) ID {
	c.lastID = hashUint64(c.idSeed(), uint64(uintptr(p)))
	return c.lastID
}

// idFromIcon derives an id from an icon value, matching microui's id for an
// icon-only button (mu_get_id(ctx, &icon, sizeof(icon))).
func (c *Context) idFromIcon(icon Icon) ID {
	h := c.idSeed()
	v := uint32(icon)
	for range 4 {
		h = (h ^ ID(byte(v))) * hashPrime
		v >>= 8
	}
	c.lastID = h
	return h
}

// PushID pushes a child scope derived from key (a string, []byte or integer)
// onto the id stack so that subsequent control ids are scoped beneath it. Use
// it to disambiguate controls created in a loop:
//
//	for i, row := range rows {
//	    ctx.PushID(i)
//	    if ctx.Button("Delete").Submitted() { ... }
//	    ctx.PopID()
//	}
func (c *Context) PushID[K IDKey](key K) {
	c.idStack = append(c.idStack, c.GetID(key))
}

// PopID pops the top of the id stack.
func (c *Context) PopID() {
	if len(c.idStack) == 0 {
		panic("microui: id stack underflow")
	}
	c.idStack = c.idStack[:len(c.idStack)-1]
}

// ---- input handlers ---------------------------------------------------------

// InputMouseMove reports the current mouse position in screen coordinates.
func (c *Context) InputMouseMove(x, y int) {
	c.mousePos = Vec2{X: x, Y: y}
}

// InputMouseDown reports a mouse button press.
func (c *Context) InputMouseDown(x, y int, btn MouseButton) {
	c.InputMouseMove(x, y)
	c.mouseDown |= btn
	c.mousePressed |= btn
}

// InputMouseUp reports a mouse button release.
func (c *Context) InputMouseUp(x, y int, btn MouseButton) {
	c.InputMouseMove(x, y)
	c.mouseDown &^= btn
}

// InputScroll reports a scroll-wheel delta (accumulated within the frame).
// Positive y scrolls content down.
func (c *Context) InputScroll(x, y int) {
	c.scrollDelta = c.scrollDelta.Add(Vec2{X: x, Y: y})
}

// InputKeyDown reports a key press. For a discrete tap, follow it with
// InputKeyUp before the frame: the press is still seen via KeyPressed.
func (c *Context) InputKeyDown(key Key) {
	c.keyPressed |= key
	c.keyDown |= key
}

// InputKeyUp reports a key release.
func (c *Context) InputKeyUp(key Key) {
	c.keyDown &^= key
}

// InputText appends UTF-8 text typed this frame, consumed by the focused
// textbox.
func (c *Context) InputText(text string) {
	c.inputText += text
}

// ---- containers -------------------------------------------------------------

// GetCurrentContainer returns the innermost open container.
func (c *Context) GetCurrentContainer() *Container {
	if len(c.containerStack) == 0 {
		panic("microui: no current container (call inside BeginWindow/BeginPanel)")
	}
	return c.containerStack[len(c.containerStack)-1]
}

// getContainer returns the retained container for id, allocating one from the
// pool if needed. It returns nil for a closed container that has no live slot.
func (c *Context) getContainer(id ID, opt Option) *Container {
	// try to get existing container from pool
	if idx := poolGet(c.containerPool[:], id); idx >= 0 {
		if c.containers[idx].Open || !opt.has(OptClosed) {
			c.poolUpdate(c.containerPool[:], idx)
		}
		return &c.containers[idx]
	}
	if opt.has(OptClosed) {
		return nil
	}
	// container not found in pool: init new container
	idx := c.poolInit(c.containerPool[:], id)
	cnt := &c.containers[idx]
	*cnt = Container{Open: true, head: -1, tail: -1}
	c.BringToFront(cnt)
	return cnt
}

// GetContainer returns the retained container associated with name, creating it
// if necessary.
func (c *Context) GetContainer(name string) *Container {
	return c.getContainer(c.getIDString(name), 0)
}

// BringToFront raises cnt above all other containers by giving it the highest
// z-index.
func (c *Context) BringToFront(cnt *Container) {
	c.lastZIndex++
	cnt.ZIndex = c.lastZIndex
}

// ---- pools ------------------------------------------------------------------

// poolInit claims the least-recently-updated slot for id and returns its index.
func (c *Context) poolInit(items []poolItem, id ID) int {
	n := -1
	f := c.frame
	for i := range items {
		if items[i].lastUpdate < f {
			f = items[i].lastUpdate
			n = i
		}
	}
	if n < 0 {
		panic("microui: retained-state pool exhausted (too many live windows/panels/tree nodes)")
	}
	items[n].id = id
	c.poolUpdate(items, n)
	return n
}

// poolGet returns the index of the slot holding id, or -1 if absent.
func poolGet(items []poolItem, id ID) int {
	for i := range items {
		if items[i].id == id {
			return i
		}
	}
	return -1
}

// poolUpdate stamps slot idx with the current frame number.
func (c *Context) poolUpdate(items []poolItem, idx int) {
	items[idx].lastUpdate = c.frame
}
