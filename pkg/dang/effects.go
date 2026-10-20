package dang

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// Closure purity.
//
// With top-level bindings immutable (purity.go), named functions and methods
// cannot write anything their caller can observe: their own `self` writes are
// forks. The only functions that can write shared state are closures — blocks
// and nested functions — writing locals, args or `self` of the scope they were
// created in. Such an impure closure may only be called or passed as an
// argument; storing, returning or collecting it is a compile error. So every
// function value that is stored anywhere is pure, and nothing needs to track
// function values through storage.
//
// A closure's effect is computed lexically, in source order, while its body
// is inferred: the bindings it writes that it does not declare (including
// through the closures and blocks it calls), and the function-typed parameters
// of enclosing functions it calls. Nested functions are not hoisted, so every
// closure is fully inferred before anything can reference it: no fixed point.
//
// Function-typed parameters are effect variables. A parameter that escapes
// (`self.cb = f`) must be pure; that requirement is checked against the
// argument at every call site once inference is complete, because hoisting
// infers callers before callees. Forwarding (`outer { x => f(x) }` into an
// escaping parameter) propagates the requirement to `f`: a monotone boolean
// propagation over parameters, not an effect fixed point.

type effKind int

const (
	// effClosure is the effect of a function body: a block, nested function,
	// named function, method, or constructor. Named functions' effects are
	// always empty of targets; they are tracked uniformly anyway.
	effClosure effKind = iota
	// effParam is the effect variable of a function-typed parameter.
	effParam
)

// Effect is the dang implementation of hm.Effect.
type Effect struct {
	kind effKind
	name string

	// effClosure: bindings written outside the closure (target -> name of
	// the closure writing it directly), and outer parameters it may call.
	targets map[effTarget]string
	params  map[*Effect]bool
	// nonLocal is a `return`/`break` in a block that leaves the block.
	nonLocal Node
	// createdIn is the frame the closure was created in.
	createdIn *effFrame

	// effParam: the function owning the parameter, and why it must be bound
	// to a pure function: because it escapes, or because it is called inside
	// a `{{ }}` field (empty when it need not be).
	owner         *Effect
	escapeWhy     string
	concurrentWhy string
}

var _ hm.Effect = (*Effect)(nil)

func (e *Effect) EffectName() string {
	if e == nil {
		return "pure"
	}
	return e.name
}

func newClosureEffect(name string) *Effect {
	return &Effect{kind: effClosure, name: name}
}

// reset clears a closure effect before its body is (re-)inferred.
func (e *Effect) reset() *Effect {
	e.targets = nil
	e.params = nil
	e.nonLocal = nil
	return e
}

func newParamEffect(owner *Effect, name string) *Effect {
	return &Effect{kind: effParam, owner: owner, name: name}
}

// impure reports whether a closure writes anything declared outside it.
func (e *Effect) impure() bool {
	return e != nil && e.kind == effClosure && len(e.targets) > 0
}

// firstTarget returns a deterministic witness write.
func (e *Effect) firstTarget() (effTarget, string) {
	var targets []effTarget
	for t := range e.targets {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })
	return targets[0], e.targets[targets[0]]
}

func (e *Effect) describeWrites(self string) string {
	t, w := e.firstTarget()
	if w != "" && w != self {
		return fmt.Sprintf("writes `%s` (via `%s`)", t.name, w)
	}
	return fmt.Sprintf("writes `%s`", t.name)
}

// effectOf returns the effect annotation of a function type, if any.
func effectOf(t hm.Type) *Effect {
	ft := fnTypeOf(t)
	if ft == nil {
		return nil
	}
	e, _ := ft.Effect().(*Effect)
	return e
}

func fnTypeOf(t hm.Type) *hm.FunctionType {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	ft, _ := t.(*hm.FunctionType)
	return ft
}

// effTarget identifies a binding: the frame declaring it and its name.
type effTarget struct {
	frame TypeScope
	name  string
}

// effFrame is a function body (named or closure), or a `{{ }}` field, on the
// inference stack.
type effFrame struct {
	parent *effFrame
	eff    *Effect
	// owns lists the frames whose bindings are declared inside this body:
	// its own scope, plus a method's or constructor's receiver type.
	owns []TypeScope
	// block is set for block arguments; entryBreak is the break target of
	// the call receiving the block.
	block      bool
	entryBreak *InferControlTarget
	// field marks a `{{ }}` field (fields_purity.go); it has no eff.
	field bool
}

func (fr *effFrame) inside(target TypeScope) bool {
	for cur := target; cur != nil; {
		for _, own := range fr.owns {
			if own == cur {
				return true
			}
		}
		t, ok := cur.(*Type)
		if !ok || t.Parent == nil {
			return false
		}
		cur = t.Parent
	}
	return false
}

type effFrameKey struct{}

func effFrameFrom(ctx context.Context) *effFrame {
	fr, _ := ctx.Value(effFrameKey{}).(*effFrame)
	return fr
}

// bodyFrame returns the innermost function body frame, skipping fields.
func bodyFrame(ctx context.Context) *effFrame {
	fr := effFrameFrom(ctx)
	for fr != nil && fr.field {
		fr = fr.parent
	}
	return fr
}

// withEffFrame enters a function body with effect eff. env is the body's own
// environment; self, when non-nil, is a method's receiver type.
func withEffFrame(ctx context.Context, eff *Effect, env hm.Env, self *Type) (context.Context, *effFrame) {
	fr := &effFrame{parent: effFrameFrom(ctx), eff: eff}
	eff.createdIn = fr.parent
	if own := envFrame(env); own != nil {
		fr.owns = append(fr.owns, own)
	}
	if self != nil {
		fr.owns = append(fr.owns, self)
	}
	return context.WithValue(ctx, effFrameKey{}, fr), fr
}

// methodSelfFrame returns the receiver type when env is a type body scope,
// i.e. when a function declared directly in env is a method or constructor.
func methodSelfFrame(env hm.Env) *Type {
	ov, ok := env.(*OverlayTypeScope)
	if !ok {
		return nil
	}
	t, ok := ov.primary.(*Type)
	if !ok || t.Parent != nil || t.Kind != ObjectKind {
		return nil
	}
	if nn, ok := t.dynamicScopeType.(hm.NonNullType); ok && nn.Type == hm.Type(t) {
		return t
	}
	return nil
}

// selfTypeFrame returns the type frame `self` refers to in env.
func selfTypeFrame(env hm.Env) TypeScope {
	t := env.GetDynamicScopeType()
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	if ts, ok := t.(*Type); ok {
		return ts
	}
	return nil
}

// absorbWrite records a write to target in the innermost function body, and
// checks it against enclosing `{{ }}` fields.
func absorbWrite(ctx context.Context, target effTarget, node Node) {
	checkFieldWrite(ctx, target, node)
	fr := bodyFrame(ctx)
	if fr == nil || fr.inside(target.frame) {
		return
	}
	if fr.eff.targets == nil {
		fr.eff.targets = map[effTarget]string{}
	}
	if _, ok := fr.eff.targets[target]; !ok {
		fr.eff.targets[target] = fr.eff.name
	}
}

// absorbCall records, in the innermost function body, the effect of calling
// a function with effect callee (named name, at node), and checks it against
// enclosing `{{ }}` fields.
func absorbCall(ctx context.Context, callee *Effect, node Node, name string) {
	if callee == nil {
		return
	}
	checkFieldCall(ctx, callee, node, name)
	fr := bodyFrame(ctx)
	if fr == nil {
		return
	}
	switch callee.kind {
	case effParam:
		if callee.owner != fr.eff {
			fr.eff.addParam(callee)
		}
	case effClosure:
		for t, w := range callee.targets {
			if fr.inside(t.frame) {
				continue
			}
			if fr.eff.targets == nil {
				fr.eff.targets = map[effTarget]string{}
			}
			if _, ok := fr.eff.targets[t]; !ok {
				fr.eff.targets[t] = w
			}
		}
		for p := range callee.params {
			if p.owner != fr.eff {
				fr.eff.addParam(p)
			}
		}
	}
}

func (e *Effect) addParam(p *Effect) {
	if e.params == nil {
		e.params = map[*Effect]bool{}
	}
	e.params[p] = true
}

// recordEffectWrite records an assignment (at node) to name (resolved in
// env), or to a field of self.
func recordEffectWrite(ctx context.Context, env hm.Env, node Node, name string, isSelf bool, first string) {
	var frame TypeScope
	if isSelf {
		frame = selfTypeFrame(env)
		name = first
	} else {
		frame = resolveBinding(env, name)
	}
	if frame == nil {
		return
	}
	absorbWrite(ctx, effTarget{frame: frame, name: name}, node)
}

// markNonLocal records a `return`/`break` leaving the enclosing block(s): a
// return leaves every block up to its function; a break leaves the block
// whose receiving call it targets.
func markNonLocalReturn(ctx context.Context, node Node) {
	for fr := bodyFrame(ctx); fr != nil && (fr.block || fr.field); fr = fr.parent {
		if fr.block && fr.eff.nonLocal == nil {
			fr.eff.nonLocal = node
		}
	}
}

func markNonLocalBreak(ctx context.Context, node Node, target *InferControlTarget) {
	fr := bodyFrame(ctx)
	if fr != nil && fr.block && fr.entryBreak == target && fr.eff.nonLocal == nil {
		fr.eff.nonLocal = node
	}
}

// Escapes. A function value expression may appear in a call position (the
// callee, or an argument for a function-typed parameter); anywhere else it
// escapes. allowedFnKey carries the one node allowed at the current position.

type allowedFnKey struct{}

func withAllowedFn(ctx context.Context, node Node) context.Context {
	return context.WithValue(ctx, allowedFnKey{}, node)
}

func fnAllowedAt(ctx context.Context, node Node) bool {
	allowed, _ := ctx.Value(allowedFnKey{}).(Node)
	return allowed == node
}

// checkEscape handles a function value of type t escaping at node: an impure
// closure is an error; a parameter (or a closure calling one) must be pure.
func checkEscape(ctx context.Context, node Node, name string, t hm.Type) error {
	eff := effectOf(t)
	if eff == nil {
		return nil
	}
	if eff.kind == effParam {
		requirePure(eff, false, fmt.Sprintf("`%s` stores, returns, or collects `%s`", eff.owner.name, eff.name))
		return nil
	}
	if eff.impure() {
		return reportEffectError(ctx, NewInferError(fmt.Errorf(
			"`%s` %s, which is declared outside it, so it can only be called or passed as an argument, not stored, returned, or put in a collection",
			name, eff.describeWrites(name)), node))
	}
	for p := range eff.params {
		requirePure(p, false, fmt.Sprintf("`%s` calls `%s` and escapes", name, p.name))
	}
	return nil
}

// requirePure records that parameter p must be bound to a pure function:
// because it escapes, or because it is called inside a `{{ }}` field.
func requirePure(p *Effect, concurrent bool, why string) bool {
	if concurrent {
		if p.concurrentWhy != "" {
			return false
		}
		p.concurrentWhy = why
		return true
	}
	if p.escapeWhy != "" {
		return false
	}
	p.escapeWhy = why
	return true
}

// reportEffectError queues an error found during inference with the deferred
// ones, so the expression still types and nothing cascades. Without a solver
// (the REPL, literate docs) it returns the error for the caller to raise.
func reportEffectError(ctx context.Context, err error) error {
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		s.errs = append(s.errs, err)
		return nil
	}
	return err
}

// Deferred checks of arguments against parameter requirements.

type effBinding struct {
	param   *Effect
	arg     *Effect
	argNode Node
	callee  string
}

type effectSolver struct {
	bindings []effBinding
	// errs holds errors found during inference that are reported with the
	// deferred ones.
	errs []error
}

type effSolverKey struct{}

// withEffectSolver installs a solver in ctx unless one is already there. The
// returned bool reports whether the caller created it and so must solve it.
func withEffectSolver(ctx context.Context) (context.Context, *effectSolver, bool) {
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		return ctx, s, false
	}
	s := &effectSolver{}
	return context.WithValue(ctx, effSolverKey{}, s), s, true
}

// effectArg relates a function-valued argument to its parameter at a call
// site: the callee may call it, so its effect is the caller's; and when the
// parameter is an effect variable, the binding is checked once requirements
// are known.
func effectArg(ctx context.Context, callee string, paramType, argType hm.Type, argNode Node) {
	arg := effectOf(argType)
	if arg == nil {
		return
	}
	absorbCall(ctx, arg, argNode, calleeNameOf(argNode))
	param := effectOf(paramType)
	if param == nil || param.kind != effParam {
		return
	}
	if s, ok := ctx.Value(effSolverKey{}).(*effectSolver); ok {
		s.bindings = append(s.bindings, effBinding{param: param, arg: arg, argNode: argNode, callee: callee})
	}
}

// paramAcceptsFn reports whether a function value may be passed for a
// parameter of type paramType to a callee of type callee without escaping:
// the parameter is an effect variable (checked at the end), or the callee is
// unknown to Dang (builtins, which never store their arguments).
func paramAcceptsFn(callee *hm.FunctionType, paramType hm.Type) bool {
	if fnTypeOf(paramType) == nil {
		return false
	}
	if p := effectOf(paramType); p != nil && p.kind == effParam {
		return true
	}
	return effectOf(callee) == nil
}

// solve propagates parameter requirements through forwarded arguments and
// reports arguments that violate them.
func (s *effectSolver) solve() []error {
	for changed := true; changed; {
		changed = false
		for _, b := range s.bindings {
			for _, q := range forwardedParams(b.arg) {
				if b.param.escapeWhy != "" && requirePure(q, false, fmt.Sprintf("`%s` passes `%s` to `%s`, where %s", q.owner.name, q.name, b.callee, b.param.escapeWhy)) {
					changed = true
				}
				if b.param.concurrentWhy != "" && requirePure(q, true, fmt.Sprintf("`%s` passes `%s` to `%s`, where %s", q.owner.name, q.name, b.callee, b.param.concurrentWhy)) {
					changed = true
				}
			}
		}
	}
	errs := &effErrors{errs: s.errs}
	for _, b := range s.bindings {
		if b.arg.kind != effClosure {
			continue
		}
		subject, self := "this block", b.arg.name
		if _, isBlock := b.argNode.(*BlockArg); !isBlock {
			self = calleeNameOf(b.argNode)
			subject = "`" + self + "`"
		}
		for _, why := range []string{b.param.escapeWhy, b.param.concurrentWhy} {
			if why != "" && b.arg.impure() {
				errs.add(b.argNode, fmt.Sprintf("%s, so %s must not write outer state, but it %s", why, subject, b.arg.describeWrites(self)))
			}
		}
		if b.param.escapeWhy != "" && b.arg.nonLocal != nil {
			errs.add(b.arg.nonLocal, fmt.Sprintf("%s, so %s may outlive this call and cannot use `%s`", b.param.escapeWhy, subject, controlKeyword(b.arg.nonLocal)))
		}
	}
	return errs.errs
}

func forwardedParams(arg *Effect) []*Effect {
	switch arg.kind {
	case effParam:
		return []*Effect{arg}
	case effClosure:
		var out []*Effect
		for p := range arg.params {
			out = append(out, p)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
		return out
	}
	return nil
}

func controlKeyword(n Node) string {
	if _, ok := n.(*Break); ok {
		return "break"
	}
	return "return"
}

type effErrors struct {
	seen map[string]bool
	errs []error
}

func (c *effErrors) add(node Node, msg string) {
	key := fmt.Sprintf("%p:%s", node, msg)
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.errs = append(c.errs, NewInferError(fmt.Errorf("%s", msg), node))
}

// calleeNameOf names the function an expression refers to, for messages.
func calleeNameOf(n Node) string {
	switch x := n.(type) {
	case *Symbol:
		return x.Name
	case *Select:
		if x.Field != nil {
			return x.Field.Name
		}
	case *FunctionRef:
		return calleeNameOf(x.Expr)
	case *Grouped:
		return calleeNameOf(x.Expr)
	}
	return strings.TrimSpace(Format(n))
}

// EffectSummary renders a function type's effect for display (LSP hover):
// what a closure writes outside itself and which outer parameters it calls.
// It returns "" for pure functions and non-functions.
func EffectSummary(t hm.Type) string {
	e := effectOf(t)
	if e == nil || e.kind != effClosure {
		return ""
	}
	var parts []string
	var names []string
	for target := range e.targets {
		names = append(names, target.name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		parts = append(parts, "writes "+strings.Join(dedupeSorted(names), ", "))
	}
	var params []string
	for p := range e.params {
		params = append(params, p.name)
	}
	sort.Strings(params)
	if len(params) > 0 {
		parts = append(parts, "calls "+strings.Join(params, ", "))
	}
	return strings.Join(parts, "; ")
}

func dedupeSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}
