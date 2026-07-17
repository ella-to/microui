package microui

import (
	"encoding/binary"
	"sort"
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

// Context holds all state for a miniui instance. Create one with New, set the
// TextWidth and TextHeight callbacks, then drive it with Begin / controls / End
// each frame.
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

	hover        ID
	focus        ID
	lastID       ID
	LastRect     Rect
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
	mouseDown    int
	mousePressed int
	keyDown      int
	keyPressed   int
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
		c.DrawBox(expandRect(rect, 1), c.Style.Colors[ColorBorder])
	}
}

// Begin starts a new frame. TextWidth and TextHeight must be set first.
func (c *Context) Begin() {
	if c.TextWidth == nil || c.TextHeight == nil {
		panic("miniui: TextWidth and TextHeight must be set before Begin")
	}
	c.commandList = c.commandList[:0]
	c.rootList = c.rootList[:0]
	c.scrollTarget = nil
	c.hoverRoot = c.nextHoverRoot
	c.nextHoverRoot = nil
	c.mouseDelta.X = c.mousePos.X - c.lastMousePos.X
	c.mouseDelta.Y = c.mousePos.Y - c.lastMousePos.Y
	c.frame++
}

// End finishes the frame: it resolves scrolling and focus, sorts root
// containers by z-index and stitches their command ranges together so that
// Commands iterates them back-to-front.
func (c *Context) End() {
	// check stacks
	if len(c.containerStack) != 0 {
		panic("miniui: unbalanced container stack at End")
	}
	if len(c.clipStack) != 0 {
		panic("miniui: unbalanced clip stack at End")
	}
	if len(c.idStack) != 0 {
		panic("miniui: unbalanced id stack at End")
	}
	if len(c.layoutStack) != 0 {
		panic("miniui: unbalanced layout stack at End")
	}

	// handle scroll input
	if c.scrollTarget != nil {
		c.scrollTarget.Scroll.X += c.scrollDelta.X
		c.scrollTarget.Scroll.Y += c.scrollDelta.Y
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
	n := len(c.rootList)
	sort.SliceStable(c.rootList, func(i, j int) bool {
		return c.rootList[i].ZIndex < c.rootList[j].ZIndex
	})

	// set root container jump commands
	for i := range n {
		cnt := c.rootList[i]
		// if this is the first container then make the first command jump to
		// it. otherwise set the previous container's tail to jump to this one.
		if i == 0 {
			c.commandList[0].jump = cnt.head + 1
		} else {
			prev := c.rootList[i-1]
			c.commandList[prev.tail].jump = cnt.head + 1
		}
		// make the last container's tail jump to the end of the command list
		if i == n-1 {
			c.commandList[cnt.tail].jump = len(c.commandList)
		}
	}
}

// SetFocus sets the focused control id and marks focus as updated this frame.
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

// MousePressed returns the mouse buttons pressed this frame, as a bitmask of
// MouseLeft / MouseRight / MouseMiddle.
func (c *Context) MousePressed() int { return c.mousePressed }

// MouseDown returns the mouse buttons currently held, as a bitmask.
func (c *Context) MouseDown() int { return c.mouseDown }

// MousePos returns the current mouse position.
func (c *Context) MousePos() Vec2 { return c.mousePos }

// MouseDelta returns the mouse movement since the previous frame.
func (c *Context) MouseDelta() Vec2 { return c.mouseDelta }

// KeyPressed returns the keys pressed this frame, as a bitmask of
// KeyShift / KeyCtrl / KeyAlt / KeyBackspace / KeyReturn.
func (c *Context) KeyPressed() int { return c.keyPressed }

// KeyDown returns the keys currently held, as a bitmask.
func (c *Context) KeyDown() int { return c.keyDown }

// ---- id hashing -------------------------------------------------------------

const hashInitial ID = 2166136261 // 32-bit FNV-1a offset basis

// hashBytes folds data into h using 32-bit FNV-1a, matching microui.
func hashBytes(h ID, data []byte) ID {
	for _, b := range data {
		h = (h ^ ID(b)) * 16777619
	}
	return h
}

// GetID hashes data together with the top of the id stack to produce a control
// id, and records it as the last id.
func (c *Context) GetID(data []byte) ID {
	res := hashInitial
	if n := len(c.idStack); n > 0 {
		res = c.idStack[n-1]
	}
	res = hashBytes(res, data)
	c.lastID = res
	return res
}

// idFromPtr derives an id from a pointer's address, matching microui's habit of
// keying value controls by the address of the caller's variable. The variable
// must persist across frames for retained state to remain stable.
func (c *Context) idFromPtr(p unsafe.Pointer) ID {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(uintptr(p)))
	return c.GetID(buf[:])
}

// idFromInt derives an id from an int value, matching microui's id for an
// icon-only button (mu_get_id(ctx, &icon, sizeof(icon))).
func (c *Context) idFromInt(v int) ID {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(v))
	return c.GetID(buf[:])
}

// PushID pushes a child id (derived from data) onto the id stack so that
// subsequent control ids are scoped beneath it.
func (c *Context) PushID(data []byte) {
	c.idStack = append(c.idStack, c.GetID(data))
}

// PopID pops the top of the id stack.
func (c *Context) PopID() {
	if len(c.idStack) == 0 {
		panic("miniui: id stack underflow")
	}
	c.idStack = c.idStack[:len(c.idStack)-1]
}

// ---- input handlers ---------------------------------------------------------

// InputMouseMove reports the current mouse position in screen coordinates.
func (c *Context) InputMouseMove(x, y int) {
	c.mousePos = Vec2{X: x, Y: y}
}

// InputMouseDown reports a mouse button press (a MouseLeft/Right/Middle bit).
func (c *Context) InputMouseDown(x, y, btn int) {
	c.InputMouseMove(x, y)
	c.mouseDown |= btn
	c.mousePressed |= btn
}

// InputMouseUp reports a mouse button release.
func (c *Context) InputMouseUp(x, y, btn int) {
	c.InputMouseMove(x, y)
	c.mouseDown &^= btn
}

// InputScroll reports a scroll-wheel delta (accumulated within the frame).
func (c *Context) InputScroll(x, y int) {
	c.scrollDelta.X += x
	c.scrollDelta.Y += y
}

// InputKeyDown reports a key press (a KeyShift/Ctrl/Alt/Backspace/Return bit).
func (c *Context) InputKeyDown(key int) {
	c.keyPressed |= key
	c.keyDown |= key
}

// InputKeyUp reports a key release.
func (c *Context) InputKeyUp(key int) {
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
		panic("miniui: no current container")
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
	id := c.GetID([]byte(name))
	return c.getContainer(id, 0)
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
		panic("miniui: pool exhausted")
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
