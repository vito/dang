package pitui

import (
	"fmt"
	"reflect"
	"time"
)

// RenderContext carries everything a component needs to render.
type RenderContext struct {
	// Width is the available width in terminal columns.
	Width int
	// Height is the allocated height in lines. 0 means unconstrained
	// (the component may return as many lines as it wants).
	Height int

	// componentStats, when non-nil, collects per-component render
	// metrics. Set by the TUI when debug logging is enabled.
	componentStats *[]ComponentStat
}

// ComponentStat captures render metrics for a single component within
// a frame.
type ComponentStat struct {
	Name     string `json:"name"`
	RenderUs int64  `json:"render_us"`
	Lines    int    `json:"lines"`
}

// componentName returns a short human-readable name for a component.
// Uses the Named interface if available, otherwise the type name from
// reflect (package path stripped for brevity in the dashboard).
func componentName(c Component) string {
	if n, ok := c.(interface{ Name() string }); ok {
		return n.Name()
	}
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return fmt.Sprintf("%s.%s", t.PkgPath(), t.Name())
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
}

// Component is the interface all UI components must implement.
type Component interface {
	// Render produces the visual output within the given constraints.
	Render(ctx RenderContext) RenderResult
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
// sequentially (vertical stack). The framework handles all change detection
// via line-level string comparison — components never need to track or
// report dirtiness.
type Container struct {
	Children []Component

	// lineCount caches the total line count from the most recent render.
	lineCount int
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
	c.lineCount = 0
}

// LineCount returns the total number of lines produced by the most recent
// render. This is useful for positioning overlays relative to content
// height without triggering a re-render.
func (c *Container) LineCount() int {
	return c.lineCount
}

func (c *Container) Render(ctx RenderContext) RenderResult {
	var lines []string
	var cursor *CursorPos
	for _, ch := range c.Children {
		var r RenderResult
		if ctx.componentStats != nil {
			start := time.Now()
			r = ch.Render(ctx)
			elapsed := time.Since(start)
			*ctx.componentStats = append(*ctx.componentStats, ComponentStat{
				Name:     componentName(ch),
				RenderUs: elapsed.Microseconds(),
				Lines:    len(r.Lines),
			})
		} else {
			r = ch.Render(ctx)
		}
		if r.Cursor != nil {
			cursor = &CursorPos{
				Row: len(lines) + r.Cursor.Row,
				Col: r.Cursor.Col,
			}
		}
		lines = append(lines, r.Lines...)
	}
	c.lineCount = len(lines)
	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
	}
}

// Slot is a component that delegates to a single replaceable child.
// Use it to swap between components (e.g. text input vs spinner)
// without modifying the parent container's child list.
type Slot struct {
	child Component
}

// NewSlot creates a Slot with the given initial child.
func NewSlot(child Component) *Slot {
	return &Slot{child: child}
}

// Set replaces the current child.
func (s *Slot) Set(c Component) {
	s.child = c
}

// Get returns the current child.
func (s *Slot) Get() Component {
	return s.child
}

func (s *Slot) Render(ctx RenderContext) RenderResult {
	if s.child == nil {
		return RenderResult{}
	}
	var r RenderResult
	if ctx.componentStats != nil {
		start := time.Now()
		r = s.child.Render(ctx)
		elapsed := time.Since(start)
		*ctx.componentStats = append(*ctx.componentStats, ComponentStat{
			Name:     componentName(s.child),
			RenderUs: elapsed.Microseconds(),
			Lines:    len(r.Lines),
		})
	} else {
		r = s.child.Render(ctx)
	}
	return r
}
