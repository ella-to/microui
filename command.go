package microui

import "iter"

// CommandType discriminates the kind of a Command.
type CommandType int

const (
	// CommandJump is an internal control-flow command used to stitch
	// z-ordered windows together. It is never yielded by Commands.
	CommandJump CommandType = iota + 1
	// CommandClip sets the active clip rectangle to Command.Rect.
	CommandClip
	// CommandRect fills Command.Rect with Command.Color.
	CommandRect
	// CommandText draws Command.Str at Command.Pos in Command.Color using
	// Command.Font.
	CommandText
	// CommandIcon draws the built-in icon Command.Icon inside Command.Rect
	// tinted with Command.Color.
	CommandIcon
)

// Command is a single draw (or control-flow) instruction emitted during a
// frame. Which fields are meaningful depends on Type:
//
//	CommandClip: Rect
//	CommandRect: Rect, Color
//	CommandText: Str, Pos, Color, Font
//	CommandIcon: Icon, Rect, Color
//
// Unlike microui, commands are stored as fixed-size struct values in a slice
// rather than as variable-length records in a byte buffer; the jump field holds
// a slice index instead of a pointer.
type Command struct {
	Type  CommandType
	Rect  Rect   // Clip, Rect, Icon
	Color Color  // Rect, Text, Icon
	Pos   Vec2   // Text
	Str   string // Text
	Font  Font   // Text
	Icon  int    // Icon

	jump int // CommandJump: destination index into commandList
}

// pushCommand appends a command and returns its index in the command list.
func (c *Context) pushCommand(cmd Command) int {
	c.commandList = append(c.commandList, cmd)
	return len(c.commandList) - 1
}

// pushJump appends a jump command targeting dst (a command index, or -1 if not
// yet known) and returns its index.
func (c *Context) pushJump(dst int) int {
	return c.pushCommand(Command{Type: CommandJump, jump: dst})
}

// Commands returns an iterator over the frame's draw commands in back-to-front
// (z-order) order. Internal jump commands are followed and never yielded, so
// callers see only Clip/Rect/Text/Icon commands. It must be called after End.
func (c *Context) Commands() iter.Seq[Command] {
	return func(yield func(Command) bool) {
		i := 0
		for i < len(c.commandList) {
			cmd := c.commandList[i]
			if cmd.Type == CommandJump {
				i = cmd.jump
				continue
			}
			if !yield(cmd) {
				return
			}
			i++
		}
	}
}

// ---- clipping ---------------------------------------------------------------

// PushClipRect intersects rect with the current clip rectangle and pushes the
// result onto the clip stack.
func (c *Context) PushClipRect(rect Rect) {
	last := c.GetClipRect()
	c.clipStack = append(c.clipStack, intersectRects(rect, last))
}

// PopClipRect pops the top clip rectangle off the clip stack.
func (c *Context) PopClipRect() {
	if len(c.clipStack) == 0 {
		panic("miniui: clip stack underflow")
	}
	c.clipStack = c.clipStack[:len(c.clipStack)-1]
}

// GetClipRect returns the current clip rectangle.
func (c *Context) GetClipRect() Rect {
	if len(c.clipStack) == 0 {
		panic("miniui: clip stack is empty")
	}
	return c.clipStack[len(c.clipStack)-1]
}

// CheckClip reports how r relates to the current clip rectangle: clipNone if r
// is fully visible, clipAll if fully clipped, or clipPart if partially clipped.
func (c *Context) checkClip(r Rect) int {
	cr := c.GetClipRect()
	if r.X > cr.X+cr.W || r.X+r.W < cr.X ||
		r.Y > cr.Y+cr.H || r.Y+r.H < cr.Y {
		return clipAll
	}
	if r.X >= cr.X && r.X+r.W <= cr.X+cr.W &&
		r.Y >= cr.Y && r.Y+r.H <= cr.Y+cr.H {
		return clipNone
	}
	return clipPart
}

// ---- draw primitives --------------------------------------------------------

// SetClip emits a clip command setting the renderer's clip rectangle to rect.
func (c *Context) SetClip(rect Rect) {
	c.pushCommand(Command{Type: CommandClip, Rect: rect})
}

// DrawRect emits a filled rectangle, clipped to the current clip rectangle.
func (c *Context) DrawRect(rect Rect, color Color) {
	rect = intersectRects(rect, c.GetClipRect())
	if rect.W > 0 && rect.H > 0 {
		c.pushCommand(Command{Type: CommandRect, Rect: rect, Color: color})
	}
}

// DrawBox emits the four edges of rect as 1px rectangles.
func (c *Context) DrawBox(rect Rect, color Color) {
	c.DrawRect(Rect{X: rect.X + 1, Y: rect.Y, W: rect.W - 2, H: 1}, color)
	c.DrawRect(Rect{X: rect.X + 1, Y: rect.Y + rect.H - 1, W: rect.W - 2, H: 1}, color)
	c.DrawRect(Rect{X: rect.X, Y: rect.Y, W: 1, H: rect.H}, color)
	c.DrawRect(Rect{X: rect.X + rect.W - 1, Y: rect.Y, W: 1, H: rect.H}, color)
}

// DrawText emits a text command for str at pos, automatically inserting clip
// commands when the text is partially clipped.
func (c *Context) DrawText(font Font, str string, pos Vec2, color Color) {
	rect := Rect{X: pos.X, Y: pos.Y, W: c.TextWidth(font, str), H: c.TextHeight(font)}
	clipped := c.checkClip(rect)
	if clipped == clipAll {
		return
	}
	if clipped == clipPart {
		c.SetClip(c.GetClipRect())
	}
	c.pushCommand(Command{Type: CommandText, Str: str, Pos: pos, Color: color, Font: font})
	if clipped != clipNone {
		c.SetClip(unclippedRect)
	}
}

// DrawIcon emits an icon command, automatically inserting clip commands when
// the icon rect is partially clipped.
func (c *Context) DrawIcon(id int, rect Rect, color Color) {
	clipped := c.checkClip(rect)
	if clipped == clipAll {
		return
	}
	if clipped == clipPart {
		c.SetClip(c.GetClipRect())
	}
	c.pushCommand(Command{Type: CommandIcon, Icon: id, Rect: rect, Color: color})
	if clipped != clipNone {
		c.SetClip(unclippedRect)
	}
}
