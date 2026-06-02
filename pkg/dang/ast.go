package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

type Node interface {
	hm.Expression
	hm.Inferer
	GetSourceLocation() *SourceLocation

	// DeclaredSymbols returns the symbols that this node declares (introduces to scope)
	DeclaredSymbols() []string

	// ReferencedSymbols returns the symbols that this node references (depends on)
	ReferencedSymbols() []string

	// SetInferredType stores the inferred type for this node
	SetInferredType(hm.Type)

	// GetInferredType retrieves the inferred type for this node
	GetInferredType() hm.Type

	// Walk recursively visits this node and all its children, calling fn for each node.
	// The callback returns true to continue walking into children, false to skip children.
	Walk(fn func(Node) bool)
}

// InferredTypeHolder is embedded in AST nodes to store inferred types
type InferredTypeHolder struct {
	inferredType hm.Type
}

func (h *InferredTypeHolder) SetInferredType(t hm.Type) {
	h.inferredType = t
}

func (h *InferredTypeHolder) GetInferredType() hm.Type {
	return h.inferredType
}

type Keyed[X any] struct {
	Key        string
	Value      X
	Positional bool // true if this argument was passed positionally
}

type Visibility int

const (
	PrivateVisibility Visibility = iota
	PublicVisibility
)

// autoCallFnType returns the type that should be used for zero-arity function auto-calling
func autoCallFnType(t hm.Type) (hm.Type, bool) {
	// Check if this is a zero-arity function and return its return type.
	// A declared block parameter is required, so functions that expect a block
	// cannot be auto-called by a bare reference.
	if ft, ok := t.(*hm.FunctionType); ok {
		if ft.Block() != nil {
			return t, false
		}
		if rt, ok := ft.Arg().(*RecordType); ok {
			// Check if all fields are optional (no NonNullType fields)
			// Note: This function only has type information, not default value information
			// The actual auto-call decision is made in isAutoCallableFn with FunctionValue
			hasRequiredArgs := false
			for _, field := range rt.Fields {
				if fieldType, _ := field.Value.Type(); fieldType != nil {
					if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
						hasRequiredArgs = true
						break
					}
				}
			}

			if hasRequiredArgs {
				return t, false
			}

			if !hasRequiredArgs {
				// All arguments are optional, return the return type
				return ft.Ret(false), true
			}
		}
	}
	return t, true
}

// isAutoCallableFn checks if a function can be auto-called (has no required arguments)
func isAutoCallableFn(val Value) bool {
	if callable, ok := val.(Callable); ok {
		return callable.IsAutoCallable()
	}
	return false
}

// hasZeroRequiredArgs checks if a GraphQL field has zero required arguments
func hasZeroRequiredArgs(field *introspection.Field) bool {
	if field == nil {
		return false
	}

	// Check if all arguments are optional (nullable or have defaults)
	for _, arg := range field.Args {
		if arg.TypeRef.Kind == "NON_NULL" && arg.DefaultValue == nil {
			// This argument is required (non-null with no default)
			return false
		}
	}

	// All arguments are optional, so this function can be called with zero args
	return true
}

// autoCallFn calls a zero-arity function with empty arguments
func autoCallFn(ctx context.Context, env ValueScope, val Value) (Value, error) {
	// Create a FunCall with empty arguments and delegate to FunCall.Eval
	emptyRecord := Record{}
	funCall := FunCall{
		Fun:  createValueNode(val),
		Args: emptyRecord,
		Loc:  nil,
	}
	return funCall.Eval(ctx, env)
}

func inferNodeWithoutAutoCall(ctx context.Context, env hm.Env, fresh hm.Fresher, node Node) (hm.Type, error) {
	switch n := node.(type) {
	case *Symbol:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return n.Infer(ctx, env, fresh)
	case *Select:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return n.Infer(ctx, env, fresh)
	case *Index:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return n.Infer(ctx, env, fresh)
	case *Grouped:
		t, err := inferNodeWithoutAutoCall(ctx, env, fresh, n.Expr)
		if err == nil {
			n.SetInferredType(t)
		}
		return t, err
	default:
		return node.Infer(ctx, env, fresh)
	}
}

func evalNodeWithoutAutoCall(ctx context.Context, env ValueScope, node Node) (Value, error) {
	switch n := node.(type) {
	case *Symbol:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return EvalNode(ctx, env, n)
	case *Select:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return EvalNode(ctx, env, n)
	case *Index:
		prev := n.AutoCall
		n.AutoCall = false
		defer func() { n.AutoCall = prev }()
		return EvalNode(ctx, env, n)
	case *Grouped:
		return evalNodeWithoutAutoCall(ctx, env, n.Expr)
	default:
		return EvalNode(ctx, env, node)
	}
}

// ValueNode is a simple node that evaluates to a given value
type ValueNode struct {
	InferredTypeHolder
	Val Value
	Loc *SourceLocation
}

func (v *ValueNode) DeclaredSymbols() []string {
	return nil // ValueNodes don't declare anything
}

func (v *ValueNode) ReferencedSymbols() []string {
	return nil // ValueNodes don't reference anything
}

func (v *ValueNode) Body() hm.Expression                { return nil }
func (v *ValueNode) GetSourceLocation() *SourceLocation { return v.Loc }
func (v *ValueNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return v.Val.Type(), nil
}
func (v *ValueNode) Eval(ctx context.Context, env ValueScope) (Value, error) { return v.Val, nil }

func (v *ValueNode) Walk(fn func(Node) bool) {
	fn(v)
}

// createValueNode creates a simple node that evaluates to the given value
func createValueNode(val Value) *ValueNode {
	return &ValueNode{Val: val, Loc: nil}
}

// valuesEqual compares two values for equality
func valuesEqual(left, right Value) bool {
	// Handle null values
	_, leftIsNull := left.(NullValue)
	_, rightIsNull := right.(NullValue)
	if leftIsNull && rightIsNull {
		return true
	}
	if leftIsNull || rightIsNull {
		return false
	}

	// Compare by type
	switch l := left.(type) {
	case StringValue:
		if r, ok := right.(StringValue); ok {
			return l.Val == r.Val
		}
		// Allow comparison with ScalarValue
		if r, ok := right.(ScalarValue); ok {
			return l.Val == r.Val
		}
	case EnumValue:
		// Compare enum values - must be same value
		if r, ok := right.(EnumValue); ok {
			// TODO: test that enums with same constructor but different type are not equal
			//
			// more precisely, that should be caught at type checking time, and the
			// bottom can be simplified
			return l.EnumType == r.EnumType && l.Val == r.Val
		}
	case ScalarValue:
		// Compare scalar values - must be same value and same type
		if r, ok := right.(ScalarValue); ok {
			return l.ScalarType == r.ScalarType && l.Val == r.Val
		}
		// Allow comparison with StringValue
		if r, ok := right.(StringValue); ok {
			return l.Val == r.Val
		}
	case IntValue:
		switch r := right.(type) {
		case IntValue:
			return l.Val == r.Val
		case FloatValue:
			return float64(l.Val) == r.Val
		}
	case FloatValue:
		switch r := right.(type) {
		case IntValue:
			return l.Val == float64(r.Val)
		case FloatValue:
			return l.Val == r.Val
		}
	case BoolValue:
		if r, ok := right.(BoolValue); ok {
			return l.Val == r.Val
		}
	case ListValue:
		if r, ok := right.(ListValue); ok {
			if len(l.Elements) != len(r.Elements) {
				return false
			}
			for i := range l.Elements {
				if !valuesEqual(l.Elements[i], r.Elements[i]) {
					return false
				}
			}
			return true
		}
	}

	// Different types or unsupported comparison
	// TODO: object comparison?
	return false
}

// isTruthy determines if a value should be considered true for assertion purposes
func isTruthy(val Value) bool {
	switch v := val.(type) {
	case BoolValue:
		return v.Val
	case NullValue:
		return false
	case IntValue:
		return v.Val != 0
	case StringValue:
		return v.Val != ""
	case ListValue:
		return len(v.Elements) > 0
	default:
		return true // Other values are considered truthy
	}
}

// OverlayValueScope overlays a primary evaluation scope on a lexical one.
// Reads check primary first, then fall through to lexical; writes
// (Bind/BindLazy/Update) and self all go to primary. It backs every situation
// where one value scope shadows another: an object body / method / constructor
// instance (primary = the object/self, lexical = the closure), and a file's
// scope (primary = the directory env so sibling declarations stay visible,
// lexical = the file's own imports).
type OverlayValueScope struct {
	primary ValueScope // First lookup + target for all writes
	lexical ValueScope // Fallback lookup (closure / file imports)
}

func (c OverlayValueScope) Lookup(ctx context.Context, name string) (Value, bool, error) {
	// First check the primary environment (receiver/parameters)
	// This allows parameters and receiver fields to shadow lexical scope
	if val, found, err := c.primary.Lookup(ctx, name); err != nil {
		return nil, found, err
	} else if found {
		return val, true, nil
	}
	// Then check the lexical environment for fallback
	return c.lexical.Lookup(ctx, name)
}

func (c OverlayValueScope) Has(name string) bool {
	return c.primary.Has(name) || c.lexical.Has(name)
}

func (c OverlayValueScope) BindLazy(name string, init func(ctx context.Context) (Value, error), visibility Visibility) {
	c.primary.BindLazy(name, init, visibility)
}

func (c OverlayValueScope) LookupLocal(name string) (Value, bool) {
	// Only consult primary: when lexical holds file-scoped imports, they must
	// not be treated as already-defined "local" bindings by FieldDecl.Eval —
	// that would silently swallow declarations sharing a name with an import.
	return c.primary.LookupLocal(name)
}

func (c OverlayValueScope) Bindings(vis Visibility) []Keyed[Value] {
	var bs []Keyed[Value]
	seen := map[string]bool{}
	for _, kv := range c.primary.Bindings(vis) {
		bs = append(bs, kv)
		seen[kv.Key] = true
	}
	for _, kv := range c.lexical.Bindings(vis) {
		if seen[kv.Key] {
			continue
		}
		bs = append(bs, kv)
	}
	return bs
}

// MarshalJSON serializes the primary scope, including private fields so that
// state can be retained.
func (m OverlayValueScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.primary)
}

var _ ValueScope = OverlayValueScope{}

func (c OverlayValueScope) String() string {
	return fmt.Sprintf("OverlayValueScope{primary: %v, lexical: %v}", c.primary, c.lexical)
}

func (c OverlayValueScope) Type() hm.Type {
	return c.primary.Type()
}

func (c OverlayValueScope) Bind(name string, value Value, visibility Visibility) {
	// All new bindings go to the primary environment (copy-on-write semantics)
	c.primary.Bind(name, value, visibility)
}

func (c OverlayValueScope) Update(name string, value Value) {
	// Delegate to the primary environment for scoping logic
	c.primary.Update(name, value)
}

func (c OverlayValueScope) Visibility(name string) Visibility {
	// Speculative: don't fall back to lexical, we should consider that always private?
	return c.primary.Visibility(name)
}

func (c OverlayValueScope) Derive(sealed bool) ValueScope {
	// Derive the primary environment and keep the same lexical environment
	return OverlayValueScope{
		primary: c.primary.Derive(sealed),
		lexical: c.lexical,
	}
}

// Self returns the dynamic scope from the primary environment
func (c OverlayValueScope) Self() (Value, bool) {
	return c.primary.Self()
}

// MutateSelf sets the dynamic scope in the primary environment
func (c OverlayValueScope) MutateSelf(value Value) {
	c.primary.MutateSelf(value)
}

// EnterSelf creates a fresh dynamic scope cell in the primary environment
func (c OverlayValueScope) EnterSelf(value Value) {
	c.primary.EnterSelf(value)
}

// CreateOverlayValueScope overlays primary on lexical (see OverlayValueScope).
func CreateOverlayValueScope(primary ValueScope, lexical ValueScope) OverlayValueScope {
	return OverlayValueScope{
		primary: primary,
		lexical: lexical,
	}
}

// ConstructorScope is a specialized environment for new() constructor bodies.
// Reads check constructor args first (shadowing fields and outer scope),
// while writes go to the instance so bare assignments like `x = val` work.
type ConstructorScope struct {
	instance ValueScope // Object instance (target for writes)
	args     ValueScope // Constructor arguments (shadow everything on reads)
	closure  ValueScope // Lexical closure (outer scope)

	dynamicScope *DynamicScope
}

func CreateConstructorScope(instance ValueScope, args ValueScope, closure ValueScope) *ConstructorScope {
	return &ConstructorScope{
		instance: instance,
		args:     args,
		closure:  closure,
	}
}

func (e *ConstructorScope) Lookup(ctx context.Context, name string) (Value, bool, error) {
	// Constructor args shadow everything
	if val, found, err := e.args.Lookup(ctx, name); err != nil {
		return nil, found, err
	} else if found {
		return val, true, nil
	}
	// Then instance fields
	if val, found, err := e.instance.Lookup(ctx, name); err != nil {
		return nil, found, err
	} else if found {
		return val, true, nil
	}
	// Then lexical closure
	return e.closure.Lookup(ctx, name)
}

func (e *ConstructorScope) Has(name string) bool {
	return e.args.Has(name) || e.instance.Has(name) || e.closure.Has(name)
}

func (e *ConstructorScope) BindLazy(name string, init func(ctx context.Context) (Value, error), visibility Visibility) {
	e.instance.BindLazy(name, init, visibility)
}

func (e *ConstructorScope) LookupLocal(name string) (Value, bool) {
	return e.instance.LookupLocal(name)
}

func (e *ConstructorScope) Bindings(vis Visibility) []Keyed[Value] {
	var bs []Keyed[Value]
	seen := map[string]bool{}
	for _, kv := range e.args.Bindings(vis) {
		bs = append(bs, kv)
		seen[kv.Key] = true
	}
	for _, kv := range e.instance.Bindings(vis) {
		if !seen[kv.Key] {
			bs = append(bs, kv)
			seen[kv.Key] = true
		}
	}
	for _, kv := range e.closure.Bindings(vis) {
		if !seen[kv.Key] {
			bs = append(bs, kv)
		}
	}
	return bs
}

func (e *ConstructorScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.instance)
}

func (e *ConstructorScope) Bind(name string, value Value, visibility Visibility) {
	e.instance.Bind(name, value, visibility)
}

func (e *ConstructorScope) Update(name string, value Value) {
	// If the name is a constructor arg, reassign there so that
	// subsequent reads (which check args first) see the new value.
	if e.args.Has(name) {
		e.args.Update(name, value)
		return
	}
	// Otherwise reassign on the instance (where fields live)
	e.instance.Update(name, value)
}

func (e *ConstructorScope) Visibility(name string) Visibility {
	return e.instance.Visibility(name)
}

func (e *ConstructorScope) Derive(sealed bool) ValueScope {
	return &ConstructorScope{
		instance:     e.instance.Derive(sealed),
		args:         e.args,
		closure:      e.closure,
		dynamicScope: e.dynamicScope,
	}
}

func (e *ConstructorScope) Self() (Value, bool) {
	if e.dynamicScope != nil && e.dynamicScope.Value != nil {
		return e.dynamicScope.Value, true
	}
	return nil, false
}

func (e *ConstructorScope) MutateSelf(value Value) {
	if e.dynamicScope != nil {
		e.dynamicScope.Value = value
	} else {
		e.dynamicScope = &DynamicScope{Value: value}
	}
}

func (e *ConstructorScope) EnterSelf(value Value) {
	e.dynamicScope = &DynamicScope{Value: value}
}

func (e *ConstructorScope) Type() hm.Type {
	return e.instance.Type()
}

func (e *ConstructorScope) String() string {
	return fmt.Sprintf("ConstructorScope(%s)", e.instance)
}

// OverlayTypeScope is the inference-time analogue of OverlayValueScope: it
// overlays a primary type scope on a lexical one. Reads check primary first,
// then fall through to lexical; new schemes are added to primary. It backs
// object-body inference (primary = the object type), file scope (primary = the
// directory env, lexical = the file's imports), and module-over-prelude.
type OverlayTypeScope struct {
	primary TypeScope // First lookup + where new schemes are added
	lexical TypeScope // Fallback lookup (closure / file imports / prelude)
}

func (c *OverlayTypeScope) SchemeOf(name string) (*hm.Scheme, bool) {
	// First check the primary environment (reopened module/object fields)
	// This allows object fields to have precedence over outer scope variables
	if scheme, found := c.primary.SchemeOf(name); found {
		return scheme, true
	}
	// Then check the lexical environment (current scope) for fallback
	return c.lexical.SchemeOf(name)
}

func (c *OverlayTypeScope) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	// Local scope is the primary environment.
	return c.primary.LocalSchemeOf(name)
}

func (c *OverlayTypeScope) Clone() hm.Env {
	return &OverlayTypeScope{
		primary: c.primary.Clone().(TypeScope),
		lexical: c.lexical, // Keep same lexical environment
	}
}

func (c *OverlayTypeScope) Add(name string, scheme *hm.Scheme) hm.Env {
	c.primary.Add(name, scheme)
	return c
}

func (c *OverlayTypeScope) SetValueOrigin(name string, origin BindingOrigin) {
	c.primary.SetValueOrigin(name, origin)
}

func (c *OverlayTypeScope) LocalValueOrigin(name string) (BindingOrigin, bool) {
	return c.primary.LocalValueOrigin(name)
}

func (c *OverlayTypeScope) SetVisibility(name string, visibility Visibility) {
	c.primary.SetVisibility(name, visibility)
}

func (c *OverlayTypeScope) SetDocString(name string, doc string) {
	c.primary.SetDocString(name, doc)
}

func (c *OverlayTypeScope) GetDocString(name string) (string, bool) {
	// First check the primary environment (reopened module)
	if doc, found := c.primary.GetDocString(name); found {
		return doc, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.GetDocString(name)
}

func (c *OverlayTypeScope) SetDirectives(name string, directives []*DirectiveApplication) {
	c.primary.SetDirectives(name, directives)
}

func (c *OverlayTypeScope) GetDirectives(name string) []*DirectiveApplication {
	// This is a bit naive, but I'd rather wait until it becomes a problem so I
	// can understand the use case
	return append(c.primary.GetDirectives(name), c.lexical.GetDirectives(name)...)
}

func (c *OverlayTypeScope) SetModuleDocString(doc string) {
	c.primary.SetModuleDocString(doc)
}

func (c *OverlayTypeScope) GetModuleDocString() string {
	return c.primary.GetModuleDocString()
}

func (c *OverlayTypeScope) Remove(name string) hm.Env {
	c.primary.Remove(name)
	return c
}

func (c *OverlayTypeScope) Apply(subs hm.Subs) hm.Substitutable {
	return &OverlayTypeScope{
		primary: c.primary.Apply(subs).(TypeScope),
		lexical: c.lexical.Apply(subs).(TypeScope),
	}
}

func (c *OverlayTypeScope) FreeTypeVar() hm.TypeVarSet {
	primaryVars := c.primary.FreeTypeVar()
	lexicalVars := c.lexical.FreeTypeVar()
	return primaryVars.Union(lexicalVars)
}

func (c *OverlayTypeScope) GetDynamicScopeType() hm.Type {
	// First check primary (object/module being inferred)
	if t := c.primary.GetDynamicScopeType(); t != nil {
		return t
	}
	// Then check lexical scope
	return c.lexical.GetDynamicScopeType()
}

func (c *OverlayTypeScope) SetDynamicScopeType(t hm.Type) {
	c.primary.SetDynamicScopeType(t)
}

var _ TypeScope = &OverlayTypeScope{}

func (t *OverlayTypeScope) Eq(other Type) bool                         { return other == t }
func (t *OverlayTypeScope) Name() string                               { return t.primary.Name() }
func (t *OverlayTypeScope) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *OverlayTypeScope) Types() hm.Types                            { return nil }
func (t *OverlayTypeScope) Supertypes() []Type                         { return t.primary.Supertypes() }
func (t *OverlayTypeScope) String() string                             { return t.primary.String() }

// NamedType looks up object types, needed for NamedTypeNode.Infer compatibility
func (c *OverlayTypeScope) NamedType(name string) (TypeScope, bool) {
	// First check the primary environment (reopened module)
	if t, found := c.primary.NamedType(name); found {
		return t, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.NamedType(name)
}

func (c *OverlayTypeScope) LocalNamedType(name string) (TypeScope, bool) {
	return c.primary.LocalNamedType(name)
}

func (c *OverlayTypeScope) NamedTypes() iter.Seq2[string, TypeScope] {
	return func(yield func(string, TypeScope) bool) {
		seen := map[string]bool{}
		for name, env := range c.primary.NamedTypes() {
			seen[name] = true
			if !yield(name, env) {
				return
			}
		}
		for name, env := range c.lexical.NamedTypes() {
			if !seen[name] {
				if !yield(name, env) {
					return
				}
			}
		}
	}
}

// AddObject adds a object type to the primary environment
func (c *OverlayTypeScope) AddObject(name string, object TypeScope) {
	c.primary.AddObject(name, object)
}

func (c *OverlayTypeScope) SetTypeOrigin(name string, origin BindingOrigin) {
	c.primary.SetTypeOrigin(name, origin)
}

func (c *OverlayTypeScope) LocalTypeOrigin(name string) (BindingOrigin, bool) {
	return c.primary.LocalTypeOrigin(name)
}

// CheckTypeConflict delegates to the primary module
func (c *OverlayTypeScope) CheckTypeConflict(symbolName string) []string {
	imports := c.primary.CheckTypeConflict(symbolName)
	// Fall back to lexical scope if primary isn't a Module
	for _, importer := range c.lexical.CheckTypeConflict(symbolName) {
		if !slices.Contains(imports, importer) {
			imports = append(imports, importer)
		}
	}
	return imports
}

// CheckValueConflict delegates to the primary module
func (c *OverlayTypeScope) CheckValueConflict(symbolName string) []string {
	imports := c.primary.CheckValueConflict(symbolName)
	// Fall back to lexical scope if primary isn't a Module
	for _, importer := range c.lexical.CheckValueConflict(symbolName) {
		if !slices.Contains(imports, importer) {
			imports = append(imports, importer)
		}
	}
	return imports
}

// CheckDirectiveConflict delegates to the primary module
func (c *OverlayTypeScope) CheckDirectiveConflict(directiveName string) []string {
	imports := c.primary.CheckDirectiveConflict(directiveName)
	// Fall back to lexical scope if primary isn't a Module
	for _, importer := range c.lexical.CheckDirectiveConflict(directiveName) {
		if !slices.Contains(imports, importer) {
			imports = append(imports, importer)
		}
	}
	return imports
}

// AddDirective adds a directive to the primary environment
func (c *OverlayTypeScope) AddDirective(name string, directive *DirectiveDecl) {
	c.primary.AddDirective(name, directive)
}

func (c *OverlayTypeScope) SetDirectiveOrigin(name string, origin BindingOrigin) {
	c.primary.SetDirectiveOrigin(name, origin)
}

func (c *OverlayTypeScope) LocalDirectiveOrigin(name string) (BindingOrigin, bool) {
	return c.primary.LocalDirectiveOrigin(name)
}

// GetDirective gets a directive from either environment
func (c *OverlayTypeScope) GetDirective(name string) (*DirectiveDecl, bool) {
	// First check the primary environment (reopened module)
	if directive, found := c.primary.GetDirective(name); found {
		return directive, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.GetDirective(name)
}

// Bindings iterates over the primary and lexical bindings, with the primary
// bindings shadowing the lexical ones
func (c *OverlayTypeScope) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
	return func(yield func(key string, val *hm.Scheme) bool) {
		seen := map[string]bool{}
		for k, v := range c.primary.Bindings(visibility) {
			if !yield(k, v) {
				return
			}
			seen[k] = true
		}
		for k, v := range c.lexical.Bindings(visibility) {
			if seen[k] {
				continue
			}
			if !yield(k, v) {
				return
			}
		}
	}
}
