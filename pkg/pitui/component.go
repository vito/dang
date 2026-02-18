package pitui

import (
	"reflect"
	"sync/atomic"
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
	Cached   bool   `json:"cached"`
}

// componentName returns a short human-readable name for a component.
func componentName(c Component) string {
	if n, ok := c.(interface{ Name() string }); ok {
		return n.Name()
	}
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.PkgPath() + "." + t.Name()
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
	// range.
	//
	// This field is managed by the framework when using Compo — component
	// authors do not need to set it. Components without Compo should set
	// it to true.
	Dirty bool
}

// ── Compo ──────────────────────────────────────────────────────────────────

// Compo provides automatic render caching and dirty propagation for
// components. Embed it in your component struct:
//
//	type MyWidget struct {
//	    pitui.Compo
//	    // ... your fields ...
//	}
//
// Call Update() when your component's state changes. The framework will
// re-render the component on the next frame. Between Update() calls,
// Render() is skipped entirely and the cached result is reused.
//
// Update() propagates upward through the component tree, so parent
// Containers automatically know a child changed. If the tree is rooted
// in a TUI, Update() also schedules a render automatically.
type Compo struct {
	needsRender   atomic.Bool
	cache         *renderCache
	parent        *Compo
	requestRender func() // set on the root by TUI
}

type renderCache struct {
	result RenderResult
	width  int
}

// Update marks the component as needing re-render on the next frame.
// Propagates upward so parent containers are also marked dirty.
// If the component tree is rooted in a TUI, a render is scheduled
// automatically.
func (c *Compo) Update() {
	c.needsRender.Store(true)
	if c.parent != nil {
		c.parent.Update()
	} else if c.requestRender != nil {
		c.requestRender()
	}
}

// GetCompo returns the embedded Compo. This method is promoted to any
// struct that embeds Compo, allowing the framework to detect and use it.
func (c *Compo) GetCompo() *Compo { return c }

// SetParent sets the parent Compo for upward dirty propagation.
// This is normally managed by Container.AddChild and Slot.Set, but
// can be called manually for components that render children directly
// without using Container/Slot (e.g. a wrapper that delegates to an
// inner component).
func (c *Compo) SetParent(parent *Compo) { c.parent = parent }

// renderComponent renders a child component, using its Compo cache when
// the component is clean and the width hasn't changed. This is the core
// function that makes finalized components O(1).
func renderComponent(ch Component, ctx RenderContext) RenderResult {
	cp := ch.GetCompo()

	if cp.cache != nil && !cp.needsRender.Load() && cp.cache.width == ctx.Width {
		// Cache hit — skip Render entirely.
		if ctx.componentStats != nil {
			*ctx.componentStats = append(*ctx.componentStats, ComponentStat{
				Name:   componentName(ch),
				Lines:  len(cp.cache.result.Lines),
				Cached: true,
			})
		}
		r := cp.cache.result
		r.Dirty = false
		return r
	}

	// Cache miss — render and store.
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
	r.Dirty = true // we rendered fresh content
	cp.cache = &renderCache{result: r, width: ctx.Width}
	cp.needsRender.Store(false)
	return r
}

// Component is the interface all UI components must implement.
// All components must embed Compo to get automatic render caching
// and dirty propagation.
type Component interface {
	// GetCompo returns the embedded Compo. This is provided automatically
	// by embedding pitui.Compo in your struct.
	GetCompo() *Compo

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

// ── Container ──────────────────────────────────────────────────────────────

// Container is a Component that holds child components and renders them
// sequentially (vertical stack). It embeds Compo, so parent containers
// can cache entire subtrees when nothing changes.
//
// Container uses renderComponent for each child, so children with Compo
// that haven't called Update() are skipped entirely.
type Container struct {
	Compo
	Children  []Component
	lineCount int
}

func (c *Container) AddChild(comp Component) {
	c.Children = append(c.Children, comp)
	comp.GetCompo().SetParent(&c.Compo)
	c.Update()
}

func (c *Container) RemoveChild(comp Component) {
	for i, ch := range c.Children {
		if ch == comp {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			comp.GetCompo().SetParent(nil)
			c.Update()
			return
		}
	}
}

func (c *Container) Clear() {
	for _, ch := range c.Children {
		ch.GetCompo().SetParent(nil)
	}
	c.Children = nil
	c.lineCount = 0
	c.Update()
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
	dirty := false
	for _, ch := range c.Children {
		r := renderComponent(ch, ctx)
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
	c.lineCount = len(lines)
	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
		Dirty:  dirty,
	}
}

// ── Slot ───────────────────────────────────────────────────────────────────

// Slot is a component that delegates to a single replaceable child.
// Use it to swap between components (e.g. text input vs spinner)
// without modifying the parent container's child list.
type Slot struct {
	Compo
	child Component
}

// NewSlot creates a Slot with the given initial child.
func NewSlot(child Component) *Slot {
	s := &Slot{}
	s.setChild(child)
	return s
}

// Set replaces the current child.
func (s *Slot) Set(c Component) {
	s.setChild(c)
	s.Update()
}

func (s *Slot) setChild(c Component) {
	if s.child != nil {
		s.child.GetCompo().SetParent(nil)
	}
	s.child = c
	if c != nil {
		c.GetCompo().SetParent(&s.Compo)
	}
}

// Get returns the current child.
func (s *Slot) Get() Component {
	return s.child
}

func (s *Slot) Render(ctx RenderContext) RenderResult {
	if s.child == nil {
		return RenderResult{}
	}
	return renderComponent(s.child, ctx)
}
