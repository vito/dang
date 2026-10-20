package dang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// inferWithEffects type-checks src the way RunFile does (top-level frame and
// effect solver installed) and returns the combined error text, or "".
func inferWithEffects(t *testing.T, src string) string {
	t.Helper()
	parsed, err := ParseWithRecovery("effects", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	file := parsed.(*FileBlock)
	typeScope, _ := BuildScopesFromImports("", nil)
	ctx, solver, _ := withEffectSolver(withTopLevelFrame(context.Background(), typeScope))
	var msgs []string
	if _, err := InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher()); err != nil {
		msgs = append(msgs, err.Error())
	}
	for _, err := range solver.solve() {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "\n")
}

type effectCase struct {
	name string
	src  string
	// want is a substring of the expected error; "" means accepted.
	want string
}

func runEffectCases(t *testing.T, cases []effectCase) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferWithEffects(t, tc.src)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("expected program to be accepted, got:\n%s", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected error containing %q, got:\n%s", tc.want, got)
			}
		})
	}
}

func TestTopLevelImmutable(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "free function",
			src:  "count: Int! = 0\nbump: Int! { count += 1; count }",
			want: "cannot assign to `count`: top-level bindings are immutable",
		},
		{
			name: "method",
			src:  "count: Int! = 0\ntype T { bump: Int! { count += 1; count } }",
			want: "cannot assign to `count`",
		},
		{
			name: "block in a function",
			src:  "count: Int! = 0\nbump: Int! { [1].each { x => count += x }; count }",
			want: "cannot assign to `count`",
		},
		{
			name: "path write in new()",
			src:  "type P { x: Int! }\np: P! = P(1)\ntype T { new { p.x = 2; self } }",
			want: "cannot assign to `p`",
		},
		{
			name: "narrowed binding",
			src:  "let n: Int = null\nf: Int! { if (n != null) { n = 1 }; 0 }",
			want: "cannot assign to `n`",
		},
		{
			name: "shadowing local is fine",
			src:  "count: Int! = 0\nbump: Int! { let count = 1; count += 1; count }",
		},
		{
			name: "self field is fine",
			src:  "type T { count: Int! = 0\n bump: T! { count += 1; self } }",
		},
		{
			name: "static written from script code",
			src:  "type Counter { self { count: Int! = 0 } }\nCounter.count = 5",
			want: "cannot assign to `Counter.count`: static members are immutable once initialized",
		},
		{
			name: "static written through a path from a method",
			src:  "type P { x: Int! }\ntype Counter { self { cfg: P! = P(1) }\n bump: Int! { Counter.cfg.x = 2; 1 } }",
			want: "cannot assign to `Counter.cfg`: static members are immutable",
		},
		{
			name: "static method writing its own self is pure",
			src: `
				type Counter {
					self {
						count: Int! = 0
						bump: Int! { count += 1; count }
						bumpSelf: Int! { self.count += 1; count }
					}
				}
				r: Int! { let x = {{a: Counter.bump, b: Counter.bumpSelf}}; x.a + x.b }
			`,
		},
		{
			name: "static method's closure writing its self inside {{ }}",
			src: `
				type Counter {
					self {
						count: Int! = 0
						run: Int! {
							bump: Int! { count += 1; count }
							{{a: bump}}.a
						}
					}
				}
			`,
			want: "{{ }} fields run concurrently; `bump` writes `count`",
		},
	})
}

func TestFieldPurity(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "named functions and methods are pure",
			src: `
				double(x: Int!): Int! { let y = x; y += x; y }
				type Counter {
					value: Int! = 0
					incr: Counter! { value += 1; self }
				}
				r: Int! {
					let ctr = Counter
					let x = {{a: double(1), b: ctr.incr.value, c: [ctr, ctr].{{incr}}}}
					x.a + x.b
				}
			`,
		},
		{
			name: "nested function writing the method's local",
			src: `
				type T {
					run: Int! {
						let n = 0
						bump: Int! { n += 1; n }
						{{a: bump, b: bump}}.a
					}
				}
			`,
			want: "{{ }} fields run concurrently; `bump` writes `n`",
		},
		{
			name: "nested function writing self",
			src: `
				type T {
					total: Int! = 0
					run: Int! {
						bump: Int! { total += 1; total }
						{{a: bump}}.a
					}
				}
			`,
			want: "{{ }} fields run concurrently; `bump` writes `total`",
		},
		{
			name: "transitively impure nested function",
			src: `
				run: Int! {
					let n = 0
					bump: Int! { n += 1; n }
					twice: Int! { bump; bump }
					{{a: twice}}.a
				}
			`,
			want: "`twice` writes `n` (via `bump`)",
		},
		{
			name: "block param called in fields, impure block",
			src: `
				type Acc {
					total: Int! = 0
					both(&block(x: Int!): Int!): Int! { let r = {{a: block(1), b: block(2)}}; r.a + r.b }
					runBoth: Acc! { both { x => total += x; total }; self }
				}
			`,
			want: "`Acc.both` calls `block` inside {{ }}, whose fields run concurrently, so this block must not write outer state, but it writes `total`",
		},
		{
			name: "block param called in fields, pure block",
			src: `
				both(&block(x: Int!): Int!): Int! { let r = {{a: block(1), b: block(2)}}; r.a + r.b }
				run: Int! { both { x => let y = x; y += 1; y } }
			`,
		},
		{
			name: "block param called from a block in a field",
			src: `
				each2(&block(x: Int!): Int!): [Int!]! { {{a: [1, 2].map { x => block(x) }}}.a }
				run: [Int!]! {
					let n = 0
					each2 { x => n += x; n }
				}
			`,
			want: "this block must not write outer state, but it writes `n`",
		},
		{
			name: "forwarded block",
			src: `
				both(&block(x: Int!): Int!): Int! { let r = {{a: block(1), b: block(2)}}; r.a + r.b }
				outer(&f(x: Int!): Int!): Int! { both { x => f(x) } }
				run: Int! {
					let total = 0
					outer { x => total += x; total }
				}
			`,
			want: "`outer` passes `f` to `both`, where `both` calls `block` inside {{ }}",
		},
		{
			name: "closure created outside a field, passed to a builtin inside it",
			src: `
				run: [Int!]! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					{{a: [1, 2].map { x => bump(x) }}}.a
				}
			`,
			want: "`bump` writes `n`",
		},
		{
			name: "direct write to an outer local",
			src: `
				run: Int! {
					let n = 0
					{{a: if (true) { n += 1; n } else { 0 }}}.a
				}
			`,
			want: "this assignment writes `n`, which is declared outside the field",
		},
		{
			name: "direct write to self",
			src: `
				type T {
					total: Int! = 0
					run: Int! { {{a: if (true) { self.total = 5; total } else { 0 }}}.a }
				}
			`,
			want: "this assignment writes `total`, which is declared outside the field",
		},
		{
			name: "block in a field writing an outer local",
			src: `
				run: [Int!]! {
					let n = 0
					{{a: [1, 2].map { x => n += x; n }}}.a
				}
			`,
			want: "this assignment writes `n`, which is declared outside the field",
		},
		{
			name: "writes to field-local state",
			src: `
				run: Int! {
					{{a: { let n = 0; [1, 2, 3].each { x => n += x }; n }}}.a
				}
			`,
		},
		{
			name: "pure closure created outside the field",
			src: `
				run: Int! {
					let k = 2
					scale(x: Int!): Int! { x * k }
					{{a: scale(1), b: scale(2)}}.b
				}
			`,
		},
		{
			name: "let inside an if block in a selection field's arguments",
			src: `
				type Acc {
					total: Int! = 0
					plus(n: Int!): Int! { total + n }
					run: Int! { self.{{x: plus(n: if (true) { let y = 1; y += 1; y } else { 0 })}}.x }
					runList: [Int!]! { [self, self].{{x: plus(n: if (true) { let y = 1; y += 1; y } else { 0 })}}.map { r => r.x } }
				}
			`,
		},
		{
			name: "selection field's arguments writing an outer local",
			src: `
				type Acc {
					total: Int! = 0
					plus(n: Int!): Int! { total + n }
					run: Int! {
						let y = 0
						self.{{x: plus(n: if (true) { y += 1; y } else { 0 })}}.x
					}
				}
			`,
			want: "this assignment writes `y`, which is declared outside the field",
		},
		{
			name: "selection field's arguments writing self",
			src: `
				type Acc {
					total: Int! = 0
					plus(n: Int!): Int! { total + n }
					run: Int! { self.{{x: plus(n: if (true) { total += 1; 1 } else { 0 })}}.x }
				}
			`,
			want: "this assignment writes `total`, which is declared outside the field",
		},
		{
			name: "nested function created inside a field writing an outer local",
			src: `
				run: Int! {
					let n = 0
					{{a: { bump: Int! { n += 1; n }; bump }}}.a
				}
			`,
			want: "this assignment writes `n`, which is declared outside the field",
		},
		{
			name: "write to a narrowed outer binding inside a field",
			src: `
				run: Int! {
					let n: Int = null
					{{a: if (n != null) { n = 1; 1 } else { 0 }}}.a
				}
			`,
			want: "this assignment writes `n`, which is declared outside the field",
		},
		{
			name: "nested record literals",
			src: `
				run: Int! {
					let n = 0
					{{a: {{b: if (true) { n += 1; n } else { 0 }}}.b}}.a
				}
			`,
			want: "this assignment writes `n`, which is declared outside the field",
		},
	})
}

// A directory module's top-level frame spans its files: a function in one
// file may not write a binding declared in another, while top-level script
// code in either still may.
func TestRunDirTopLevelImmutableAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.dang"), []byte("pub count: Int! = 0\ncount = 1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.dang"), []byte("bump: Int! { count += 1; count }\n"), 0o600))
	_, err := RunDir(context.Background(), dir, false)
	require.ErrorContains(t, err, "cannot assign to `count`: top-level bindings are immutable")
	require.ErrorContains(t, err, "b.dang")
}

// InferFormsWithPhases called directly (embedders, tests) is an entry point
// too: the deferred checks run, so an escaping block with a non-local return
// can never reach evaluation, where nothing would catch it.
func TestInferFormsWithPhasesRunsDeferredChecks(t *testing.T) {
	src := `
		type Box {
			saved(x: Int!): Int! { x }
			store(&block(x: Int!): Int!): Box! { self.saved = block; self }
		}
		makeReturner: Box! { Box.store { x => return Box } }
	`
	parsed, err := ParseWithRecovery("effects", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	typeScope, _ := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(context.Background(), parsed.(*FileBlock).Forms, typeScope, hm.NewSimpleFresher())
	if err == nil || !strings.Contains(err.Error(), "cannot use `return`") {
		t.Fatalf("expected the escaping return to be rejected, got %v", err)
	}
}

func TestEffectSummary(t *testing.T) {
	src := `
		run: Int! {
			let n = 0
			bump: Int! { n += 1; n }
			scale(x: Int!): Int! { x * 2 }
			bump + scale(1)
		}
	`
	parsed, err := ParseWithRecovery("effects", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	file := parsed.(*FileBlock)
	typeScope, _ := BuildScopesFromImports("", nil)
	if _, err := InferFormsWithPhases(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher()); err != nil {
		t.Fatal(err)
	}
	var got []string
	file.Walk(func(n Node) bool {
		if fd, ok := n.(*FunDecl); ok && fd.Inferred != nil {
			got = append(got, fd.Named+": "+EffectSummary(fd.Inferred))
		}
		return true
	})
	want := "run: \nbump: writes n\nscale: "
	if strings.Join(got, "\n") != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func TestClosureEscapes(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "impure nested function called directly",
			src: `
				type T {
					run: Int! {
						let n = 0
						bump: Int! { n += 1; n }
						bump
						bump
					}
				}
			`,
		},
		{
			name: "impure nested function stored in a let",
			src: `
				run: Int! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					let f = &bump
					f(1)
				}
			`,
			want: "`bump` writes `n`, which is declared outside it, so it can only be called or passed as an argument",
		},
		{
			name: "impure nested function returned from a block",
			src: `
				run: Int! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					let fs = [1].map { x => &bump }
					0
				}
			`,
			want: "`bump` writes `n`",
		},
		{
			name: "impure nested function in a list",
			src: `
				run: Int! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					let fs = [&bump]
					0
				}
			`,
			want: "`bump` writes `n`",
		},
		{
			name: "transitively impure nested function",
			src: `
				run: Int! {
					let n = 0
					bump: Int! { n += 1; n }
					twice(x: Int!): Int! { bump; bump }
					let f = &twice
					f(1)
				}
			`,
			want: "`twice` writes `n` (via `bump`)",
		},
		{
			name: "pure nested function stored",
			src: `
				run: Int! {
					double(x: Int!): Int! { x * 2 }
					let f = &double
					f(2)
				}
			`,
		},
		{
			name: "nested function writing self, stored",
			src: `
				type T {
					total: Int! = 0
					run: Int! {
						bump(x: Int!): Int! { total += x; total }
						let f = &bump
						f(1)
					}
				}
			`,
			want: "`bump` writes `total`",
		},
		{
			name: "impure block passed to a storing method",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				run: Int! {
					let n = 0
					let b = Box.store { x => n += x; n }
					b.saved(1)
				}
			`,
			want: "`Box.store` stores, returns, or collects `block`, so this block must not write outer state, but it writes `n`",
		},
		{
			name: "pure block passed to a storing method",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				run: Int! { Box.store { x => x * 2 }.saved(1) }
			`,
		},
		{
			name: "impure block passed to a non-storing function",
			src: `
				twice(&body: Int!): Int! { body; body }
				run: Int! {
					let n = 0
					twice { n += 1; n }
				}
			`,
		},
		{
			name: "forwarded into a storing parameter",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				wrap(&f(x: Int!): Int!): Box! { Box.store { x => f(x) } }
				run: Int! {
					let n = 0
					wrap { x => n += x; n }.saved(1)
				}
			`,
			want: "`wrap` passes `f` to `store`, where `Box.store` stores, returns, or collects `block`, so this block must not write outer state",
		},
		{
			name: "builtin calls an impure block",
			src: `
				run: Int! {
					let n = 0
					[1, 2].each { x => n += x }
					n
				}
			`,
		},
		{
			name: "top-level script block passed to a storing method",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				let total = 0
				let b = Box.store { x => total += x; total }
			`,
			want: "this block must not write outer state, but it writes `total`",
		},
		{
			name: "escaping block with return",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				makeReturner: Box! {
					Box.store { x => return Box }
				}
			`,
			want: "so this block may outlive this call and cannot use `return`",
		},
		{
			name: "escaping block with break",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				makeBreaker: Box! {
					Box.store { x => break Box }
				}
			`,
			want: "cannot use `break`",
		},
		{
			name: "non-escaping block with return",
			src: `
				first(xs: [Int!]!): Int! {
					xs.each { x => return x }
					0
				}
			`,
		},
		{
			name: "break inside a loop inside an escaping block",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				run: Box! {
					Box.store { x =>
						let i = 0
						loop { i += 1; if (i > x) { break } }
						i
					}
				}
			`,
		},
		{
			name: "return nested in a builtin's block inside an escaping block",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				run: Box! {
					Box.store { x =>
						[1].each { y => return Box }
						x
					}
				}
			`,
			want: "cannot use `return`",
		},
		{
			name: "either branch of a conditional escaping",
			src: `
				run: Int! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					double(x: Int!): Int! { x * 2 }
					let f = if (true) { &double } else { &bump }
					f(1)
				}
			`,
			want: "`bump` writes `n`",
		},
		{
			name: "impure closure in a record",
			src: `
				run: Int! {
					let n = 0
					bump(x: Int!): Int! { n += x; n }
					let r = {{f: &bump}}
					0
				}
			`,
			want: "`bump` writes `n`",
		},
		{
			name: "nested function closing over an escaping block parameter",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! {
						helper(x: Int!): Int! { block(x) + 1 }
						self.saved = &helper
						self
					}
				}
				run: Int! {
					let n = 0
					Box.store { x => n += x; n }.saved(1)
				}
			`,
			want: "`helper` calls `block` and escapes, so this block must not write outer state, but it writes `n`",
		},
		{
			name: "escaping a pure nested function that reads outer state",
			src: `
				type Box {
					saved(x: Int!): Int! { x }
				}
				run: Int! {
					let k = 3
					scale(x: Int!): Int! { x * k }
					let b = Box
					b.saved = &scale
					b.saved(2)
				}
			`,
		},
	})
}

// A closure or method ref created in a field default and using the object
// under construction may be called or passed as an argument, but not kept:
// it would keep reading the half-built object.
func TestFieldDefaultClosures(t *testing.T) {
	const box = `
		type Box {
			saved(x: Int!): Int! { x }
			store(&block(x: Int!): Int!): Box! { self.saved = block; self }
		}
	`
	runEffectCases(t, []effectCase{
		{
			name: "method ref stored in a field",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let h = &get }",
			want: "`get` is bound to the object under construction, so in a field default it can only be called or passed as an argument, not stored, returned, or put in a collection; create it in a method or computed field, or in new(), to use the finished object",
		},
		{
			name: "self. method ref stored in a field",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let h = &self.get }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "method ref in a list",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let hs = [&get] }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "method ref in a record",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let r = {{h: &get}} }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "method ref stored on another object",
			src: box + `
				type T {
					n: Int! = 1
					get(x: Int!): Int! { n + x }
					let b: Box! = Box.store { x => x }
					let wired: Box! = if (true) { let w = b; w.saved = &get; w } else { b }
				}
			`,
			want: "`get` is bound to the object under construction",
		},
		{
			name: "block using a field, passed to a storing method",
			src: box + `
				type T {
					n: Int! = 1
					let held = Box.store { x => n + x }
				}
			`,
			want: "`Box.store` stores, returns, or collects `block`, so in a field default this block must not use the object under construction, but it uses `n` from the object under construction",
		},
		{
			name: "block using self, passed to a storing method",
			src: box + `
				type T {
					n: Int! = 1
					let held = Box.store { x => self.n + x }
				}
			`,
			want: "but it uses `self`, the object under construction",
		},
		{
			name: "block calling a method, passed to a storing method",
			src: box + `
				type T {
					n: Int! = 1
					get(x: Int!): Int! { n + x }
					let held = Box.store { x => get(x) }
				}
			`,
			want: "but it uses `get` from the object under construction",
		},
		{
			name: "inner block using a field, inside a stored block",
			src: box + `
				type T {
					n: Int! = 1
					let held = Box.store { x => [x].map { y => n + y }.reduce(0) { a, b => a + b } }
				}
			`,
			want: "but it uses `n` from the object under construction",
		},
		{
			name: "block calling a nested function that uses a field",
			src: box + `
				type T {
					n: Int! = 1
					let held: Box! = {
						a(x: Int!): Int! { n + x }
						Box.store { x => a(x) }
					}
				}
			`,
			want: "this block must not use the object under construction, but it uses `n` from the object under construction",
		},
		{
			name: "ref to a nested function calling one that uses a field",
			src: box + `
				type T {
					n: Int! = 1
					let wired: Box! = {
						a(x: Int!): Int! { n + x }
						b(x: Int!): Int! { a(x) }
						let w = Box
						w.saved = &b
						w
					}
				}
			`,
			want: "`b` uses `n` from the object under construction",
		},
		{
			name: "nested function using a field in an argument default",
			src: box + `
				type T {
					n: Int! = 1
					let held: Box! = {
						a(x: Int!, k: Int! = n): Int! { x + k }
						Box.store { x => a(x) }
					}
				}
			`,
			want: "this block must not use the object under construction, but it uses `n` from the object under construction",
		},
		{
			name: "selections on another instance of the same type",
			src: box + `
				type Node {
					n: Int! = 1
					handle(x: Int!): Int! { x }
					let held = Box.store { y => let o = Node; o.{{n}}.n + y }
					let viaHandle = Box.store { y => let o = Node; o.{{handle(x: y)}}.handle }
				}
			`,
		},
		{
			name: "field holding another instance's method",
			src: `
				type Handler {
					next: Handler
					handle(x: Int!): Int! { x }
					let fallback = next.handle
					let chain = [fallback]
					let g = fallback
				}
			`,
		},
		{
			name: "method selected through self without &",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let h = self.get }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "method ref through a parenthesized self",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let h = &(self).get }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "method selected from self with {{ }}",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let h = self.{{get}}.get }",
			want: "`get` is bound to the object under construction",
		},
		{
			name: "fields selected from self with {{ }}",
			src:  "type T { n: Int! = 1\n m: Int! = 2\n let sum: Int! = self.{{n, m}}.n }",
		},
		{
			name: "function-valued fields that aren't methods",
			src: `
				double(x: Int!): Int! { x * 2 }
				type T {
					let f = &double
					let g = f
					let fs = [f]
				}
			`,
		},
		{
			name: "method called through self in a default",
			src:  "type T { n: Int! = 1\n get(x: Int!): Int! { n + x }\n let a: Int! = self.get(1)\n let b: Int! = (self).get(2) }",
		},
		{
			name: "blocks run while the default is evaluated",
			src: box + `
				type T {
					n: Int! = 1
					get(x: Int!): Int! { n + x }
					let total: Int! = [1, 2].reduce(0) { acc, x => acc + x + n }
					let viaMethod: Int! = [1, 2].map { x => get(x) }.reduce(0) { a, b => a + b }
					let called: Int! = get(1)
				}
			`,
		},
		{
			name: "stored block not using the object",
			src: box + `
				type T {
					n: Int! = 1
					let held = Box.store { x => x + 1 }
				}
			`,
		},
		{
			name: "same refs and blocks in a method, computed field, or new()",
			src: box + `
				type T {
					n: Int! = 1
					fromNew: Box!
					get(x: Int!): Int! { n + x }
					wired: Box! { let w = Box; w.saved = &get; w }
					held: Box! { Box.store { x => n + x } }
					viaSelf(y: Int!): Box! { let w = Box; w.saved = &self.get; w }
					new {
						self.fromNew = Box.store { x => n + x }
						self
					}
				}
			`,
		},
		{
			name: "static member refs",
			src: `
				type Counter {
					self {
						base: Int! = 2
						scale(x: Int!): Int! { x * base }
						let f = &scale
					}
				}
			`,
		},
	})
}
