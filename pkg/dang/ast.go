package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/hm"
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

type Pattern interface{}

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
	// Check if this is a zero-arity function and return its return type
	if ft, ok := t.(*hm.FunctionType); ok {
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
	switch fn := val.(type) {
	case BoundMethod:
		// BoundMethods delegate to their underlying function
		return isAutoCallableFn(fn.Method)
	case FunctionValue:
		// For FunctionValue, check if all arguments either have default values or are optional
		for _, argName := range fn.Args {
			// If this argument has a default value, it's optional
			if _, hasDefault := fn.Defaults[argName]; hasDefault {
				continue
			}

			// If no default, check if the type is nullable (optional)
			if rt, ok := fn.FnType.Arg().(*RecordType); ok {
				scheme, found := rt.SchemeOf(argName)
				if found {
					if fieldType, _ := scheme.Type(); fieldType != nil {
						if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
							// This is a required argument with no default value
							return false
						}
					}
				}
			}
		}
		// All arguments are either optional or have defaults, so this function can be auto-called
		return true
	case GraphQLFunction:
		// Check if the function has zero REQUIRED arguments (all args are optional)
		return hasZeroRequiredArgs(fn.Field)
	case BuiltinFunction:
		// For builtin functions, check if all arguments are optional (would need metadata)
		// For now, only consider truly zero-argument builtins as auto-callable
		if rt, ok := fn.FnType.Arg().(*RecordType); ok {
			return len(rt.Fields) == 0
		}
		return false
	case *ConstructorFunction:
		// For constructor functions, check if all parameters have default values
		for _, param := range fn.Parameters {
			if param.Value == nil {
				// No default value, so this is a required parameter
				return false
			}
		}
		// All parameters have default values, so constructor can be auto-called
		return true
	default:
		return false
	}
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
func autoCallFn(ctx context.Context, env EvalEnv, val Value) (Value, error) {
	// Create a FunCall with empty arguments and delegate to FunCall.Eval
	emptyRecord := Record{}
	funCall := FunCall{
		Fun:  createValueNode(val),
		Args: emptyRecord,
		Loc:  nil,
	}
	return funCall.Eval(ctx, env)
}

// autoCallFnWithReceiver calls a zero-arity function with empty arguments and an optional receiver for 'self'
func autoCallFnWithReceiver(ctx context.Context, env EvalEnv, val Value, receiver *ModuleValue) (Value, error) {
	// For BoundMethods, just use regular autoCall since they already have their receiver
	if boundMethod, isBoundMethod := val.(BoundMethod); isBoundMethod {
		return autoCallFn(ctx, env, boundMethod)
	}

	// For FunctionValue with receiver, create a BoundMethod and then autoCall
	if fnVal, isFunctionValue := val.(FunctionValue); isFunctionValue && receiver != nil {
		boundMethod := BoundMethod{Method: fnVal, Receiver: receiver}
		return autoCallFn(ctx, env, boundMethod)
	}

	// For everything else, use regular autoCall
	return autoCallFn(ctx, env, val)
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
func (v *ValueNode) Eval(ctx context.Context, env EvalEnv) (Value, error) { return v.Val, nil }

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

// CompositeEnv combines two evaluation environments for variable resolution
type CompositeEnv struct {
	primary EvalEnv // Where new bindings go (the reopened module)
	lexical EvalEnv // Where to look for external variables (current environment)
}

func (c CompositeEnv) Get(name string) (Value, bool) {
	// First check the primary environment (receiver/parameters)
	// This allows parameters and receiver fields to shadow lexical scope
	if val, found := c.primary.Get(name); found {
		return val, true
	}
	// Then check the lexical environment for fallback
	return c.lexical.Get(name)
}

func (c CompositeEnv) GetLocal(name string) (Value, bool) {
	return c.primary.GetLocal(name)
}

func (c CompositeEnv) Bindings(vis Visibility) []Keyed[Value] {
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

// MarshalJSON implements json.Marshaler for ModuleValue
// Includes private fields, so that state can be retained
func (m CompositeEnv) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.primary)
}

// GetForAssignment returns the value from the environment where assignment should occur
// For compound assignments, we want to read and write from the same environment (primary)
func (c CompositeEnv) GetForAssignment(name string) (Value, bool) {
	// For assignment operations, prefer the primary environment (receiver)
	// This ensures compound assignments like += work on receiver fields
	if val, found := c.primary.Get(name); found {
		return val, true
	}
	// Fall back to lexical environment only if not found in primary
	return c.lexical.Get(name)
}

var _ Value = CompositeEnv{}

func (c CompositeEnv) String() string {
	return fmt.Sprintf("CompositeEnv{primary: %v, lexical: %v}", c.primary, c.lexical)
}

func (c CompositeEnv) Type() hm.Type {
	return c.primary.Type()
}

func (c CompositeEnv) Set(name string, value Value) EvalEnv {
	// All new bindings go to the primary environment (copy-on-write semantics)
	c.primary.Set(name, value)
	return c
}

func (c CompositeEnv) SetWithVisibility(name string, value Value, visibility Visibility) {
	// All new bindings go to the primary environment (copy-on-write semantics)
	c.primary.SetWithVisibility(name, value, visibility)
}

func (c CompositeEnv) Reassign(name string, value Value) {
	// Delegate to the primary environment for scoping logic
	c.primary.Reassign(name, value)
}

func (c CompositeEnv) Visibility(name string) Visibility {
	// Speculative: don't fall back to lexical, we should consider that always private?
	return c.primary.Visibility(name)
}

func (c CompositeEnv) Fork() EvalEnv {
	// Fork the primary environment and keep the same lexical environment
	return CompositeEnv{
		primary: c.primary.Fork(),
		lexical: c.lexical,
	}
}

func (c CompositeEnv) Clone() EvalEnv {
	// Clone the primary environment and keep the same lexical environment
	return CompositeEnv{
		primary: c.primary.Clone(),
		lexical: c.lexical,
	}
}

// CreateCompositeEnv creates a composite environment for reopening
func CreateCompositeEnv(reopenedEnv EvalEnv, currentEnv EvalEnv) CompositeEnv {
	return CompositeEnv{
		primary: reopenedEnv,
		lexical: currentEnv,
	}
}

// CompositeModule combines two type environments for Reopen type inference
type CompositeModule struct {
	primary Env // The reopened module (where new bindings go)
	lexical Env // Current lexical scope (for variable lookups)
}

func (c *CompositeModule) SchemeOf(name string) (*hm.Scheme, bool) {
	// First check the primary environment (reopened module/class fields)
	// This allows class fields to have precedence over outer scope variables
	if scheme, found := c.primary.SchemeOf(name); found {
		return scheme, true
	}
	// Then check the lexical environment (current scope) for fallback
	return c.lexical.SchemeOf(name)
}

func (c *CompositeModule) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	// For CompositeModule, local scope is the primary environment (the reopened module)
	return c.primary.LocalSchemeOf(name)
}

func (c *CompositeModule) Clone() hm.Env {
	return &CompositeModule{
		primary: c.primary.Clone().(Env),
		lexical: c.lexical, // Keep same lexical environment
	}
}

func (c *CompositeModule) Add(name string, scheme *hm.Scheme) hm.Env {
	c.primary.Add(name, scheme)
	return c
}

func (c *CompositeModule) SetVisibility(name string, visibility Visibility) {
	c.primary.SetVisibility(name, visibility)
}

func (c *CompositeModule) SetDocString(name string, doc string) {
	c.primary.SetDocString(name, doc)
}

func (c *CompositeModule) GetDocString(name string) (string, bool) {
	// First check the primary environment (reopened module)
	if doc, found := c.primary.GetDocString(name); found {
		return doc, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.GetDocString(name)
}

func (c *CompositeModule) SetModuleDocString(doc string) {
	c.primary.SetModuleDocString(doc)
}

func (c *CompositeModule) GetModuleDocString() string {
	return c.primary.GetModuleDocString()
}

func (c *CompositeModule) Remove(name string) hm.Env {
	c.primary.Remove(name)
	return c
}

func (c *CompositeModule) Apply(subs hm.Subs) hm.Substitutable {
	return &CompositeModule{
		primary: c.primary.Apply(subs).(Env),
		lexical: c.lexical.Apply(subs).(Env),
	}
}

func (c *CompositeModule) FreeTypeVar() hm.TypeVarSet {
	primaryVars := c.primary.FreeTypeVar()
	lexicalVars := c.lexical.FreeTypeVar()
	return primaryVars.Union(lexicalVars)
}

var _ Env = &CompositeModule{}

func (t *CompositeModule) Eq(other Type) bool                         { return other == t }
func (t *CompositeModule) Name() string                               { return t.primary.Name() }
func (t *CompositeModule) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *CompositeModule) Types() hm.Types                            { return nil }
func (t *CompositeModule) String() string                             { return t.primary.String() }

// NamedType looks up class types, needed for NamedTypeNode.Infer compatibility
func (c *CompositeModule) NamedType(name string) (Env, bool) {
	// First check the primary environment (reopened module)
	if t, found := c.primary.NamedType(name); found {
		return t, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.NamedType(name)
}

// AddClass adds a class type to the primary environment
func (c *CompositeModule) AddClass(name string, class Env) {
	c.primary.AddClass(name, class)
}

// AddDirective adds a directive to the primary environment
func (c *CompositeModule) AddDirective(name string, directive *DirectiveDecl) {
	c.primary.AddDirective(name, directive)
}

// GetDirective gets a directive from either environment
func (c *CompositeModule) GetDirective(name string) (*DirectiveDecl, bool) {
	// First check the primary environment (reopened module)
	if directive, found := c.primary.GetDirective(name); found {
		return directive, true
	}
	// Then check the lexical environment (current scope)
	return c.lexical.GetDirective(name)
}

// Bindings iterates over the primary and lexical bindings, with the primary
// bindings shadowing the lexical ones
func (c *CompositeModule) Bindings(visibility Visibility) iter.Seq2[string, *hm.Scheme] {
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

// nodeToString converts a Node to a readable string representation for debugging
func nodeToString(node Node) string {
	switch n := node.(type) {
	case *Symbol:
		return n.Name
	case *Select:
		if n.Receiver == nil {
			return n.Field
		}
		receiver := nodeToString(n.Receiver)
		return fmt.Sprintf("%s.%s", receiver, n.Field)
	case *FunCall:
		fun := nodeToString(n.Fun)
		return fmt.Sprintf("%s(...)", fun)
	case *String:
		return fmt.Sprintf("\"%s\"", n.Value)
	case *Int:
		return fmt.Sprintf("%d", n.Value)
	case *Boolean:
		return fmt.Sprintf("%t", n.Value)
	case *Null:
		return "null"
	case *List:
		return "[...]"
	case *Default:
		left := nodeToString(n.Left)
		right := nodeToString(n.Right)
		return fmt.Sprintf("%s ?? %s", left, right)
	case *Equality:
		left := nodeToString(n.Left)
		right := nodeToString(n.Right)
		return fmt.Sprintf("%s == %s", left, right)
	case *Conditional:
		condition := nodeToString(n.Condition)
		return fmt.Sprintf("if %s { ... }", condition)
	case *Let:
		return fmt.Sprintf("let %s = %s in ...", n.Name, nodeToString(n.Value))
	default:
		return fmt.Sprintf("%T", node)
	}
}
