package pitui

// Component is the interface all UI components must implement.
type Component interface {
	// Render produces lines for the given viewport width.
	// Each string is one terminal line. Lines must not exceed width visible
	// columns (use VisibleWidth to measure, Truncate to trim).
	Render(width int) []string

	// Invalidate clears any cached rendering state. Called on theme change
	// or when the component must re-render from scratch.
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

// Focusable is an optional interface for components that display a hardware
// cursor. When focused, the component should emit CursorMarker at the cursor
// position in its Render output. TUI will find this marker and position the
// hardware cursor there.
type Focusable interface {
	SetFocused(focused bool)
}

// CursorMarker is a zero-width APC sequence that terminals ignore.
// Components embed this at the cursor position when focused.
// TUI finds and strips the marker, then positions the hardware cursor there.
const CursorMarker = "\x1b_pi:c\x07"

// Container is a Component that holds child components and renders them
// sequentially.
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

func (c *Container) Render(width int) []string {
	var lines []string
	for _, ch := range c.Children {
		lines = append(lines, ch.Render(width)...)
	}
	return lines
}
