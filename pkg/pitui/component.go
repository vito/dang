package pitui

// RenderContext carries everything a component needs to render.
type RenderContext struct {
	// Width is the available width in terminal columns.
	Width int
	// Height is the allocated height in lines. 0 means unconstrained
	// (the component may return as many lines as it wants).
	Height int
}

// CursorPos represents a cursor position within a component's rendered output.
type CursorPos struct {
	Row, Col int
}

// RenderResult is the output of a Component.Render call.
type RenderResult struct {
	// Lines is the rendered content.
	Lines []string

	// Cursor, if non-nil, is where the hardware cursor should be placed,
	// relative to this component's output (Row 0 = first line of Lines).
	Cursor *CursorPos

	// Dirty reports whether this result differs from the previous render.
	// When false, the framework may skip diffing this component's line
	// range. Components that don't cache should always return true.
	Dirty bool
}

// Component is the interface all UI components must implement.
type Component interface {
	// Render produces the visual output within the given constraints.
	Render(ctx RenderContext) RenderResult

	// Invalidate marks the component as needing re-render, clearing any
	// cached state.
	Invalidate()
}

// Interactive is an optional interface for components that accept keyboard
// input when focused.
type Interactive interface {
	Component

	// HandleInput is called with raw terminal input when the component has
	// focus.
	HandleInput(data []byte)
}

// Focusable is an optional interface for components that want to know when
// they gain or lose focus (e.g. to show/hide a cursor).
type Focusable interface {
	SetFocused(focused bool)
}

// Container is a Component that holds child components and renders them
// sequentially (vertical stack).
type Container struct {
	Children []Component
}

func (c *Container) AddChild(comp Component) {
	c.Children = append(c.Children, comp)
}

func (c *Container) RemoveChild(comp Component) {
	for i, ch := range c.Children {
		if ch == comp {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

func (c *Container) Clear() {
	c.Children = nil
}

func (c *Container) Invalidate() {
	for _, ch := range c.Children {
		ch.Invalidate()
	}
}

func (c *Container) Render(ctx RenderContext) RenderResult {
	var lines []string
	var cursor *CursorPos
	dirty := false
	for _, ch := range c.Children {
		r := ch.Render(ctx)
		if r.Cursor != nil {
			cursor = &CursorPos{
				Row: len(lines) + r.Cursor.Row,
				Col: r.Cursor.Col,
			}
		}
		if r.Dirty {
			dirty = true
		}
		lines = append(lines, r.Lines...)
	}
	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
		Dirty:  dirty,
	}
}

// Slot is a component that delegates to a single replaceable child.
// Use it to swap between components (e.g. text input vs spinner)
// without modifying the parent container's child list.
type Slot struct {
	child Component
	dirty bool
}

// NewSlot creates a Slot with the given initial child.
func NewSlot(child Component) *Slot {
	return &Slot{child: child, dirty: true}
}

// Set replaces the current child.
func (s *Slot) Set(c Component) {
	s.child = c
	s.dirty = true
}

// Get returns the current child.
func (s *Slot) Get() Component {
	return s.child
}

func (s *Slot) Render(ctx RenderContext) RenderResult {
	if s.child == nil {
		r := RenderResult{Dirty: s.dirty}
		s.dirty = false
		return r
	}
	r := s.child.Render(ctx)
	r.Dirty = r.Dirty || s.dirty
	s.dirty = false
	return r
}

func (s *Slot) Invalidate() {
	s.dirty = true
	if s.child != nil {
		s.child.Invalidate()
	}
}
