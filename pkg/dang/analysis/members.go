package analysis

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// MemberKind says what a member is.
type MemberKind string

const (
	// KindFunction is a callable binding: a function, a method, or a field
	// computed by a block.
	KindFunction MemberKind = "function"
	// KindValue is any other binding: a variable, a stored field, an enum
	// value.
	KindValue MemberKind = "value"

	// The kinds of named types, and of bindings that name one (a type's name
	// used as a value, an import's namespace).
	KindType      MemberKind = "type"
	KindInterface MemberKind = "interface"
	KindEnum      MemberKind = "enum"
	KindScalar    MemberKind = "scalar"
	KindUnion     MemberKind = "union"
	KindInput     MemberKind = "input"
)

// Member is something that can be named on a value or type, or in a scope.
type Member struct {
	// Name is the member's name.
	Name string `json:"name"`

	// Receiver names the type the member belongs to, e.g. "List" or
	// "Container". Empty for a member of a scope.
	Receiver string `json:"receiver,omitempty"`

	Kind MemberKind `json:"kind"`

	// Static marks a member of a type's name rather than of its values, e.g.
	// JSON.encode.
	Static bool `json:"static,omitempty"`

	// TypeOnly marks a named type that has no value of the same name, so it
	// can only be named in type position (e.g. an input type).
	TypeOnly bool `json:"typeOnly,omitempty"`

	// Builtin marks a member Dang itself provides.
	Builtin bool `json:"builtin,omitempty"`

	// Args are a function's arguments.
	Args []Arg `json:"args,omitempty"`

	// Block is the block a function takes, if any.
	Block *Block `json:"block,omitempty"`

	// Type is the member's type: a function's return type, or a type's name.
	Type string `json:"type,omitempty"`

	// TypeParams is the type parameter list of a generic member whose type
	// variables are bounded, as a declaration writes it: "[a: Numeric]"
	// for List.sum, "[a, k: Ordered]" for List.sortBy. Empty when no bound
	// would show, since type variables are generic implicitly.
	TypeParams string `json:"typeParams,omitempty"`

	// Doc is the member's documentation.
	Doc string `json:"doc,omitempty"`

	// Deprecated, when non-empty, is why the member is deprecated.
	Deprecated string `json:"deprecated,omitempty"`

	// Replacement names what supersedes a deprecated builtin, when there is
	// a mechanical replacement (e.g. "JSON.encode" for toJSON).
	Replacement string `json:"replacement,omitempty"`

	// Example is a small runnable Dang snippet using a builtin.
	Example string `json:"example,omitempty"`

	// HMType is the member's type as inferred, when it is a binding.
	HMType hm.Type `json:"-"`

	// Scope is what the member leads to, for browsing: the type a value or
	// function result has members of, or the type a type name names. Nil
	// when that has no members.
	Scope dang.TypeScope `json:"-"`

	// scheme is the binding's scheme, which identifies it however it is
	// reached (e.g. Test.featured and an unqualified import of it).
	scheme *hm.Scheme
}

// Arg is an argument of a function, or a parameter of a block.
type Arg struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Default is the argument's default value, when known.
	Default string `json:"default,omitempty"`
	Doc     string `json:"doc,omitempty"`
}

// Block describes the block a function takes.
type Block struct {
	Args []Arg `json:"args,omitempty"`
	// Type is what the block returns.
	Type string `json:"type"`
}

// Qualified returns the member's name qualified by its receiver, e.g.
// "List.reduce".
func (m Member) Qualified() string {
	if m.Receiver == "" {
		return m.Name
	}
	return m.Receiver + "." + m.Name
}

// Signature renders how the member is used, e.g.
//
//	reduce(initial: b) { acc: b, item: a => b }: b
//	sum[a: Numeric]: a
//	name: String!
//	type Container
func (m Member) Signature() string {
	switch m.Kind {
	case KindFunction:
		var sig strings.Builder
		sig.WriteString(m.Name)
		sig.WriteString(m.TypeParams)
		if len(m.Args) > 0 {
			sig.WriteString("(")
			for i, arg := range m.Args {
				if i > 0 {
					sig.WriteString(", ")
				}
				sig.WriteString(arg.Name + ": " + arg.Type)
				if arg.Default != "" {
					sig.WriteString(" = " + arg.Default)
				}
			}
			sig.WriteString(")")
		}
		if m.Block != nil {
			sig.WriteString(" {")
			for i, arg := range m.Block.Args {
				if i > 0 {
					sig.WriteString(",")
				}
				sig.WriteString(" " + arg.Name + ": " + arg.Type)
			}
			sig.WriteString(" => " + m.Block.Type + " }")
		}
		if m.Type != "" {
			sig.WriteString(": " + m.Type)
		}
		return sig.String()
	case KindValue:
		return m.Name + ": " + m.TypeString()
	default:
		return string(m.Kind) + " " + m.Name
	}
}

// QualifiedSignature is Signature qualified by the member's receiver, e.g.
// "List.sum[a: Numeric]: a".
func (m Member) QualifiedSignature() string {
	if m.Receiver == "" {
		return m.Signature()
	}
	return m.Receiver + "." + m.Signature()
}

// SelectedMember returns the member a selection (`xs.sum`, or the callee of
// `xs.sort(descending: true)`) names, by its receiver's inferred type.
func SelectedMember(node dang.Node) (Member, bool) {
	if call, ok := node.(*dang.FunCall); ok {
		node = call.Fun
	}
	sel, ok := node.(*dang.Select)
	if !ok || sel.Field == nil {
		return Member{}, false
	}
	recv := sel.Receiver.GetInferredType()
	if recv == nil {
		return Member{}, false
	}
	mod := methodScope(recv)
	if mod == nil {
		return Member{}, false
	}
	m, ok := Lookup(mod, sel.Field.Name)
	if !ok {
		return Member{}, false
	}
	if typ, ok := mod.(*dang.Type); ok && !typ.IsMeta() {
		m.Receiver = receiverName(mod)
	}
	return m, true
}

// TypeString renders the member's type (a function's return type) after
// the type parameters its bounds need, e.g. "[a: Numeric] a".
func (m Member) TypeString() string {
	if m.TypeParams == "" {
		return m.Type
	}
	return m.TypeParams + " " + m.Type
}

// Detail renders the member's type for a completion: a binding's whole
// type, else what TypeString does, with the bounds of its type variables.
func (m Member) Detail() string {
	if m.HMType != nil {
		return dang.FormatType(m.HMType)
	}
	return m.TypeString()
}

// MemberList is what Members lists.
type MemberList struct {
	// Of is what was listed, as given; empty for a scope.
	Of string `json:"of,omitempty"`
	// Type is the type whose members are listed; empty for a scope.
	Type    string   `json:"type,omitempty"`
	Members []Member `json:"members"`
}

// Members lists what can be named on of, in scope: an expression (e.g.
// `["a"]`, `ctr.withExec(["ls"])`), whose type is inferred without
// evaluating it, or a type name (e.g. "List", "JSON", "Test.ServerInfo"),
// whose values' members are listed along with the static members of its
// name. Empty of lists the scope itself (see ScopeMembers).
func Members(ctx context.Context, scope dang.TypeScope, of string) (*MemberList, error) {
	of = strings.TrimSpace(of)
	if of == "" {
		return &MemberList{Members: ScopeMembers(scope)}, nil
	}
	if named, ok := lookupTypeName(scope, of); ok {
		return &MemberList{
			Of:      of,
			Type:    named.Name(),
			Members: typeNameMembers(of, named),
		}, nil
	}
	if m, ok := preludeTypeMember(scope, of); ok {
		// A Prelude type that is not a module (Decoded) has no members:
		// its data is read at a type first.
		return &MemberList{Of: of, Type: m.Type, Members: []Member{}}, nil
	}
	t, err := inferExpr(ctx, scope, of)
	if err != nil {
		return nil, err
	}
	return &MemberList{
		Of:      of,
		Type:    dang.FormatType(t),
		Members: TypeMembers(t),
	}, nil
}

// Doc returns the member a qualified name names, in scope: a member of the
// scope (e.g. "print", or a declaration), or a member of a type or of a
// value's type (e.g. "List.reduce", "JSON.encode", "Container.withExec",
// "ctr.withExec").
func Doc(ctx context.Context, scope dang.TypeScope, qualified string) (*Member, error) {
	qualified = strings.TrimSpace(qualified)
	recv, name, found := cutLast(qualified, ".")
	if !found {
		if m, ok := Lookup(scope, qualified); ok {
			return &m, nil
		}
		return nil, fmt.Errorf("%q not found", qualified)
	}
	list, err := Members(ctx, scope, recv)
	if err != nil {
		return nil, err
	}
	for _, m := range list.Members {
		if m.Name == name {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("%s has no member %q", list.Type, name)
}

// cutLast is strings.Cut on the last occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// ScopeMembers lists what code in scope can name: its bindings, private ones
// included (a module's `let`s, a function's locals), and its named types.
func ScopeMembers(scope dang.TypeScope) []Member {
	return sortMembers(scopeMembers(scope, dang.PrivateVisibility, false))
}

// TypeMembers lists the public members of values of type t: fields and
// methods, the builtin methods of strings, numbers, lists and maps, and the
// named types nested in it (e.g. an import's types). An enum value has
// none; its type's name holds the enum's values.
func TypeMembers(t hm.Type) []Member {
	return sortMembers(typeMembers(t))
}

// NamedTypeMembers lists the public members a named type holds: an object's
// fields and methods, an enum's values, a namespace's (an import's, a
// builtin module's) fields and types.
func NamedTypeMembers(named dang.TypeScope) []Member {
	if named == nil {
		return nil
	}
	members := scopeMembers(named, dang.PublicVisibility, true)
	for i := range members {
		members[i].Receiver = receiverName(named)
	}
	return sortMembers(members)
}

// Lookup returns the member of scope named name: a binding (whatever its
// visibility), a builtin method of a builtin type, or a named type.
func Lookup(scope dang.TypeScope, name string) (Member, bool) {
	if scope == nil {
		return Member{}, false
	}
	if scheme, ok := scope.SchemeOf(name); ok {
		if m, ok := bindingMember(scope, name, scheme); ok {
			return m, true
		}
	}
	if mod, ok := scope.(*dang.Type); ok {
		if def, ok := builtinMember(mod, name); ok {
			return defMember(def, mod), true
		}
	}
	if named, ok := scope.NamedType(name); ok {
		return namedTypeMember(name, named), true
	}
	if m, ok := preludeTypeMember(scope, name); ok {
		return m, true
	}
	return Member{}, false
}

// preludeTypeMember describes a Prelude type that is not a module
// (Decoded), which scope's NamedType can't find, when scope is one that sees
// the Prelude: a lexical scope, not a type's or an import's namespace.
func preludeTypeMember(scope dang.TypeScope, name string) (Member, bool) {
	pt, ok := dang.PreludeType(name)
	if !ok || !seesPrelude(scope) {
		return Member{}, false
	}
	if _, shadowed := scope.NamedType(name); shadowed {
		return Member{}, false
	}
	return Member{
		Name: name,
		Kind: KindType,
		Type: pt.Name(),
		Doc:  dang.PreludeTypeDoc(name),
	}, true
}

// seesPrelude reports whether scope resolves names through the Prelude, as a
// lexical scope does; a type's or an import's namespace holds only its own.
// A scope sees the Prelude when one of the Prelude's own object or interface
// types (not a GraphQL builtin scalar, which a schema's namespace has too)
// resolves through it unshadowed.
func seesPrelude(scope dang.TypeScope) bool {
	preludeScope() // loads the Prelude
	for name, t := range dang.Prelude.NamedTypes() {
		mod, ok := t.(*dang.Type)
		if !ok || (mod.Kind != dang.ObjectKind && mod.Kind != dang.InterfaceKind) {
			continue
		}
		if got, ok := scope.NamedType(name); ok && got == t {
			return true
		}
	}
	return false
}

// scopeMembers lists scope's bindings of at least the given visibility, the
// builtin methods its type has (for a builtin type), and its named types, in
// binding order.
//
// With own, only the scope's own members are listed: a type's or namespace's.
// That leaves out what an import's namespace sees of the prelude through its
// lexical chain, since Test.print isn't a member of Test.
func scopeMembers(scope dang.TypeScope, visibility dang.Visibility, own bool) []Member {
	if scope == nil {
		return nil
	}
	prelude := preludeScope()
	var members []Member
	seen := map[string]bool{}
	for name, scheme := range scope.Bindings(visibility) {
		if seen[name] || dang.IsInternalName(name) {
			continue
		}
		if own {
			if s, ok := prelude.SchemeOf(name); ok && s == scheme {
				continue
			}
		}
		if m, ok := bindingMember(scope, name, scheme); ok {
			seen[name] = true
			members = append(members, m)
		}
	}
	if mod, ok := scope.(*dang.Type); ok {
		dang.ForEachMethod(mod, func(def dang.BuiltinDef) {
			if !seen[def.Name] {
				seen[def.Name] = true
				members = append(members, defMember(def, mod))
			}
		})
	}
	var types []Member
	for name, named := range scope.NamedTypes() {
		if seen[name] {
			continue
		}
		if own {
			if t, ok := prelude.NamedType(name); ok && t == named {
				continue
			}
		}
		seen[name] = true
		m := namedTypeMember(name, named)
		m.TypeOnly = true
		types = append(types, m)
	}
	if !own {
		for _, name := range dang.PreludeTypeNames() {
			if seen[name] {
				continue
			}
			if m, ok := preludeTypeMember(scope, name); ok {
				seen[name] = true
				m.TypeOnly = true
				types = append(types, m)
			}
		}
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	return append(members, types...)
}

// preludeScope is a scope with nothing but the prelude in it.
var preludeScope = sync.OnceValue(func() dang.TypeScope {
	return dang.NewPreludeTypeScope("")
})

// typeMembers lists the public members of values of type t, in binding
// order.
func typeMembers(t hm.Type) []Member {
	mod := methodScope(t)
	if mod == nil {
		return nil
	}
	// An enum value has no members; its enum's values belong to the enum's
	// name (its meta-type).
	if typ, ok := mod.(*dang.Type); ok && typ.Kind == dang.EnumKind {
		return nil
	}
	members := scopeMembers(mod, dang.PublicVisibility, true)
	for i := range members {
		members[i].Receiver = receiverName(mod)
	}
	return members
}

// typeNameMembers lists the members of the type a type name names: its
// values' members (for an enum, its values), then the static members of the
// name itself, e.g. what a `self { }` block declares.
func typeNameMembers(name string, named dang.TypeScope) []Member {
	members := scopeMembers(named, dang.PublicVisibility, true)
	seen := map[string]bool{}
	for _, m := range members {
		seen[m.Name] = true
	}
	for _, m := range staticMembers(named) {
		if !seen[m.Name] {
			seen[m.Name] = true
			members = append(members, m)
		}
	}
	for i := range members {
		members[i].Receiver = name
	}
	return sortMembers(members)
}

// staticMembers lists the public static members of a named type: those of
// its meta-type, the type of its name, if it has one.
func staticMembers(named dang.TypeScope) []Member {
	mod, ok := named.(*dang.Type)
	if !ok {
		return nil
	}
	meta := mod.ExistingMetaType()
	if meta == nil {
		return nil
	}
	members := scopeMembers(meta, dang.PublicVisibility, true)
	for i := range members {
		members[i].Static = true
	}
	return members
}

// receiverName names the type a member listing is of, qualified by its
// import (e.g. "Test.ServerInfo").
func receiverName(scope dang.TypeScope) string {
	if mod, ok := scope.(*dang.Type); ok && mod.IsMeta() {
		return mod.InstanceType().String()
	}
	return scope.String()
}

// methodScope returns the scope holding the members of values of type t:
// the type itself for a named type, or the builtin module of a list or map.
func methodScope(t hm.Type) dang.TypeScope {
	switch inner := unwrapNonNull(t).(type) {
	case dang.ListType:
		return dang.ListTypeModule
	case dang.MapType:
		return dang.MapTypeModule
	case dang.TypeScope:
		return inner
	}
	return nil
}

// lookupTypeName resolves a possibly qualified type name (e.g. "List",
// "Test.ServerInfo") in scope.
func lookupTypeName(scope dang.TypeScope, name string) (dang.TypeScope, bool) {
	parts := strings.Split(name, ".")
	for _, part := range parts {
		if !isIdent(part) {
			return nil, false
		}
	}
	current := scope
	for _, part := range parts {
		next, ok := current.NamedType(part)
		if !ok {
			if len(parts) == 1 && part == "Map" {
				return dang.MapTypeModule, true
			}
			return nil, false
		}
		current = next
	}
	return current, true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// inferExpr infers the type of a Dang expression in scope, without
// evaluating it.
func inferExpr(ctx context.Context, scope dang.TypeScope, expr string) (hm.Type, error) {
	parsed, err := dang.Parse("<expr>", []byte(expr), dang.WithVersion(dang.VersionFromContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", expr, err)
	}
	block, ok := parsed.(*dang.FileBlock)
	if !ok || len(block.Forms) == 0 {
		return nil, fmt.Errorf("parse %q: not an expression", expr)
	}
	// Infer in a clone, so that declarations in expr don't leak into scope.
	ctx, _ = dang.WithInferWarningSink(ctx)
	if _, err := dang.InferFormsWithPhases(ctx, block.Forms, scope.Clone(), hm.NewSimpleFresher()); err != nil {
		return nil, inferErrorMessage(err)
	}
	t := block.Forms[len(block.Forms)-1].GetInferredType()
	if t == nil {
		return nil, fmt.Errorf("could not infer the type of %q", expr)
	}
	return t, nil
}

// inferErrorMessage reduces an inference error to its messages, leaving out
// rendered source snippets.
func inferErrorMessage(err error) error {
	diags := errorDiagnostics(err, CodeType, "", func(string) (string, bool) { return "", true }, nil)
	if len(diags) == 0 {
		return err
	}
	msgs := make([]string, len(diags))
	for i, d := range diags {
		msgs[i] = d.Message
	}
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}

// bindingMember describes the binding name of scope.
func bindingMember(scope dang.TypeScope, name string, scheme *hm.Scheme) (Member, bool) {
	t, _ := scheme.Type()
	if t == nil {
		return Member{}, false
	}
	m := Member{
		Name:       name,
		Type:       t.String(),
		TypeParams: dang.TypeParamsOf(t),
		HMType:     t,
		scheme:     scheme,
	}
	if doc, ok := scope.GetDocString(name); ok {
		m.Doc = doc
	}
	m.Deprecated = directiveDeprecation(scope.GetDirectives(name))

	if fn, ok := t.(*hm.FunctionType); ok {
		m.Kind = KindFunction
		m.Args = fnArgs(fn)
		m.Type = typeString(fn.Ret(true))
		if block := fn.Block(); block != nil {
			m.Block = &Block{Args: fnArgs(block), Type: typeString(block.Ret(true))}
		}
		m.Scope = scopeOf(fn.Ret(true))
	} else {
		m.Kind = KindValue
		inner := unwrapType(t)
		if mod, ok := inner.(*dang.Type); ok && mod.IsMeta() {
			// A type's name, bound to its meta-type: it names the type.
			m.Kind = typeKind(mod.InstanceType())
			m.Scope = mod.InstanceType()
		} else if named, ok := scope.NamedType(name); ok && named == inner {
			// A namespace, e.g. an import or a builtin module.
			m.Kind = typeKind(named)
			m.Scope = named
		} else {
			m.Scope = scopeOf(t)
		}
	}

	if def, ok := builtinBinding(scope, name, scheme); ok {
		enrichFromDef(&m, def)
	}
	return m, true
}

// namedTypeMember describes a named type.
func namedTypeMember(name string, named dang.TypeScope) Member {
	m := Member{
		Name:  name,
		Kind:  typeKind(named),
		Type:  named.Name(),
		Doc:   named.GetTypeDocString(),
		Scope: named,
	}
	return m
}

// defMember describes a builtin method of mod that has no binding.
func defMember(def dang.BuiltinDef, mod *dang.Type) Member {
	m := Member{
		Name:     def.Name,
		Receiver: receiverName(mod),
		Kind:     KindFunction,
	}
	var types []hm.Type
	for _, p := range def.ParamTypes {
		m.Args = append(m.Args, Arg{Name: p.Name, Type: typeString(p.Type)})
		types = append(types, p.Type)
	}
	if def.ReturnType != nil {
		m.Type = typeString(def.ReturnType)
		m.Scope = scopeOf(def.ReturnType)
		types = append(types, def.ReturnType)
	}
	if def.BlockType != nil {
		m.Block = &Block{Args: fnArgs(def.BlockType), Type: typeString(def.BlockType.Ret(true))}
		types = append(types, def.BlockType)
	}
	m.TypeParams = dang.TypeParamsOf(types...)
	enrichFromDef(&m, def)
	return m
}

// enrichFromDef adds what a builtin's definition knows to its member: docs
// (static methods have none in scope), default values, deprecation and its
// runnable example.
func enrichFromDef(m *Member, def dang.BuiltinDef) {
	m.Builtin = true
	if m.Doc == "" {
		m.Doc = def.Doc
	}
	if m.Deprecated == "" {
		m.Deprecated = def.Deprecated
	}
	m.Replacement = def.Replacement
	m.Example = def.Example
	for _, p := range def.ParamTypes {
		if p.DefaultValue == nil {
			continue
		}
		for i := range m.Args {
			if m.Args[i].Name == p.Name {
				m.Args[i].Default = valueString(p.DefaultValue)
			}
		}
	}
}

func valueString(v dang.Value) string {
	if s, ok := v.(dang.StringValue); ok {
		return strconv.Quote(s.Val)
	}
	return v.String()
}

// builtinBinding finds the builtin behind binding name of scope, if it is
// one: a method of a builtin type, a static member of a builtin module, or
// (unshadowed) a builtin function.
func builtinBinding(scope dang.TypeScope, name string, scheme *hm.Scheme) (dang.BuiltinDef, bool) {
	if mod, ok := scope.(*dang.Type); ok {
		if def, ok := builtinMember(mod, name); ok {
			return def, true
		}
	}
	def, ok := builtinFunctions()[name]
	if !ok {
		return dang.BuiltinDef{}, false
	}
	// Only the builtin itself, not a declaration that shadows it.
	if prelude, ok := preludeScope().SchemeOf(name); !ok || prelude != scheme {
		return dang.BuiltinDef{}, false
	}
	return def, true
}

// builtinMember finds a builtin member of mod: a method of a builtin type, or
// a static member of a builtin module (mod being the module or its name's
// meta-type).
func builtinMember(mod *dang.Type, name string) (dang.BuiltinDef, bool) {
	if def, ok := dang.LookupMethod(mod, name); ok {
		return def, true
	}
	host := mod
	if host.IsMeta() {
		host = host.InstanceType()
	}
	var found dang.BuiltinDef
	var ok bool
	dang.ForEachStaticMethod(host, func(def dang.BuiltinDef) {
		if !ok && def.Name == name {
			found, ok = def, true
		}
	})
	return found, ok
}

var (
	builtinFunctionsOnce   sync.Once
	builtinFunctionsByName map[string]dang.BuiltinDef
)

func builtinFunctions() map[string]dang.BuiltinDef {
	builtinFunctionsOnce.Do(func() {
		builtinFunctionsByName = map[string]dang.BuiltinDef{}
		dang.ForEachFunction(func(def dang.BuiltinDef) {
			builtinFunctionsByName[def.Name] = def
		})
	})
	return builtinFunctionsByName
}

// directiveDeprecation returns the reason a @deprecated directive gives, or
// "" if there is none.
func directiveDeprecation(directives []*dang.DirectiveApplication) string {
	for _, d := range directives {
		if d.Name != "deprecated" {
			continue
		}
		for _, arg := range d.Args {
			if s, ok := arg.Value.(*dang.String); ok && arg.Key == "reason" {
				return s.Value
			}
		}
		return "deprecated"
	}
	return ""
}

func fnArgs(fn *hm.FunctionType) []Arg {
	rec, ok := fn.Arg().(*dang.RecordType)
	if !ok {
		return nil
	}
	var args []Arg
	for _, field := range rec.Fields {
		t, _ := field.Value.Type()
		arg := Arg{Name: field.Key, Type: typeString(t)}
		if rec.DocStrings != nil {
			arg.Doc = rec.DocStrings[field.Key]
		}
		args = append(args, arg)
	}
	return args
}

func typeString(t hm.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

// typeKind classifies a named type.
func typeKind(scope dang.TypeScope) MemberKind {
	mod, ok := scope.(*dang.Type)
	if !ok {
		return KindType
	}
	if mod.IsMeta() {
		mod = mod.InstanceType()
	}
	switch mod.Kind {
	case dang.EnumKind:
		return KindEnum
	case dang.ScalarKind:
		return KindScalar
	case dang.InterfaceKind:
		return KindInterface
	case dang.UnionKind:
		return KindUnion
	case dang.InputKind:
		return KindInput
	}
	return KindType
}

// scopeOf returns the type a value of type t has members of, looking through
// non-null and list wrappers and type names.
func scopeOf(t hm.Type) dang.TypeScope {
	inner := unwrapType(t)
	if mod, ok := inner.(*dang.Type); ok && mod.IsMeta() {
		return mod.InstanceType()
	}
	if scope, ok := inner.(dang.TypeScope); ok {
		return scope
	}
	return nil
}

func unwrapNonNull(t hm.Type) hm.Type {
	if nn, ok := t.(hm.NonNullType); ok {
		return nn.Type
	}
	return t
}

// unwrapType strips non-null and list wrappers from a type.
func unwrapType(t hm.Type) hm.Type {
	for {
		switch inner := t.(type) {
		case hm.NonNullType:
			t = inner.Type
		case dang.ListType:
			t = inner.Type
		case dang.GraphQLListType:
			t = inner.Type
		default:
			return t
		}
	}
}

// kindOrder sorts members: values and functions first, then types.
var kindOrder = map[MemberKind]int{
	KindFunction:  0,
	KindValue:     0,
	KindType:      1,
	KindInterface: 2,
	KindEnum:      3,
	KindScalar:    4,
	KindUnion:     5,
	KindInput:     6,
}

// sortMembers sorts members by kind, then case-insensitively by name.
func sortMembers(members []Member) []Member {
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return members
}
