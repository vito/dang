package dang

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// TypeParam is a type parameter a function declares in its explicit list,
// `name[a, k: Ordered](...)`, with the bounds its type must satisfy.
type TypeParam struct {
	Name   string
	Bounds []*NamedTypeNode
	Loc    *SourceLocation
}

func (p *TypeParam) GetSourceLocation() *SourceLocation { return p.Loc }

// variable returns the type variable p declares: its letter, bounded as p
// is (by the bounds checkTypeParams finds; others it reports).
func (p *TypeParam) variable() hm.TypeVariable {
	tv := hm.TypeVariable([]rune(p.Name)[0])
	for _, b := range p.Bounds {
		if c, ok := lookupConstraint(b); ok {
			tv = hm.Bound(tv, c.Constraint)
		}
	}
	return tv
}

// formatTypeParams renders a type parameter list as written, `[a, k:
// Ordered]`, or "" for none.
func formatTypeParams(params []*TypeParam) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name
		if len(p.Bounds) > 0 {
			bounds := make([]string, len(p.Bounds))
			for j, b := range p.Bounds {
				bounds[j] = namedTypeString(b)
			}
			parts[i] += ": " + strings.Join(bounds, " & ")
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func namedTypeString(t *NamedTypeNode) string {
	if t.Base != nil {
		return namedTypeString(t.Base) + "." + t.Name
	}
	return t.Name
}

// FormatType renders a type for display, with the type parameter list its
// bounds need, as a user writes one: `[a, k: Ordered](xs: [a!]!, ...)` for
// a function, `[a: Numeric] a` for anything else. Type variables are generic
// implicitly, so the list is only written when a bound would otherwise go
// unseen.
func FormatType(t hm.Type) string {
	if t == nil {
		return ""
	}
	params := TypeParamsOf(t)
	if params == "" {
		return t.String()
	}
	if _, ok := t.(*hm.FunctionType); ok {
		return params + t.String()
	}
	return params + " " + t.String()
}

// FormatScheme renders a scheme's type as FormatType does, in place of
// hm's `forall a. ...` form.
func FormatScheme(s *hm.Scheme) string {
	if s == nil {
		return ""
	}
	t, _ := s.Type()
	return FormatType(t)
}

// TypeParamsOf returns the type parameter list, `[a, k: Ordered]`, of the
// free type variables of types, or "" when none of them is bounded. Rigid
// variables are left out (they belong to an enclosing declaration), and so
// are a binding's weak ones (`_a`, not generic), but not an expression's own
// open unknown (an empty literal's element type), which reads as a plain
// variable: `[].sum` is `[a: Numeric] a!`.
func TypeParamsOf(types ...hm.Type) string {
	bounds := map[string][]hm.Constraint{}
	bounded := false
	for _, t := range types {
		if t == nil {
			continue
		}
		for tv := range t.FreeTypeVar() {
			if tv.IsRigid() || (tv.IsWeak() && !tv.IsOpenWeak()) {
				continue
			}
			// Not Unbounded's name: an open weak variable is known by its
			// bounds too, and without them would read as a binding's (`_a`).
			name := tv.String()
			cs := bounds[name]
			for _, c := range tv.Bounds() {
				if !slices.Contains(cs, c) {
					cs = append(cs, c)
				}
			}
			bounds[name] = cs
			bounded = bounded || len(cs) > 0
		}
	}
	if !bounded {
		return ""
	}
	names := make([]string, 0, len(bounds))
	for name := range bounds {
		names = append(names, name)
	}
	slices.Sort(names)
	params := make([]*TypeParam, len(names))
	for i, name := range names {
		cs := bounds[name]
		slices.Sort(cs)
		p := &TypeParam{Name: name}
		for _, c := range cs {
			p.Bounds = append(p.Bounds, &NamedTypeNode{Name: c.Name()})
		}
		params[i] = p
	}
	return formatTypeParams(params)
}

type typeParamsKey struct{}

// withTypeParams brings a function's declared type parameters into scope for
// its signature and body: there, a written `a` is the declared one, bounded
// as declared. A parameter shadows an enclosing generic function's own
// variable of the same name.
func withTypeParams(ctx context.Context, params []*TypeParam) context.Context {
	if len(params) == 0 {
		return ctx
	}
	outer := typeParamVars(ctx)
	vars := make(map[hm.TypeVariable]hm.TypeVariable, len(outer)+len(params))
	for tv, t := range outer {
		vars[tv] = t
	}
	names := map[hm.TypeVariable]bool{}
	for _, p := range params {
		if r := []rune(p.Name); len(r) != 1 || r[0] < 'a' || r[0] > 'z' {
			continue // rejected by checkTypeParams
		}
		tv := p.variable()
		vars[tv.Unbounded()] = tv
		names[tv.Unbounded()] = true
	}
	ctx = context.WithValue(ctx, typeParamsKey{}, vars)
	if scoped := scopedTypeVars(ctx); len(scoped) > 0 {
		kept := make(hm.Subs, len(scoped))
		for tv, t := range scoped {
			if !names[tv.Unbounded()] {
				kept[tv] = t
			}
		}
		ctx = context.WithValue(ctx, scopedTypeVarsKey{}, kept)
	}
	return ctx
}

// typeParamVars maps each declared type parameter in scope, by its letter,
// to the variable it declares.
func typeParamVars(ctx context.Context) map[hm.TypeVariable]hm.TypeVariable {
	s, _ := ctx.Value(typeParamsKey{}).(map[hm.TypeVariable]hm.TypeVariable)
	return s
}

// checkTypeParams checks a function's declared type parameters against its
// signature: each is a single letter, declared once, bounded only by known
// constraints and used in the signature, and the signature uses no type
// variable of its own that the list leaves out. A rigid variable in the
// signature is an enclosing generic function's, in scope already.
func checkTypeParams(name string, params []*TypeParam, sig ...hm.Type) error {
	if len(params) == 0 {
		return nil
	}
	declared := map[string]*TypeParam{}
	for _, p := range params {
		if r := []rune(p.Name); len(r) != 1 || r[0] < 'a' || r[0] > 'z' {
			return NewInferError(fmt.Errorf("type parameter %s: a type parameter is a single lowercase letter, like `a`", p.Name), p)
		}
		if _, dup := declared[p.Name]; dup {
			return NewInferError(fmt.Errorf("type parameter %s is declared twice", p.Name), p)
		}
		declared[p.Name] = p
		for _, b := range p.Bounds {
			if _, ok := lookupConstraint(b); !ok {
				return NewInferError(fmt.Errorf("unknown bound %s on type parameter %s: known bounds: %s", namedTypeString(b), p.Name, constraintNames()), b)
			}
		}
	}
	used := map[string]bool{}
	for _, t := range sig {
		if t == nil {
			continue
		}
		for tv := range t.FreeTypeVar() {
			if tv.IsRigid() || tv.IsWeak() || !tv.IsWritten() {
				continue
			}
			v := tv.Unbounded().String()
			if _, ok := declared[v]; !ok {
				return NewInferError(fmt.Errorf("type variable %s is not declared: add it to %s's type parameters, `%s%s`", v, name, name, formatTypeParams(append(params[:len(params):len(params)], &TypeParam{Name: v}))), typeParamList(params))
			}
			used[v] = true
		}
	}
	for _, p := range params {
		if !used[p.Name] {
			return NewInferError(fmt.Errorf("type parameter %s is not used in %s's signature", p.Name, name), p)
		}
	}
	return nil
}

// typeParamList locates a whole type parameter list.
type typeParamList []*TypeParam

func (l typeParamList) GetSourceLocation() *SourceLocation {
	first, last := l[0].Loc, l[len(l)-1].Loc
	if first == nil || last == nil {
		return first
	}
	loc := *first
	loc.End = last.End
	if first.Line == last.Line && last.End != nil {
		loc.Length = last.End.Column - first.Column
	}
	return &loc
}

// checkImplementationBounds rejects a parameter type of an object's method
// (impl) whose bounded type variable the interface's parameter type
// (iface) doesn't satisfy. The interface's type variables stand for any type
// its callers pick, so they are checked as rigid ones, with no bound: an
// interface's `pick(x: a!)` can't be implemented by `pick[a: Ordered](x: a!)`,
// whose `<` would then meet values with no order. A concrete type the
// interface names (`pick(x: Int!)`) satisfies the bound as usual.
func checkImplementationBounds(iface, impl hm.Type) error {
	bounded := false
	for tv := range impl.FreeTypeVar() {
		bounded = bounded || (len(tv.Bounds()) > 0 && !tv.IsRigid())
	}
	if !bounded {
		return nil
	}
	skolem, _ := skolemSubs(iface)
	_, err := hm.Assignable(iface.Apply(skolem).(hm.Type), impl)
	var ce hm.ConstraintError
	if errors.As(err, &ce) {
		return fmt.Errorf("the implementation bounds %s by %s", ce.Var.Unbounded(), ce.Constraint.Name())
	}
	return nil
}