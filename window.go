package microui

// popContainer records the current container's content size and pops the
// container, its layout and its id.
func (c *Context) popContainer() {
	cnt := c.GetCurrentContainer()
	l := c.getLayout()
	cnt.ContentSize.X = l.max.X - l.body.X
	cnt.ContentSize.Y = l.max.Y - l.body.Y
	c.containerStack = c.containerStack[:len(c.containerStack)-1]
	c.popLayout()
	c.PopID()
}

// scrollbarV adds the vertical scrollbar for cnt, shrinking and scrolling body.
func (c *Context) scrollbarV(cnt *Container, b *Rect, cs Vec2) {
	maxscroll := cs.Y - b.H
	if maxscroll > 0 && b.H > 0 {
		id := c.GetID([]byte("!scrollbary"))
		base := *b
		base.X = b.X + b.W
		base.W = c.Style.ScrollbarSize

		c.UpdateControl(id, base, 0)
		if c.focus == id && c.mouseDown == MouseLeft {
			cnt.Scroll.Y += c.mouseDelta.Y * cs.Y / base.H
		}
		cnt.Scroll.Y = clampI(cnt.Scroll.Y, 0, maxscroll)

		c.DrawFrame(c, base, ColorScrollBase)
		thumb := base
		thumb.H = max(c.Style.ThumbSize, base.H*b.H/cs.Y)
		thumb.Y += cnt.Scroll.Y * (base.H - thumb.H) / maxscroll
		c.DrawFrame(c, thumb, ColorScrollThumb)

		if c.MouseOver(*b) {
			c.scrollTarget = cnt
		}
	} else {
		cnt.Scroll.Y = 0
	}
}

// scrollbarH adds the horizontal scrollbar for cnt, shrinking and scrolling
// body. It mirrors scrollbarV with x/y and w/h swapped.
func (c *Context) scrollbarH(cnt *Container, b *Rect, cs Vec2) {
	maxscroll := cs.X - b.W
	if maxscroll > 0 && b.W > 0 {
		id := c.GetID([]byte("!scrollbarx"))
		base := *b
		base.Y = b.Y + b.H
		base.H = c.Style.ScrollbarSize

		c.UpdateControl(id, base, 0)
		if c.focus == id && c.mouseDown == MouseLeft {
			cnt.Scroll.X += c.mouseDelta.X * cs.X / base.W
		}
		cnt.Scroll.X = clampI(cnt.Scroll.X, 0, maxscroll)

		c.DrawFrame(c, base, ColorScrollBase)
		thumb := base
		thumb.W = max(c.Style.ThumbSize, base.W*b.W/cs.X)
		thumb.X += cnt.Scroll.X * (base.W - thumb.W) / maxscroll
		c.DrawFrame(c, thumb, ColorScrollThumb)

		if c.MouseOver(*b) {
			c.scrollTarget = cnt
		}
	} else {
		cnt.Scroll.X = 0
	}
}

// scrollbars adds scrollbars to body when the content overflows it.
func (c *Context) scrollbars(cnt *Container, body *Rect) {
	sz := c.Style.ScrollbarSize
	cs := cnt.ContentSize
	cs.X += c.Style.Padding * 2
	cs.Y += c.Style.Padding * 2
	c.PushClipRect(*body)
	// resize body to make room for scrollbars
	if cs.Y > cnt.Body.H {
		body.W -= sz
	}
	if cs.X > cnt.Body.W {
		body.H -= sz
	}
	c.scrollbarV(cnt, body, cs)
	c.scrollbarH(cnt, body, cs)
	c.PopClipRect()
}

// pushContainerBody sets up the layout for a container's inner body, adding
// scrollbars unless disabled.
func (c *Context) pushContainerBody(cnt *Container, body Rect, opt Option) {
	if !opt.has(OptNoScroll) {
		c.scrollbars(cnt, &body)
	}
	c.pushLayout(expandRect(body, -c.Style.Padding), cnt.Scroll)
	cnt.Body = body
}

// beginRootContainer pushes a root container (window/popup), reserving its head
// jump command and updating the next hover root.
func (c *Context) beginRootContainer(cnt *Container) {
	c.containerStack = append(c.containerStack, cnt)
	// push container to roots list and push head command
	c.rootList = append(c.rootList, cnt)
	cnt.head = c.pushJump(-1)
	// set as hover root if the mouse is overlapping this container and it has a
	// higher zindex than the current hover root
	if rectOverlapsVec2(cnt.Rect, c.mousePos) &&
		(c.nextHoverRoot == nil || cnt.ZIndex > c.nextHoverRoot.ZIndex) {
		c.nextHoverRoot = cnt
	}
	// clipping is reset here in case a root container is created within another
	// root container's begin/end block
	c.clipStack = append(c.clipStack, unclippedRect)
}

// endRootContainer pushes the tail jump command and pops the root container.
func (c *Context) endRootContainer() {
	cnt := c.GetCurrentContainer()
	cnt.tail = c.pushJump(-1)
	c.commandList[cnt.head].jump = len(c.commandList)
	c.PopClipRect()
	c.popContainer()
}

// BeginWindow begins a window with default options. If it returns non-zero,
// emit controls and then call EndWindow.
func (c *Context) BeginWindow(title string, rect Rect) int {
	return c.BeginWindowEx(title, rect, 0)
}

// BeginWindowEx begins a window with options. rect is the initial window
// rectangle, used only the first time the window appears.
func (c *Context) BeginWindowEx(title string, rect Rect, opt Option) int {
	id := c.GetID([]byte(title))
	cnt := c.getContainer(id, opt)
	if cnt == nil || !cnt.Open {
		return 0
	}
	c.idStack = append(c.idStack, id)

	if cnt.Rect.W == 0 {
		cnt.Rect = rect
	}
	c.beginRootContainer(cnt)
	rect = cnt.Rect
	body := cnt.Rect

	// draw frame
	if !opt.has(OptNoFrame) {
		c.DrawFrame(c, rect, ColorWindowBG)
	}

	// do title bar
	if !opt.has(OptNoTitle) {
		tr := rect
		tr.H = c.Style.TitleHeight
		c.DrawFrame(c, tr, ColorTitleBG)

		// do title text
		tid := c.GetID([]byte("!title"))
		c.UpdateControl(tid, tr, opt)
		c.DrawControlText(title, tr, ColorTitleText, opt)
		if tid == c.focus && c.mouseDown == MouseLeft {
			cnt.Rect.X += c.mouseDelta.X
			cnt.Rect.Y += c.mouseDelta.Y
		}
		body.Y += tr.H
		body.H -= tr.H

		// do `close` button
		if !opt.has(OptNoClose) {
			clid := c.GetID([]byte("!close"))
			r := Rect{X: tr.X + tr.W - tr.H, Y: tr.Y, W: tr.H, H: tr.H}
			tr.W -= r.W
			c.DrawIcon(IconClose, r, c.Style.Colors[ColorTitleText])
			c.UpdateControl(clid, r, opt)
			if c.mousePressed == MouseLeft && clid == c.focus {
				cnt.Open = false
			}
		}
	}

	c.pushContainerBody(cnt, body, opt)

	// do `resize` handle
	if !opt.has(OptNoResize) {
		sz := c.Style.ResizeHandle
		if sz <= 0 {
			sz = c.Style.TitleHeight // microui default: handle == title height
		}
		rid := c.GetID([]byte("!resize"))
		r := Rect{X: rect.X + rect.W - sz, Y: rect.Y + rect.H - sz, W: sz, H: sz}
		c.UpdateControl(rid, r, opt)
		if rid == c.focus && c.mouseDown == MouseLeft {
			cnt.Rect.W = max(c.Style.MinWidth, cnt.Rect.W+c.mouseDelta.X)
			cnt.Rect.H = max(c.Style.MinHeight, cnt.Rect.H+c.mouseDelta.Y)
		}
	}

	// resize to content size
	if opt.has(OptAutoSize) {
		r := c.getLayout().body
		cnt.Rect.W = cnt.ContentSize.X + (cnt.Rect.W - r.W)
		cnt.Rect.H = cnt.ContentSize.Y + (cnt.Rect.H - r.H)
	}

	// close if this is a popup window and elsewhere was clicked
	if opt.has(OptPopup) && c.mousePressed != 0 && c.hoverRoot != cnt {
		cnt.Open = false
	}

	c.PushClipRect(cnt.Body)
	return ResActive
}

// EndWindow ends the window started by BeginWindow.
func (c *Context) EndWindow() {
	c.PopClipRect()
	c.endRootContainer()
}

// OpenPopup opens the popup named name at the current mouse position.
func (c *Context) OpenPopup(name string) {
	cnt := c.GetContainer(name)
	// set as hover root so popup isn't closed in BeginWindowEx
	c.hoverRoot = cnt
	c.nextHoverRoot = cnt
	// position at mouse cursor, open and bring-to-front
	cnt.Rect = Rect{X: c.mousePos.X, Y: c.mousePos.Y, W: 1, H: 1}
	cnt.Open = true
	c.BringToFront(cnt)
}

// BeginPopup begins a popup window opened with OpenPopup. If it returns
// non-zero, emit controls and call EndPopup.
func (c *Context) BeginPopup(name string) int {
	opt := OptPopup | OptAutoSize | OptNoResize | OptNoScroll | OptNoTitle | OptClosed
	return c.BeginWindowEx(name, Rect{}, opt)
}

// EndPopup ends a popup begun with BeginPopup.
func (c *Context) EndPopup() {
	c.EndWindow()
}

// BeginPanel begins a scrollable sub-region within the current container.
func (c *Context) BeginPanel(name string) {
	c.BeginPanelEx(name, 0)
}

// BeginPanelEx begins a panel with options. Always pair with EndPanel.
func (c *Context) BeginPanelEx(name string, opt Option) {
	c.PushID([]byte(name))
	cnt := c.getContainer(c.lastID, opt)
	cnt.Rect = c.LayoutNext()
	if !opt.has(OptNoFrame) {
		c.DrawFrame(c, cnt.Rect, ColorPanelBG)
	}
	c.containerStack = append(c.containerStack, cnt)
	c.pushContainerBody(cnt, cnt.Rect, opt)
	c.PushClipRect(cnt.Body)
}

// EndPanel ends a panel begun with BeginPanel.
func (c *Context) EndPanel() {
	c.PopClipRect()
	c.popContainer()
}
