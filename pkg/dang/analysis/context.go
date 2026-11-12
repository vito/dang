package analysis

import (
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/dang"
)

// DiagnosticContext is what a type error about a name already knows the
// answer to, found after inference from the node the error is on: the
// member the name does name, or, when it names nothing, what the receiver
// really has and the closest names. It is never derived from the error's
// message. Analyze finds it only with Options.DiagnosticContext.
type DiagnosticContext struct {
	// Name is the name the erroring selection or reference spells, e.g.
	// "findFirst" for `xs.findFirst { ... }`.
	Name string `json:"name,omitempty"`

	// NameRange is where Name is spelled, for an edit that replaces it.
	NameRange *Range `json:"nameRange,omitempty"`

	// Member is what Name names, when it exists: e.g. the method a call
	// missing its block argument calls, with the signature it needs.
	Member *Member `json:"member,omitempty"`

	// Receiver is the type selected from, when Name isn't a member of it,
	// e.g. "[String!]!".
	Receiver string `json:"receiver,omitempty"`

	// Has lists the receiver's members.
	Has []Member `json:"has,omitempty"`

	// Near lists the members (of the receiver, or of the scope for an
	// unknown name) closest to Name by spelling, closest first.
	Near []Member `json:"near,omitempty"`

	// Query is what Search searched for: Name's camelCase words of three
	// letters or more, e.g. "sort strings" for sortStrings.
	Query string `json:"query,omitempty"`

	// Search lists the members whose names or docs match Query (see
	// Search), best first, leaving out those already in Near: the
	// receiver's, or for an unknown name, everything the scope reaches.
	Search []Member `json:"search,omitempty"`
}

// How many members Near and Search hold at most.
const (
	contextNearLimit   = 3
	contextSearchLimit = 3
)

// errorContext finds the context of an inference error, by the node it is
// on: a selection (`xs.findFirst`), a reference to a name (`sortStrings`),
// or a call of either. path is the Source path of the file it is in, empty
// for a file outside the Source, where only a selection's context is found.
func (a *Analysis) errorContext(e *dang.InferError, path string) *DiagnosticContext {
	switch node := e.Node.(type) {
	case *dang.FunCall:
		switch fun := node.Fun.(type) {
		case *dang.Select:
			return selectContext(fun)
		case *dang.Symbol:
			return a.symbolContext(fun, path)
		}
	case *dang.Select:
		return selectContext(node)
	case *dang.Symbol:
		return a.symbolContext(node, path)
	}
	return nil
}

// selectContext is the context of an error on a selection, by its
// receiver's inferred type.
func selectContext(sel *dang.Select) *DiagnosticContext {
	if sel.Field == nil || sel.Receiver == nil {
		return nil
	}
	recv := sel.Receiver.GetInferredType()
	if recv == nil {
		return nil
	}
	c := &DiagnosticContext{Name: sel.Field.Name}
	if loc := sel.Field.Loc; loc != nil {
		rng := nameRange(loc, sel.Field.Name)
		c.NameRange = &rng
	}
	if m, ok := SelectedMember(sel); ok {
		c.Member = &m
		return c
	}
	has := TypeMembers(recv)
	if len(has) == 0 {
		return nil
	}
	c.Receiver = dang.FormatType(recv)
	c.Has = has
	c.Near = nearest(c.Name, has, contextNearLimit)
	c.Query, c.Search = searchMembers(c.Name, has, c.Near)
	return c
}

// symbolContext is the context of an error on a reference to a name, by the
// scope at the reference. Names the Source declares around the reference
// that it can't see yet (a block's later locals, a declaration's own name in
// its value) are left out: ScopeAt has them, but they are what's missing.
func (a *Analysis) symbolContext(sym *dang.Symbol, path string) *DiagnosticContext {
	if path == "" || sym.Loc == nil {
		return nil
	}
	at := Pos{Line: sym.Loc.Line, Column: sym.Loc.Column}
	scope := a.ScopeAt(path, at)
	if scope == nil {
		return nil
	}
	hidden := a.hiddenAt(path, at)
	rng := nameRange(sym.Loc, sym.Name)
	c := &DiagnosticContext{Name: sym.Name, NameRange: &rng}
	if !hidden[sym.Name] {
		if m, ok := Lookup(scope, sym.Name); ok {
			c.Member = &m
			return c
		}
	}
	var visible []Member
	for _, m := range ScopeMembers(scope) {
		if !hidden[m.Name] {
			visible = append(visible, m)
		}
	}
	c.Near = nearest(sym.Name, visible, contextNearLimit)
	query := searchQuery(sym.Name)
	if query == "" {
		return c
	}
	var hits []Member
	for _, m := range Search(scope, query, contextSearchLimit+len(c.Near)+len(hidden)) {
		if containsMember(c.Near, m) || (m.Receiver == "" && hidden[m.Name]) {
			continue
		}
		hits = append(hits, m)
	}
	if len(hits) > contextSearchLimit {
		hits = hits[:contextSearchLimit]
	}
	if len(hits) > 0 {
		c.Query, c.Search = query, hits
	}
	return c
}

// hiddenAt returns the names the Source declares in the scopes around pos
// that code at pos can't see: a block's locals declared at or after it, and
// a declaration's own name within its value, unless another declaration of
// the name is visible there.
func (a *Analysis) hiddenAt(path string, at Pos) map[string]bool {
	hidden := map[string]bool{}
	visible := map[string]bool{}
	for _, s := range a.symbols().scopes {
		if s.path != path || !s.rng.containsIncl(at) {
			continue
		}
		for _, d := range s.decls {
			ownValue := d.path == path && d.value != nil && d.value.containsIncl(at)
			later := s.sequential && !d.rng.Start.before(at)
			if ownValue || later {
				hidden[d.name] = true
			} else {
				visible[d.name] = true
			}
		}
	}
	for name := range visible {
		delete(hidden, name)
	}
	return hidden
}

// searchQuery is what to search the docs for, for a name that names
// nothing: its camelCase words, leaving out those too short to mean
// anything on their own (`to`, `x`), which match nearly every doc.
func searchQuery(name string) string {
	var words []string
	for _, w := range camelWords(name) {
		if len(w) >= 3 {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// searchMembers ranks members by how well they match name's camelCase
// words, as Search does, leaving out those in near. It returns the query
// searched for, empty if nothing matched.
func searchMembers(name string, members, near []Member) (string, []Member) {
	query := searchQuery(name)
	if query == "" {
		return "", nil
	}
	words := strings.Fields(query)
	type hit struct {
		member Member
		score  int
	}
	var hits []hit
	for _, m := range members {
		if containsMember(near, m) {
			continue
		}
		if score := searchScore(m, query, words); score > 0 {
			hits = append(hits, hit{m, score})
		}
	}
	if len(hits) == 0 {
		return "", nil
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > contextSearchLimit {
		hits = hits[:contextSearchLimit]
	}
	found := make([]Member, len(hits))
	for i, h := range hits {
		found[i] = h.member
	}
	return query, found
}

func containsMember(members []Member, m Member) bool {
	for _, o := range members {
		if o.Qualified() == m.Qualified() {
			return true
		}
	}
	return false
}
