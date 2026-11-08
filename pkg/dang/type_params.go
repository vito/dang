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

// orderedBound is the name of the Ordered constraint, the one bound there is
// so far.
const orderedBound = "Ordered"

// errOrderedAsType reports Ordered written where a type goes.
var errOrderedAsType = errors.New("Ordered is a constraint, not a type: declare a type parameter bounded by it, like `[a: Ordered]`, and use that (`x: a!`)")

// variable returns the type variable p declares: its letter, bounded by
// Ordered if p is.
func (p *TypeParam) variable() hm.TypeVariable {
	tv := hm.TypeVariable([]rune(p.Name)[0])
	for _, b := range p.Bounds {
		if b.Base == nil && b.Name == orderedBound {
			tv = hm.Ordered(tv)
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

// FormatFunctionType renders a function type for display, with the type
// parameter list its bounds need (`[a, k: Ordered](xs: [a!]!, ...)`): a
// function's type variables are generic implicitly, so the list is only
// written when a bound would otherwise go unseen.
func FormatFunctionType(t hm.Type) string {
	ft, ok := t.(*hm.FunctionType)
	if !ok {
		return t.String()
	}
	var vars []hm.TypeVariable
	bounded := false
	for tv := range ft.FreeTypeVar() {
		if tv.IsRigid() || tv.IsWeak() {
			continue
		}
		vars = append(vars, tv)
		bounded = bounded || tv.IsOrdered()
	}
	if !bounded {
		return ft.String()
	}
	slices.SortFunc(vars, func(a, b hm.TypeVariable) int {
		return strings.Compare(a.Unbounded().String(), b.Unbounded().String())
	})
	params := make([]*TypeParam, 0, len(vars))
	for _, tv := range vars {
		name := tv.Unbounded().String()
		if len(params) > 0 && params[len(params)-1].Name == name {
			continue
		}
		p := &TypeParam{Name: name}
		if tv.IsOrdered() {
			p.Bounds = []*NamedTypeNode{{Name: orderedBound}}
		}
		params = append(params, p)
	}
	return formatTypeParams(params) + ft.String()
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
			if b.Base != nil || b.Name != orderedBound {
				return NewInferError(fmt.Errorf("unknown bound %s on type parameter %s: the only bound is Ordered", namedTypeString(b), p.Name), b)
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