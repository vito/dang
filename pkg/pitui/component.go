package pitui

import (
	"reflect"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
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
//
// Dirty tracking uses a monotonic generation counter rather than a
// boolean flag. Update() increments the counter; renderComponent
// snapshots it before calling Render and records the snapshot
// afterwards. Any concurrent Update() during Render increments the
// counter past the snapshot, guaranteeing a re-render on the next
// frame — no store-ordering subtleties required.
type Compo struct {
	generation  atomic.Int64
	renderedGen int64        // generation when last rendered; render-goroutine only
	cache       *renderCache // only accessed from the render goroutine
	parent      *Compo
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
	c.generation.Add(1)
	if c.parent != nil {
		c.parent.Update()
	} else if c.requestRender != nil {
		c.requestRender()
	}
}

// compo returns the embedded Compo. The unexported method ensures that
// only types embedding Compo can satisfy the Component interface.
func (c *Compo) compo() *Compo { return c }

// setParent sets the parent Compo for upward dirty propagation.
// Managed automatically by Container.AddChild, Slot.Set, and
// RenderChild.
func (c *Compo) setParent(parent *Compo) { c.parent = parent }

// RenderChild renders a child component through this Compo, using the
// framework's render cache. It also wires the child's parent pointer so
// that Update() on the child propagates upward through this component.
//
// Use this instead of calling child.Render(ctx) directly when your
// component wraps another component without using Container or Slot:
//
//	func (w *MyWrapper) Render(ctx pitui.RenderContext) pitui.RenderResult {
//	    return w.RenderChild(w.inner, ctx)
//	}
func (c *Compo) RenderChild(child Component, ctx RenderContext) RenderResult {
	child.compo().parent = c
	return renderComponent(child, ctx)
}

// renderComponent renders a child component, using its Compo cache when
// the component is clean and the width hasn't changed. This is the core
// function that makes finalized components O(1).
//
// Dirty tracking is race-free: the generation counter is snapshotted
// before Render and recorded after. Any concurrent Update() increments
// the counter past the snapshot, so the next renderComponent call sees
// a mismatch and re-renders. No boolean store-ordering issues.
func renderComponent(ch Component, ctx RenderContext) RenderResult {
	cp := ch.compo()

	gen := cp.generation.Load()
	if cp.cache != nil && gen == cp.renderedGen && cp.cache.width == ctx.Width {
		// Cache hit — skip Render entirely.
		if ctx.componentStats != nil {
			*ctx.componentStats = append(*ctx.componentStats, ComponentStat{
				Name:   componentName(ch),
				Lines:  len(cp.cache.result.Lines),
				Cached: true,
			})
		}
		return cp.cache.result
	}

	// Cache miss — render and store. Record the generation we checked,
	// not the current one, so any Update() during Render() is visible
	// as a generation mismatch on the next frame.
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
	cp.cache = &renderCache{result: r, width: ctx.Width}
	cp.renderedGen = gen
	return r
}

// Component is the interface all UI components must implement.
// All components must embed Compo to get automatic render caching
// and dirty propagation.
type Component interface {
	// compo returns the embedded Compo. Unexported to keep it out of
	// the public API; satisfied automatically by embedding Compo.
	compo() *Compo

	// Render produces the visual output within the given constraints.
	Render(ctx RenderContext) RenderResult
}

// Interactive is an optional interface for components that accept keyboard
// input when focused. The TUI decodes raw terminal bytes and dispatches
// typed events; components never see raw bytes.
type Interactive interface {
	Component

	// HandleKeyPress is called with a decoded key press event when the
	// component has focus.
	HandleKeyPress(ev uv.KeyPressEvent)
}

// Pasteable is an optional interface for components that accept pasted
// text (via bracketed paste). If not implemented, paste events are ignored.
type Pasteable interface {
	HandlePaste(ev uv.PasteEvent)
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
	lineCount atomic.Int32
}

func (c *Container) AddChild(comp Component) {
	c.Children = append(c.Children, comp)
	comp.compo().setParent(&c.Compo)
	c.Update()
}

func (c *Container) RemoveChild(comp Component) {
	for i, ch := range c.Children {
		if ch == comp {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			comp.compo().setParent(nil)
			c.Update()
			return
		}
	}
}

func (c *Container) Clear() {
	for _, ch := range c.Children {
		ch.compo().setParent(nil)
	}
	c.Children = nil
	c.lineCount.Store(0)
	c.Update()
}

// LineCount returns the total number of lines produced by the most recent
// render. Safe to call from any goroutine (e.g. input handlers positioning
// overlays relative to content height).
func (c *Container) LineCount() int {
	return int(c.lineCount.Load())
}

func (c *Container) Render(ctx RenderContext) RenderResult {
	var lines []string
	var cursor *CursorPos
	for _, ch := range c.Children {
		r := renderComponent(ch, ctx)
		if r.Cursor != nil {
			cursor = &CursorPos{
				Row: len(lines) + r.Cursor.Row,
				Col: r.Cursor.Col,
			}
		}
		lines = append(lines, r.Lines...)
	}
	c.lineCount.Store(int32(len(lines)))
	return RenderResult{
		Lines:  lines,
		Cursor: cursor,
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
		s.child.compo().setParent(nil)
	}
	s.child = c
	if c != nil {
		c.compo().setParent(&s.Compo)
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
