package microui

// layout is one frame's positioning state for the current container or column.
type layout struct {
	body      Rect
	next      Rect
	position  Vec2
	size      Vec2
	max       Vec2
	widths    [maxWidths]int
	items     int
	itemIndex int
	nextRow   int
	nextType  int
	indent    int
}

// next-rect placement modes for LayoutSetNext.
const (
	layoutRelative = 1
	layoutAbsolute = 2
)

// pushLayout pushes a new layout whose body is body offset by scroll, and
// starts it with a single full-width row.
func (c *Context) pushLayout(body Rect, scroll Vec2) {
	l := layout{
		body: Rect{X: body.X - scroll.X, Y: body.Y - scroll.Y, W: body.W, H: body.H},
		max:  Vec2{X: -0x1000000, Y: -0x1000000},
	}
	c.layoutStack = append(c.layoutStack, l)
	c.layoutRow(1, []int{0}, 0)
}

// getLayout returns a pointer to the current (top) layout.
func (c *Context) getLayout() *layout {
	return &c.layoutStack[len(c.layoutStack)-1]
}

// popLayout pops the top layout off the stack.
func (c *Context) popLayout() {
	c.layoutStack = c.layoutStack[:len(c.layoutStack)-1]
}

// LayoutBeginColumn starts a sub-layout occupying the next cell, letting
// controls inside it flow independently.
func (c *Context) LayoutBeginColumn() {
	c.pushLayout(c.LayoutNext(), Vec2{})
}

// LayoutEndColumn finishes a column, folding its extent back into the parent
// layout.
func (c *Context) LayoutEndColumn() {
	b := *c.getLayout()
	c.popLayout()
	// inherit position/next_row/max from child layout if they are greater
	a := c.getLayout()
	a.position.X = max(a.position.X, b.position.X+b.body.X-a.body.X)
	a.nextRow = max(a.nextRow, b.nextRow+b.body.Y-a.body.Y)
	a.max.X = max(a.max.X, b.max.X)
	a.max.Y = max(a.max.Y, b.max.Y)
}

// layoutRow configures the next row. When widths is non-nil it sets per-item
// widths; items is the number of items in the row.
func (c *Context) layoutRow(items int, widths []int, height int) {
	l := c.getLayout()
	if widths != nil {
		if items > maxWidths {
			panic("miniui: too many layout widths")
		}
		copy(l.widths[:items], widths[:items])
	}
	l.items = items
	l.position = Vec2{X: l.indent, Y: l.nextRow}
	l.size.Y = height
	l.itemIndex = 0
}

// LayoutRow begins a row of len(widths) items with the given per-item widths
// and row height. A width of 0 means "use the default control width"; a
// negative width means "extend to that many pixels from the right edge". A
// height of 0 means "use the default control height". Passing an empty slice
// produces a single-column row sized by LayoutWidth.
func (c *Context) LayoutRow(widths []int, height int) {
	c.layoutRow(len(widths), widths, height)
}

// LayoutWidth sets the width used for items in rows with no explicit widths.
func (c *Context) LayoutWidth(width int) {
	c.getLayout().size.X = width
}

// LayoutHeight sets the height used for the current row.
func (c *Context) LayoutHeight(height int) {
	c.getLayout().size.Y = height
}

// LayoutSetNext overrides the rectangle returned by the next LayoutNext call.
// When relative is true, r is offset by the layout body; otherwise r is used as
// an absolute rectangle (and does not advance the layout).
func (c *Context) LayoutSetNext(r Rect, relative bool) {
	l := c.getLayout()
	l.next = r
	if relative {
		l.nextType = layoutRelative
	} else {
		l.nextType = layoutAbsolute
	}
}

// LayoutNext returns the rectangle for the next control, advancing the layout.
func (c *Context) LayoutNext() Rect {
	l := c.getLayout()
	style := c.Style
	var res Rect

	if l.nextType != 0 {
		// handle rect set by LayoutSetNext
		typ := l.nextType
		l.nextType = 0
		res = l.next
		if typ == layoutAbsolute {
			c.LastRect = res
			return res
		}
	} else {
		// handle next row
		if l.itemIndex == l.items {
			c.layoutRow(l.items, nil, l.size.Y)
		}

		// position
		res.X = l.position.X
		res.Y = l.position.Y

		// size
		if l.items > 0 {
			res.W = l.widths[l.itemIndex]
		} else {
			res.W = l.size.X
		}
		res.H = l.size.Y
		if res.W == 0 {
			res.W = style.Size.X + style.Padding*2
		}
		if res.H == 0 {
			res.H = style.Size.Y + style.Padding*2
		}
		if res.W < 0 {
			res.W += l.body.W - res.X + 1
		}
		if res.H < 0 {
			res.H += l.body.H - res.Y + 1
		}

		l.itemIndex++
	}

	// update position
	l.position.X += res.W + style.Spacing
	l.nextRow = max(l.nextRow, res.Y+res.H+style.Spacing)

	// apply body offset
	res.X += l.body.X
	res.Y += l.body.Y

	// update max position
	l.max.X = max(l.max.X, res.X+res.W)
	l.max.Y = max(l.max.Y, res.Y+res.H)

	c.LastRect = res
	return res
}
