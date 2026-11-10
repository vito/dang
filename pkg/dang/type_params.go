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
// variables are left out: they belong to an enclosing declaration. A weak
// variable is listed by the name it prints as, so a binding's unknown shows
// its bound too (`[_a: Numeric] [_a!]!` for `let xs = []` summed later),
// and an expression's own open unknown reads as a plain variable (`[].sum`
// is `[a: Numeric] a!`).
func TypeParamsOf(types ...hm.Type) string {
	bounds := map[string][]hm.Constraint{}
	bounded := false
	for _, t := range types {
		typeVarsOf(t, func(tv hm.TypeVariable) {
			if tv.IsRigid() {
				return
			}
			// Not Unbounded's name: a weak variable is known by its bounds
			// too, and an open one would read as a binding's (`_a`).
			name := tv.String()
			cs := bounds[name]
			for _, c := range tv.Bounds() {
				if !slices.Contains(cs, c) {
					cs = append(cs, c)
				}
			}
			bounds[name] = cs
			bounded = bounded || len(cs) > 0
		})
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

// typeVarsOf visits the type variables t mentions, as its FreeTypeVar would,
// but also inside an anonymous record type (`{{x: xs}}`'s), whose fields
// FreeTypeVar doesn't look into. A bound weak variable is followed to its
// binding.
func typeVarsOf(t hm.Type, visit func(hm.TypeVariable)) {
	seen := map[*Type]bool{}
	var walk func(hm.Type)
	walk = func(t hm.Type) {
		switch v := t.(type) {
		case nil:
		case hm.TypeVariable:
			if bound := hm.ResolveWeak(v); bound != hm.Type(v) {
				walk(bound)
				return
			}
			visit(v)
		case hm.NullableTypeVariable:
			walk(v.TypeVariable)
		case *hm.UnionType:
			for _, o := range v.Options {
				walk(o)
			}
		case *Type:
			// A named type's members aren't part of how it reads.
			if v.metaOf == nil && v.Named == "" && v.importedAs == "" && !seen[v] {
				seen[v] = true
				walk(v.AsRecord())
			}
		case *RecordType:
			for _, f := range v.Fields {
				ft, _ := f.Value.Type()
				walk(ft)
			}
		default:
			for _, c := range t.Types() {
				walk(c)
			}
		}
	}
	walk(t)
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

// checkGenericImplementation checks an object's method (impl) against the
// interface method it implements (iface) as a whole, once the per-part
// variance checks have passed: the interface's type variables stand for
// whatever types its callers pick, within the interface's own bounds, so
// they are made rigid once, bounded as the interface declares them, and the
// method must fit every argument, its block and its return type at once.
// As hm's function assignability does, the arguments and block are fitted
// each on its own and their substitutions composed, so the outcome doesn't
// depend on the arguments' order; the return type is fitted under what
// they say of the method's variables (a `b!` return is one of the
// interface's `a` once an argument says b is a), and every part is checked
// again under the combination.
// That rejects an implementation that is less general than the interface:
// `best(x: String!, ...)` for `best(x: a!, ...)`, one that takes the same
// type for two of the interface's variables (`(x: a!, y: a!)` for `(x: a!,
// y: b!)`), and one that bounds a variable the interface doesn't (`pick[a:
// Ordered](x: a!)` for `pick(x: a!)`, whose `<` would meet values with no
// order). An implementation may bound less than the interface does.
//
// An interface method with no type variables of its own is checked the
// same way: an implementation taking one variable for two of its argument
// types (`f(x: c!, y: c!)` for `f(x: Int!, y: String!)`) would return a
// String where callers expect an Int, while a concrete type the interface
// names (`pick(x: Int!)`) satisfies a bound as usual. Like a stored
// field's, a function-valued field's type is pinned by conforming.
func checkGenericImplementation(fieldName string, ifaceFn, implFn *hm.FunctionType) error {
	skolem, _ := skolemSubs(ifaceFn)
	rigid := ifaceFn.Apply(skolem).(*hm.FunctionType)
	ifaceArgs, ok := rigid.Arg().(*RecordType)
	if !ok {
		return nil
	}
	implArgs, ok := implFn.Arg().(*RecordType)
	if !ok {
		return nil
	}
	// Callers through the interface leave the implementation's extra
	// (optional) arguments out, so those are null: a variable typing one
	// is nullable, as at any call that omits it.
	provided := map[string]bool{}
	for _, arg := range ifaceArgs.Fields {
		provided[arg.Key] = true
	}
	if omitted := omittedParamSubs(implArgs, provided); omitted != nil {
		implFn = implFn.Apply(omitted).(*hm.FunctionType)
		implArgs = implFn.Arg().(*RecordType)
	}
	// Why a type doesn't fit: the interface's callers pick its variables.
	forEvery := ""
	if len(skolem) > 0 {
		forEvery = " for every type the interface's callers pick"
	}

	// A part of the method: values of type from must fit to, and report
	// says why not, given what the parts say of the method's variables.
	type part struct {
		from, to hm.Type
		report   func(err error, subs hm.Subs) error
	}
	// here notes what an implementation's type stands for under subs, when
	// that reads differently: its `b!` may be the interface's `a!`.
	here := func(t hm.Type, subs hm.Subs) string {
		if subs == nil {
			return t.String()
		}
		if applied := t.Apply(subs).(hm.Type); applied.String() != t.String() {
			return fmt.Sprintf("%s (here %s)", t, applied)
		}
		return t.String()
	}
	var parts []part
	for _, arg := range ifaceArgs.Fields {
		iface, _ := arg.Value.Type()
		s, ok := implArgs.SchemeOf(arg.Key)
		if !ok {
			continue // reported by the variance checks
		}
		impl, _ := s.Type()
		key := arg.Key
		parts = append(parts, part{iface, impl, func(err error, subs hm.Subs) error {
			if be, ok := boundError(err); ok {
				return fmt.Errorf("field %q, argument %q: %w, but the interface's callers may pass %s for %s",
					fieldName, key, be, callersPass(be), iface)
			}
			return fmt.Errorf("field %q, argument %q: type %s doesn't fit interface type %s%s",
				fieldName, key, here(impl, subs), iface, forEvery)
		}})
	}
	if ifaceBlock, implBlock := rigid.Block(), implFn.Block(); ifaceBlock != nil && implBlock != nil {
		parts = append(parts, part{ifaceBlock, implBlock, func(err error, subs hm.Subs) error {
			if be, ok := boundError(err); ok {
				return fmt.Errorf("field %q, block argument: %w, but the interface's callers may pass a block of %s",
					fieldName, be, FormatType(ifaceFn.Block()))
			}
			return fmt.Errorf("field %q, block argument: type %s doesn't fit interface block type %s%s",
				fieldName, here(implBlock, subs), ifaceBlock, forEvery)
		}})
	}

	implRet, ifaceRet := implFn.Ret(false), rigid.Ret(false)
	ret := part{implRet, ifaceRet, func(err error, subs hm.Subs) error {
		if be, ok := boundError(err); ok {
			return fmt.Errorf("field %q, return type: %w, but the interface's callers may pick %s for %s",
				fieldName, be, callersPass(be), ifaceRet)
		}
		return fmt.Errorf("field %q: return type %s doesn't fit interface return type %s%s",
			fieldName, here(implRet, subs), ifaceRet, forEvery)
	}}

	// attempt fits first's parts against the originals and composes what
	// they say, then fits each of then's under that, and checks every part
	// again under the combination. Nothing is committed (a weak variable
	// of a function-valued field's type is bound only by the attempt that
	// succeeds), so no part's order pins another's.
	attempt := func(first, then []part) (hm.Subs, error) {
		subs := hm.NewSubs()
		fit := func(p part, from, to hm.Type) error {
			s, err := hm.AssignableUncommitted(from, to)
			if err != nil {
				return p.report(err, subs)
			}
			merged, err := subs.Compose(s)
			if err != nil {
				return p.report(err, subs)
			}
			subs = merged
			return nil
		}
		for _, p := range first {
			if err := fit(p, p.from, p.to); err != nil {
				return nil, err
			}
		}
		for _, p := range then {
			if err := fit(p, p.from.Apply(subs).(hm.Type), p.to.Apply(subs).(hm.Type)); err != nil {
				return nil, err
			}
		}
		for _, p := range append(append([]part{}, first...), then...) {
			if _, err := hm.AssignableUncommitted(p.from.Apply(subs).(hm.Type), p.to.Apply(subs).(hm.Type)); err != nil {
				return nil, p.report(err, subs)
			}
		}
		return subs, nil
	}

	// The arguments and block decide the method's variables, and the
	// return is fitted under them (a `b!` return is the interface's `a`
	// once an argument says b is a). Failing that, the return may decide
	// them: `f(x: a!, y: a!): a!` for `f(x: Dog!, y: Cat!): Pet!` takes a
	// as Pet, where the arguments alone would join Dog and Cat elsewhere.
	subs, err := attempt(parts, []part{ret})
	if err != nil {
		var retErr error
		if subs, retErr = attempt([]part{ret}, parts); retErr != nil {
			return err
		}
	}
	if err := hm.CommitWeak(subs); err != nil {
		return fmt.Errorf("field %q: %w", fieldName, err)
	}
	return nil
}

// freshenTypeVars gives t's flexible type variables new identities, keeping
// their names and bounds, so that they can't coincide with another type's
// variables by being written with the same letter.
func freshenTypeVars(t hm.Type) hm.Type {
	subs := hm.NewSubs()
	for tv := range t.FreeTypeVar() {
		if tv.IsRigid() || tv.IsWeak() {
			continue
		}
		subs.Add(tv, hm.Freshen(tv))
	}
	if len(subs) == 0 {
		return t
	}
	return t.Apply(subs).(hm.Type)
}

// boundError recognizes an implementation's bound that the interface's
// (rigid, declared-bounded) type variable doesn't satisfy.
func boundError(err error) (implementationBoundError, bool) {
	var ce hm.ConstraintError
	if !errors.As(err, &ce) || ce.Var == 0 {
		return implementationBoundError{}, false
	}
	e := implementationBoundError{Var: ce.Var.Unbounded(), Bound: ce.Constraint}
	if t, _ := stripNonNull(ce.Type); t != nil {
		if tv, ok := t.(hm.TypeVariable); ok {
			e.Allowed = tv.Bounds()
		}
	}
	return e, true
}

// implementationBoundError is checkGenericImplementation's error for a
// bound: the implementation bounds Var by Bound, which the interface's type
// variable, bounded only by Allowed, doesn't promise.
type implementationBoundError struct {
	Var     hm.TypeVariable
	Bound   hm.Constraint
	Allowed []hm.Constraint
}

func (e implementationBoundError) Error() string {
	return fmt.Sprintf("the implementation bounds %s by %s", e.Var, e.Bound.Name())
}

// callersPass says what an interface lets its callers pass for the type
// variable a bound error is about: "any type", or "any Ordered type" when
// the interface bounds it.
func callersPass(e implementationBoundError) string {
	if len(e.Allowed) == 0 {
		return "any type"
	}
	names := make([]string, len(e.Allowed))
	for i, c := range e.Allowed {
		names[i] = c.Name()
	}
	return "any " + strings.Join(names, " & ") + " type"
}
