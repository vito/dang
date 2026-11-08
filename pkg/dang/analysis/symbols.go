package analysis

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// Location is a range of a file in the Source.
type Location struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

func (l Location) String() string {
	return fmt.Sprintf("%s:%s", l.Path, l.Range.Start)
}

// Symbol is what a name at a position names.
type Symbol struct {
	// Name is the name.
	Name string `json:"name"`

	// Kind says what it names.
	Kind MemberKind `json:"kind"`

	// Range is where the name is spelled at the position asked about.
	Range Range `json:"range"`

	// Type is its inferred type, if known.
	Type string `json:"type,omitempty"`

	// Doc is its documentation.
	Doc string `json:"doc,omitempty"`

	// Definition is where it is declared, when that is in the Source. Nil
	// for builtins, imports, schema fields and the like.
	Definition *Location `json:"definition,omitempty"`

	// Member is what Doc would say about it, when it isn't declared in the
	// Source (a builtin, a schema type's field) or is a type's member.
	Member *Member `json:"member,omitempty"`

	// Node is the node at the position: a reference, or the declaration
	// whose name it is.
	Node dang.Node `json:"-"`

	// Decl is the node declaring it, when that is in the Source.
	Decl dang.Node `json:"-"`
}

// ScopeAt returns the type scope code at pos in the file at path sees: the
// file's top level, with the bindings of the declarations, functions and
// blocks around pos laid over it, innermost last. Nil if the file wasn't
// inferred.
func (a *Analysis) ScopeAt(path string, pos Pos) dang.TypeScope {
	fileScope := a.FileScope(path)
	f := a.File(path)
	if fileScope == nil || f == nil || f.Block == nil {
		return fileScope
	}
	enclosing := enclosingScopes(f.Block, pos)
	if len(enclosing) == 0 {
		return fileScope
	}
	scope, ok := fileScope.Clone().(dang.TypeScope)
	if !ok {
		return fileScope
	}
	for _, env := range enclosing {
		for name, scheme := range env.Bindings(dang.PrivateVisibility) {
			scope.Add(name, scheme)
		}
	}
	return scope
}

// enclosingScopes returns the type scopes inference stored on the nodes
// around pos, outermost first.
func enclosingScopes(root dang.Node, pos Pos) []dang.TypeScope {
	var scopes []dang.TypeScope
	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}
		if !nodeContains(n, pos) {
			return true
		}
		switch typed := n.(type) {
		case *dang.ObjectDecl:
			if typed.Inferred != nil {
				scopes = append(scopes, typed.Inferred)
			}
		case *dang.FunDecl:
			if typed.InferredScope != nil {
				scopes = append(scopes, typed.InferredScope)
			}
		case *dang.FileBlock:
			if typed.TypeScope != nil {
				scopes = append(scopes, typed.TypeScope)
			}
		case *dang.Block:
			if typed.TypeScope != nil {
				scopes = append(scopes, typed.TypeScope)
			}
		case *dang.ObjectLiteral:
			if typed.Mod != nil {
				scopes = append(scopes, typed.Mod)
			}
		case *dang.ObjectSelection:
			if typed.Inferred != nil {
				scopes = append(scopes, typed.Inferred)
			}
		}
		return true
	})
	return scopes
}

// nodeContains reports whether pos is within n's source, end included.
func nodeContains(n dang.Node, pos Pos) bool {
	loc := n.GetSourceLocation()
	if loc == nil {
		return false
	}
	return locRange(loc).containsIncl(pos)
}

func (r Range) containsIncl(p Pos) bool {
	return !p.before(r.Start) && !r.End.before(p)
}

func (r Range) within(o Range) bool {
	return !r.Start.before(o.Start) && !o.End.before(r.End)
}

func (p Pos) before(q Pos) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Column < q.Column
}

// Inspect returns what the name at pos in the file at path names: a
// reference, resolved through the scopes around it (so a local shadowing an
// outer binding resolves to the local), the name of a declaration, or a
// field selected from a value.
func (a *Analysis) Inspect(path string, pos Pos) (*Symbol, error) {
	idx := a.symbols()
	for _, r := range idx.refs {
		if r.path == path && r.rng.containsIncl(pos) {
			return a.refSymbol(path, r), nil
		}
	}
	for _, d := range idx.decls {
		if d.path == path && d.rng.containsIncl(pos) {
			return a.declSymbol(d, d.rng, d.node), nil
		}
	}
	for _, s := range idx.selects {
		if s.path == path && s.rng.containsIncl(pos) {
			if sym := a.selectSymbol(s); sym != nil {
				return sym, nil
			}
		}
	}
	return nil, fmt.Errorf("%s:%s: no symbol", path, pos)
}

// References returns where the declaration of what the name at pos names is
// spelled: the declaration itself, then every reference resolving to it,
// across the analyzed files. Nil if the name isn't declared in the Source.
func (a *Analysis) References(path string, pos Pos) []Location {
	idx := a.symbols()
	var target *decl
	for _, r := range idx.refs {
		if r.path == path && r.rng.containsIncl(pos) {
			target = r.decl
			break
		}
	}
	if target == nil {
		for _, d := range idx.decls {
			if d.path == path && d.rng.containsIncl(pos) {
				target = d
				break
			}
		}
	}
	if target == nil {
		return nil
	}
	locs := []Location{{Path: target.path, Range: target.rng}}
	for _, r := range idx.refs {
		if r.decl == target {
			locs = append(locs, Location{Path: r.path, Range: r.rng})
		}
	}
	return locs
}

func (a *Analysis) refSymbol(path string, r *ref) *Symbol {
	if r.decl != nil {
		sym := a.declSymbol(r.decl, r.rng, r.sym)
		if t := r.sym.GetInferredType(); t != nil {
			sym.Type = t.String()
			if sym.Kind == KindValue {
				if _, ok := t.(*hm.FunctionType); ok {
					sym.Kind = KindFunction
				}
			}
		}
		return sym
	}
	sym := &Symbol{Name: r.sym.Name, Kind: KindValue, Range: r.rng, Node: r.sym}
	if t := r.sym.GetInferredType(); t != nil {
		sym.Type = t.String()
		if _, ok := t.(*hm.FunctionType); ok {
			sym.Kind = KindFunction
		}
	}
	if scope := a.ScopeAt(path, r.rng.Start); scope != nil {
		if m, ok := Lookup(scope, r.sym.Name); ok {
			sym.Member = &m
			sym.Doc = m.Doc
			sym.Kind = m.Kind
		}
	}
	return sym
}

func (a *Analysis) declSymbol(d *decl, rng Range, node dang.Node) *Symbol {
	sym := &Symbol{
		Name:       d.name,
		Kind:       d.kind,
		Range:      rng,
		Doc:        d.doc,
		Definition: &Location{Path: d.path, Range: d.rng},
		Node:       node,
		Decl:       d.node,
	}
	if t := d.node.GetInferredType(); t != nil {
		sym.Type = t.String()
	}
	return sym
}

func (a *Analysis) selectSymbol(s *selectRef) *Symbol {
	recv := s.sel.Receiver.GetInferredType()
	if recv == nil {
		return nil
	}
	mod := methodScope(recv)
	if mod == nil {
		return nil
	}
	name := s.sel.Field.Name
	m, ok := Lookup(mod, name)
	if !ok {
		return nil
	}
	if typ, ok := mod.(*dang.Type); ok && !typ.IsMeta() {
		m.Receiver = receiverName(mod)
	}
	sym := &Symbol{
		Name:   name,
		Kind:   m.Kind,
		Range:  s.rng,
		Type:   m.Type,
		Doc:    m.Doc,
		Member: &m,
		Node:   s.sel,
	}
	if t := s.sel.GetInferredType(); t != nil {
		sym.Type = t.String()
	}
	// A member of a type declared in the Source: point at its declaration.
	if typ, ok := mod.(*dang.Type); ok {
		d := a.symbols().members[memberKey{typ, name}]
		if d == nil && typ.Canonical != nil {
			d = a.symbols().members[memberKey{typ.Canonical, name}]
		}
		if d != nil {
			sym.Definition = &Location{Path: d.path, Range: d.rng}
			sym.Decl = d.node
		}
	}
	return sym
}

// decl is a declaration of a name in the Source.
type decl struct {
	name string
	kind MemberKind
	doc  string
	path string
	rng  Range // of the name
	node dang.Node
	// value is the range of a non-function declaration's own value, where
	// its name still refers to an outer binding.
	value *Range
}

// ref is a reference to a name, a *dang.Symbol in value position.
type ref struct {
	path string
	sym  *dang.Symbol
	rng  Range
	decl *decl // nil if not declared in the Source
}

// selectRef is a field selected from a value: recv.name.
type selectRef struct {
	path string
	sel  *dang.Select
	rng  Range // of the field's name
}

// lexicalScope is a region of a file whose declarations its code can name.
type lexicalScope struct {
	path string
	rng  Range
	// sequential scopes (a block's) declare names for the code after them;
	// the others (a file, a type, a function's parameters) for all of it.
	sequential bool
	decls      []*decl
}

type memberKey struct {
	typ  *dang.Type
	name string
}

type symbolIndex struct {
	decls   []*decl
	refs    []*ref
	selects []*selectRef
	scopes  []*lexicalScope
	files   map[string]*lexicalScope // each file's top level
	members map[memberKey]*decl      // the members of types declared in the Source
}

// symbols indexes the analyzed files' declarations and references, once.
func (a *Analysis) symbols() *symbolIndex {
	a.symbolsOnce.Do(func() {
		a.symbolIdx = buildSymbols(a.Files)
	})
	return a.symbolIdx
}

var wholeFile = Range{Start: Pos{Line: 1, Column: 1}, End: Pos{Line: math.MaxInt, Column: math.MaxInt}}

func buildSymbols(files []*File) *symbolIndex {
	idx := &symbolIndex{
		files:   map[string]*lexicalScope{},
		members: map[memberKey]*decl{},
	}
	for _, f := range files {
		if f.Block != nil {
			idx.index(f.Path, f.Block)
		}
	}
	for _, r := range idx.refs {
		r.decl = idx.resolve(r)
	}
	return idx
}

func (idx *symbolIndex) addScope(path string, rng Range, sequential bool, forms []dang.Node) *lexicalScope {
	s := &lexicalScope{path: path, rng: rng, sequential: sequential}
	for _, form := range forms {
		s.decls = append(s.decls, idx.declare(path, form)...)
	}
	idx.scopes = append(idx.scopes, s)
	return s
}

func (idx *symbolIndex) index(path string, file *dang.FileBlock) {
	idx.files[path] = idx.addScope(path, wholeFile, false, file.Forms)

	owned := map[*dang.Block]bool{}
	file.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}
		loc := n.GetSourceLocation()
		switch n := n.(type) {
		case *dang.Symbol:
			if n.Loc != nil {
				idx.refs = append(idx.refs, &ref{path: path, sym: n, rng: nameRange(n.Loc, n.Name)})
			}
		case *dang.Select:
			if n.Field != nil && n.Field.Loc != nil {
				idx.selects = append(idx.selects, &selectRef{path: path, sel: n, rng: nameRange(n.Field.Loc, n.Field.Name)})
			}
		case *dang.ObjectDecl:
			if loc == nil || n.Value == nil {
				break
			}
			owned[n.Value] = true
			s := idx.addScope(path, locRange(loc), false, n.Value.Forms)
			if typ := n.Inferred; typ != nil {
				for _, d := range s.decls {
					idx.members[memberKey{typ, d.name}] = d
					if typ.Canonical != nil {
						idx.members[memberKey{typ.Canonical, d.name}] = d
					}
				}
			}
		case *dang.SelfBlockDecl:
			if loc == nil || n.BodyBlock == nil {
				break
			}
			// Statics name each other in any order.
			owned[n.BodyBlock] = true
			idx.addScope(path, locRange(loc), false, n.BodyBlock.Forms)
		case *dang.FunDecl:
			if loc != nil {
				idx.addScope(path, locRange(loc), false, params(n.Args, n.BlockParam))
			}
		case *dang.NewConstructorDecl:
			if loc != nil {
				idx.addScope(path, locRange(loc), false, params(n.Args, n.BlockParam))
			}
		case *dang.BlockArg:
			if loc != nil {
				idx.addScope(path, locRange(loc), false, params(n.Args, nil))
			}
		case *dang.ObjectLiteral:
			if loc != nil {
				idx.addScope(path, locRange(loc), false, params(n.Fields, nil))
			}
		case *dang.Block:
			if loc != nil && !owned[n] {
				idx.addScope(path, locRange(loc), true, n.Forms)
			}
		}
		return true
	})
}

func params(args []*dang.FieldDecl, block *dang.FieldDecl) []dang.Node {
	var nodes []dang.Node
	for _, arg := range args {
		nodes = append(nodes, arg)
	}
	if block != nil {
		nodes = append(nodes, block)
	}
	return nodes
}

// declare returns the declarations a form makes.
func (idx *symbolIndex) declare(path string, form dang.Node) []*decl {
	named := func(sym *dang.Symbol, kind MemberKind, doc string) []*decl {
		if sym == nil || sym.Loc == nil {
			return nil
		}
		d := &decl{name: sym.Name, kind: kind, doc: doc, path: path, rng: nameRange(sym.Loc, sym.Name), node: form}
		idx.decls = append(idx.decls, d)
		return []*decl{d}
	}
	switch f := form.(type) {
	case *dang.FieldDecl:
		kind := KindValue
		value := uncoerced(f.Value)
		if _, ok := value.(*dang.FunDecl); ok {
			kind = KindFunction
		}
		ds := named(f.Name, kind, f.DocString)
		if len(ds) > 0 && kind == KindValue && value != nil {
			if loc := value.GetSourceLocation(); loc != nil {
				rng := locRange(loc)
				ds[0].value = &rng
			}
		}
		return ds
	case *dang.ObjectDecl:
		return named(f.Name, KindType, f.DocString)
	case *dang.InterfaceDecl:
		return named(f.Name, KindInterface, f.DocString)
	case *dang.EnumDecl:
		return named(f.Name, KindEnum, f.DocString)
	case *dang.ScalarDecl:
		return named(f.Name, KindScalar, f.DocString)
	case *dang.UnionDecl:
		return named(f.Name, KindUnion, f.DocString)
	}
	return nil
}

// uncoerced looks through the Coerce nodes inference wraps around values
// flowing into typed contexts, such as a field's value.
func uncoerced(node dang.Node) dang.Node {
	for {
		c, ok := node.(*dang.Coerce)
		if !ok {
			return node
		}
		node = c.Expr
	}
}

// resolve finds the declaration a reference names: the nearest scope
// around it declaring the name (for a block, before the reference), then
// the top level of the other files.
func (idx *symbolIndex) resolve(r *ref) *decl {
	at := r.rng.Start
	var best *lexicalScope
	var found *decl
	for _, s := range idx.scopes {
		if s.path != r.path || !s.rng.containsIncl(at) {
			continue
		}
		if best != nil && !s.rng.within(best.rng) {
			continue
		}
		if d := s.lookup(r.sym.Name, r.path, at); d != nil {
			best, found = s, d
		}
	}
	if found != nil {
		return found
	}
	paths := make([]string, 0, len(idx.files))
	for p := range idx.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == r.path {
			continue
		}
		if d := idx.files[p].lookup(r.sym.Name, r.path, at); d != nil {
			return d
		}
	}
	return nil
}

// lookup finds the declaration of name in s that a reference at pos in
// path sees.
func (s *lexicalScope) lookup(name, path string, at Pos) *decl {
	var found *decl
	for _, d := range s.decls {
		if d.name != name {
			continue
		}
		if d.path == path && d.value != nil && d.value.containsIncl(at) {
			// Its own value names what's outside it.
			continue
		}
		if s.sequential {
			if !d.rng.Start.before(at) {
				continue
			}
			found = d // the latest before the reference
			continue
		}
		return d
	}
	return found
}

// symbolsState is embedded in Analysis.
type symbolsState struct {
	symbolsOnce sync.Once
	symbolIdx   *symbolIndex
}
