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

// A type declared in a function body is a value like any other: it can be
// stored, returned and used concurrently, so its methods, constructor and
// field defaults may not write (or call anything writing) outside the type.
func TestLocalTypesSealed(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "method writing an enclosing local",
			src: `
				interface Bumper { bump: Int! }
				make: Bumper! {
					let n = 0
					type T implements Bumper { bump: Int! { n += 1; n } }
					T
				}
			`,
			want: "cannot assign to `n`: it is declared outside T",
		},
		{
			name: "field default writing an enclosing local",
			src: `
				run: Int! {
					let n = 0
					type T { a: Int! = { n = 5; 1 } }
					T.a + n
				}
			`,
			want: "cannot assign to `n`: it is declared outside T",
		},
		{
			name: "constructor writing an enclosing local",
			src: `
				run: Int! {
					let n = 0
					type T { a: Int! = 1; new { n += 1; self } }
					T.a + n
				}
			`,
			want: "cannot assign to `n`: it is declared outside T",
		},
		{
			name: "method calling an impure enclosing closure",
			src: `
				run: Int! {
					let n = 0
					bump: Int! { n += 1; n }
					type T { go: Int! { bump } }
					T.go
				}
			`,
			want: "`bump` writes `n`, which is declared outside T",
		},
		{
			name: "method passing an impure block to a builtin",
			src: `
				run: Int! {
					let n = 0
					type T { go: Int! { [1, 2].each { x => n += x }; 0 } }
					T.go
				}
			`,
			want: "this block writes `n`, which is declared outside T",
		},
		{
			name: "method calling an enclosing block parameter",
			src: `
				wrap(&cb: Int!): Int! {
					type T { go: Int! { cb } }
					T.go
				}
				run: Int! {
					let k = 0
					wrap { k += 1; k }
				}
			`,
			want: "`T.go` calls `cb`, and T's methods, constructor and field defaults run wherever its values are used",
		},
		{
			name: "method passing an enclosing pure block parameter",
			src: `
				wrap(&cb: Int!): Int! {
					type T { go: Int! { cb } }
					T.go
				}
				run: Int! { wrap { 1 } }
			`,
		},
		{
			name: "statics writing their own locals",
			src: `
				type T {
					a: Int! = 1
					self { total: Int! = { let k = 0; [1, 2].each { x => k += x }; k += 1; k } }
				}
				run: Int! {
					type U { self { total: Int! = { let k = 0; k += 1; k } } }
					T.total + U.total
				}
			`,
		},
		{
			name: "local scalar method writing an enclosing local",
			src: `
				run: String! {
					let n = 0
					scalar S { bump: Int! { n += 1; n } }
					"a"
				}
			`,
			want: "cannot assign to `n`: it is declared outside S",
		},
		{
			name: "own state, reads and nested closures are fine",
			src: `
				run: Int! {
					let k = 2
					scale(x: Int!): Int! { x * k }
					type T {
						total: Int! = 0
						add(x: Int!): T! {
							let local = 0
							bump: Int! { local += 1; total += scale(x); local }
							[1, 2].each { y => bump }
							self
						}
					}
					T.add(1).total
				}
			`,
		},
	})
}

// A top-level declaration is forced on first read, wherever that is; one
// whose initializer writes top-level state must not be forced from a `{{ }}`
// field, where it would run concurrently with other fields.
func TestTopLevelInitializersInFields(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "forward reference from a field",
			src: `
				let total = 0
				let r = {{ p: a, q: b }}
				let a = { total += 1; 1 }
				let b = { total += 1; 2 }
			`,
			want: "`a` is declared after `r`, so reading it here runs its initializer, which may write `total`",
		},
		{
			name: "write through a block the initializer runs",
			src: `
				let total = 0
				let r = {{ p: a }}
				let a = { [1, 2].each { x => total += x }; 1 }
			`,
			want: "`a` is declared after `r`",
		},
		{
			name: "forcing a declaration that writes",
			src: `
				let total = 0
				let r = {{ p: c }}
				let c = a + 1
				let a = { total += 1; 1 }
			`,
			want: "`c` is declared after `r`, so reading it here runs its initializer, which may write `total`",
		},
		{
			name: "field in a function body",
			src: `
				let total = 0
				let a = { total += 1; 1 }
				g: Int! { {{ p: a }}.p }
			`,
			want: "reading `a` here may run its initializer, which may write `total`",
		},
		{
			name: "field calling functions that read later declarations",
			src: `
				let total = 0
				let r = {{ p: readA, q: readB }}
				readA: Int! { a }
				readB: Int! { helper }
				helper: Int! { b }
				let a = { total += 1; 1 }
				let b = { total += 1; 2 }
			`,
			want: "`readB` reads `b` (through `helper`), which is declared after `r`, so calling it here runs `b`'s initializer, which may write `total`",
		},
		{
			name: "field calling a method that reads a later declaration",
			src: `
				let total = 0
				type T { get: Int! { a } }
				let r = {{ p: T.get }}
				let a: Int! = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `T.get`), which is declared after `r`",
		},
		{
			name: "field calling a ref a function returned",
			src: `
				let total = 0
				readA: Int! { a }
				type Box { get: Int! { 0 } }
				boxA: Box! { let b = Box; b.get = &readA; b }
				let r = {{ p: boxA.get }}
				let a = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `readA`, which may be stored in `Box.get`), which is declared after `r`, so calling it here runs `a`'s initializer, which may write `total`",
		},
		{
			name: "field calling a ref held by a declaration",
			src: `
				let total = 0
				readA: Int! { a }
				type Box { get: Int! { 0 } }
				boxA: Box! { let b = Box; b.get = &readA; b }
				let held = boxA
				let r = {{ p: held.get }}
				let a = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `readA`, which may be stored in `Box.get`), which is declared after `r`, so calling it here runs `a`'s initializer, which may write `total`",
		},
		{
			name: "field calling a returned ref through a local",
			src: `
				let total = 0
				readA: Int! { a }
				type Box { get: Int! { 0 } }
				boxA: Box! { let b = Box; b.get = &readA; b }
				let r = { let x = boxA; {{ p: x.get }} }
				let a = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `readA`, which may be stored in `Box.get`), which is declared after `r`",
		},
		{
			name: "field in a function calling a returned ref through a parameter",
			src: `
				let total = 0
				readA: Int! { a }
				type Box { get: Int! { 0 } }
				boxA: Box! { let b = Box; b.get = &readA; b }
				both(x: Box!): Int! { {{ p: x.get }}.p }
				let a = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `readA`, which may be stored in `Box.get`), whose initializer may write `total`, so calling it here may run that initializer",
		},
		{
			name: "field calling a stored ref through an interface",
			src: `
				let total = 0
				readA: Int! { a }
				readZero: Int! { 0 }
				interface Getter { get: Int! }
				type Box implements Getter { pub get = &readZero }
				boxA: Getter! { Box(get: &readA) }
				let r = { let x = boxA; {{ p: x.get }} }
				let a = { total += 1; 1 }
			`,
			want: "`get` reads `a` (through `readA`, which may be stored in `Box.get`), which is declared after `r`",
		},
		{
			name: "field calling a declaration another initializer stored a ref in",
			src: `
				let total = 0
				readA: Int! { 1 }
				readB: Int! { b }
				pub fn = &readA
				let init = { fn = &readB; 1 }
				let r = {{ p: fn }}
				let b = { total += 1; 2 }
			`,
			want: "`fn` reads `b` (through `readB`, which may be stored in `fn`), which is declared after `r`",
		},
		{
			name: "returned refs reading nothing that writes are fine",
			src: `
				let total = 0
				readA: Int! { a }
				type Box { get: Int! { 0 } }
				type Fixed { get: Int! { 0 } }
				boxA: Box! { let b = Box; b.get = &readA; b }
				pure: Fixed! { let k = 3; three: Int! { k }; let b = Fixed; b.get = &three; b }
				let a = { total += 1; 1 }
				let r = {{ p: boxA.get, q: pure.get }}
				let held = boxA
				let s = {{ p: held.get, q: later }}
				let later = pure
			`,
		},
		{
			name: "earlier declarations, pure ones, and statements are fine",
			src: `
				let total = 0
				let a = { total += 1; 1 }
				let r = {{ p: a, q: later }}
				let later = 5
				let s = {{ p: a }}
				print({{ p: a, q: later }})
			`,
		},
	})
}

// Top-level declarations all run before top-level statements, so a statement
// can't write what a later declaration reads: the declaration would see the
// value from before the write.
func TestTopLevelStatementOrder(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "assignment before a declaration reading it",
			src: `
				let p = null
				p = 1
				let s = p
			`,
			want: "top-level declarations run before top-level statements, so `s` (line 4) reads `p` before this statement writes it",
		},
		{
			name: "assignment after a top-level guard",
			// The guard narrows the statements after it, which are
			// inferred in a narrowed clone of the module scope; the order
			// check must still see them as top-level statements.
			src: `
				let p: Int = 1
				if (p == null) { raise "missing" }
				p = 5
				let s = p
			`,
			want: "so `s` (line 5) reads `p` before this statement writes it",
		},
		{
			name: "block writing before a declaration reading it",
			src: `
				let sum = 0
				[1, 2, 3].each { x => sum += x }
				let s2 = sum
			`,
			want: "so `s2` (line 4) reads `sum` before this statement writes it",
		},
		{
			name: "declaration calling a function that reads the binding",
			src: `
				let sum = 0
				[1, 2, 3].each { x => sum += x }
				let s2 = getSum
				getSum: Int! { viaHelper }
				viaHelper: Int! { sum }
			`,
			want: "so `s2` (line 4) reads `sum` (through `viaHelper`) before this statement writes it",
		},
		{
			name: "declaration constructing a type whose defaults read the binding",
			src: `
				let sum = 0
				sum = 6
				type Box { v: Int! = sum }
				let s3 = Box.v
			`,
			want: "so `s3` (line 5) reads `sum` (through `Box`) before this statement writes it",
		},
		{
			name: "declaration calling a stored ref that reads the binding",
			src: `
				let sum = 0
				getSum: Int! { sum }
				let g = &getSum
				sum = 5
				let v = g()
			`,
			want: "so `v` (line 6) reads `sum` (through `getSum`) before this statement writes it",
		},
		{
			name: "storing a ref is fine",
			src: `
				let sum = 0
				getSum: Int! { sum }
				let g = &getSum
				sum = 5
				print(g())
			`,
		},
		{
			name: "constructing a type whose methods read the binding is fine",
			src: `
				let sum = 0
				sum = 6
				type Box { v: Int! = 1; get: Int! { sum } }
				let b = Box
				print(b.get)
			`,
		},
		{
			name: "statements reading, and declarations before the write, are fine",
			src: `
				let total = 0
				let start = total
				[1, 2, 3].each { x => total += x }
				print(total)
				total = 10
				print([start, total])
				let doubled = { let t = 0; [1, 2].each { x => t += x * 2 }; t }
			`,
		},
		{
			name: "declaration calling a ref a function returned",
			// Box.get's type says nothing about readSum: mk's result
			// carries the ref it stored.
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				mk: Box! { let b = Box; b.get = &readSum; b }
				sum = 5
				let s = mk.get
			`,
			want: "so `s` (line 7) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref held by an earlier function result",
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				inner: Box! { let b = Box; b.get = &readSum; b }
				mk: Box! { inner }
				let held = mk
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 9) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "initializer calling a ref it stored",
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				sum = 5
				let s = { let b = Box; b.get = &readSum; b.get }
			`,
			want: "so `s` (line 6) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref another initializer stored in it",
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				let holder = Box
				let init = { holder.get = &readSum; 1 }
				sum = 5
				let s = holder.get
			`,
			want: "so `s` (line 8) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling an escaping block a function result holds",
			src: `
				let sum = 0
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				mk: Box! { Box.store { x => sum + x } }
				let held = mk
				sum = 5
				let s = held.saved(1)
			`,
			want: "so `s` (line 10) reads `sum` (through `Box.saved`) before this statement writes it",
		},
		{
			name: "declaration calling an escaping block stored under a bare name",
			src: `
				let sum = 0
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { saved = block; self }
				}
				mk: Box! { Box.store { x => sum + x } }
				let held = mk
				sum = 5
				let s = held.saved(1)
			`,
			want: "so `s` (line 10) reads `sum` (through `Box.saved`) before this statement writes it",
		},
		{
			name: "blocks stored by top-level statements are fine",
			src: `
				let sum = 0
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				print(Box.store { x => sum + x }.saved(1))
				sum = 5
				let s = Box.saved(1)
			`,
		},
		{
			name: "declaration reading a binding another initializer stored a ref in",
			// fn's type is readA's: the ref is fn's, not readA's.
			src: `
				let sum = 0
				readA: Int! { 1 }
				readB: Int! { sum }
				pub fn = &readA
				let init = { fn = &readB; 1 }
				sum = 5
				let s = readA
				let t = fn
			`,
			want: "so `t` (line 9) reads `sum` (through `readB`, which may be stored in `fn`) before this statement writes it",
		},
		{
			name: "a function whose type a binding has doesn't read what's stored in it",
			src: `
				let sum = 0
				readA: Int! { 1 }
				readB: Int! { sum }
				pub fn = &readA
				let init = { fn = &readB; 1 }
				sum = 5
				let s = readA
			`,
		},
		{
			name: "declaration calling a method through an interface",
			src: `
				let sum = 0
				interface Getter { get: Int! }
				type Box implements Getter { get: Int! { sum } }
				let held: Getter! = Box
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 7) reads `sum` (through `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a method through an extended interface",
			src: `
				let sum = 0
				interface Getter { get: Int! }
				interface SubGetter implements Getter { get: Int! }
				type Box implements SubGetter { get: Int! { sum } }
				let held: Getter! = Box
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 8) reads `sum` (through `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a field default through an interface",
			src: `
				let sum = 0
				readSum: Int! { sum }
				interface Getter { get: Int! }
				type Box implements Getter { pub get = &readSum }
				let held: Getter! = Box
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 8) reads `sum` (through `readSum`) before this statement writes it",
		},
		{
			name: "declaration calling a member ref stored in a slot",
			src: `
				let sum = 0
				readZero: Int! { 0 }
				readSum: Int! { sum }
				type C { pub get = &readZero }
				type Box { get: Int! { 0 } }
				mkC: C! { let c = C; c.get = &readSum; c }
				mk: Box! { let c = mkC; let b = Box; b.get = &c.get; b }
				sum = 5
				let s = mk.get
			`,
			want: "so `s` (line 10) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref stored from a conditional",
			src: `
				let sum = 0
				readZero: Int! { 0 }
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				mk(z: Boolean!): Box! { let x = Box; x.get = if (z) { &readZero } else { &readSum }; x }
				let held = mk(false)
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 9) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref stored from a reassigned local",
			src: `
				let sum = 0
				readZero: Int! { 0 }
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				mk: Box! { let f = &readZero; f = &readSum; let x = Box; x.get = &f; x }
				let held = mk
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 9) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref stored from a reassigned declaration",
			src: `
				let sum = 0
				readA: Int! { 1 }
				readB: Int! { sum }
				type Box { get: Int! { 0 } }
				pub fn = &readA
				let init = { fn = &readB; 1 }
				let holder = Box
				let init2 = { holder.get = &fn; 1 }
				sum = 5
				let s = holder.get
			`,
			want: "so `s` (line 11) reads `sum` (through `readB`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a function stored from a conditional by name",
			src: `
				let sum = 0
				type Box { get(x: Int!): Int! { x } }
				f(x: Int!): Int! { x }
				g(x: Int!): Int! { sum + x }
				mk(z: Boolean!): Box! { let b = Box; b.get = if (z) { f } else { g }; b }
				let held = mk(false)
				sum = 5
				let s = held.get(1)
			`,
			want: "so `s` (line 9) reads `sum` (through `g`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "declaration calling a ref stored over a parameter in a local",
			src: `
				let sum = 0
				addSum(x: Int!): Int! { sum + x }
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { let g = block; g = &addSum; self.saved = g; self }
				}
				mk: Box! { Box.store { x => x } }
				let held = mk
				sum = 5
				let s = held.saved(1)
			`,
			want: "so `s` (line 11) reads `sum` (through `addSum`, which may be stored in `Box.saved`) before this statement writes it",
		},
		{
			name: "declaration calling a block stored over a field default",
			src: `
				let sum = 0
				idf(x: Int!): Int! { x }
				type Box {
					pub saved = &idf
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				mk: Box! { Box.store { x => sum + x } }
				let held = mk
				sum = 5
				let s = held.saved(1)
			`,
			want: "so `s` (line 11) reads `sum` (through `Box.saved`) before this statement writes it",
		},
		{
			name: "field defaults and constructor arguments that are refs aren't calls",
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { pub get = &readSum }
				sum = 5
				let b = Box
				let c = Box(get: &readSum)
				let r = {{p: Box, q: 1}}
			`,
		},
		{
			name: "declaration calling a ref stored in a field typed by its initializer",
			src: `
				let sum = 0
				readZero: Int! { 0 }
				readSum: Int! { sum }
				type Box { pub get = &readZero }
				mk: Box! { Box(get: &readSum) }
				let held = mk
				sum = 5
				let s = held.get
			`,
			want: "so `s` (line 9) reads `sum` (through `readSum`, which may be stored in `Box.get`) before this statement writes it",
		},
		{
			name: "a function a field's type comes from doesn't read what's stored in it",
			src: `
				let sum = 0
				readZero: Int! { 0 }
				readSum: Int! { sum }
				type Box { pub get = &readZero }
				mk: Box! { let b = Box; b.get = &readSum; b }
				mk2: Box! { Box(get: &readSum) }
				let held = mk
				sum = 5
				let z = readZero
			`,
		},
		{
			name: "blocks that don't escape aren't carried",
			src: `
				let sum = 0
				mkList: [Int!]! { [1, 2].map { x => sum + x } }
				let xs = mkList
				sum = 5
				let ys = xs
			`,
		},
		{
			name: "returned refs reading no top-level binding, or called in statements, are fine",
			// Slots are told apart by type: only Box.get may call readSum.
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				type Fixed { get: Int! { 0 } }
				mk: Box! { let b = Box; b.get = &readSum; b }
				pure: Fixed! { let k = 3; three: Int! { k }; let b = Fixed; b.get = &three; b }
				let held = mk
				let p = pure
				sum = 5
				let q = pure.get
				let r = p.get
				print([held.get, mk.get, q, r])
			`,
		},
	})
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
