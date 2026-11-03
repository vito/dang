package dang

import (
	"context"
	"fmt"
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

// W spans every file of a directory module: a statement in one file writing
// a binding puts it off limits to declarations and functions in the others.
func TestRunDirStatementStateAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.dang"), []byte("pub sum: Int! = 0\nsum = 5\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.dang"), []byte("readSum: Int! { sum }\nlet avg = sum / 2\n"), 0o600))
	_, err := RunDir(context.Background(), dir, false)
	require.ErrorContains(t, err, "`readSum` reads `sum`, which a top-level statement writes (a.dang:2)")
	require.ErrorContains(t, err, "`avg`'s initializer reads `sum`, which a top-level statement writes (a.dang:2)")
}

// An imported Dang module is checked the same wherever the import is: an
// import written inside a top-level statement doesn't make the module's
// functions statement code.
func TestStatementStateInImportedModule(t *testing.T) {
	for name, main := range map[string]string{
		"top level":         "import Lib\nprint(1)\n",
		"in a statement":    "[1].each { x => import Lib; print(x) }\n",
		"in an initializer": "let x = { import Lib; 1 }\nprint(x)\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "dang.toml"), []byte("[imports.Lib]\npath = \"./lib\"\n"), 0o600))
			require.NoError(t, os.Mkdir(filepath.Join(dir, "lib"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "main.dang"), []byte("pub sum: Int! = 0\nsum = 5\npub readSum: Int! { sum }\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(main), 0o600))
			err := RunFile(context.Background(), filepath.Join(dir, "main.dang"), false)
			require.ErrorContains(t, err, "`readSum` reads `sum`, which a top-level statement writes")
		})
	}
}

// Inferred without a top-level frame, nothing checks that a local type's code
// captures nothing, so its item can't be trusted to mean what the code says:
// making it fails rather than resolve a captured name elsewhere.
func TestUncheckedLocalTypeHasNoItem(t *testing.T) {
	parsed, err := ParseWithRecovery("unchecked", []byte(`
		let n = 100
		f(n: Int!): Int! {
			type T { pub get: Int! = n }
			T.get
		}
	`))
	require.NoError(t, err)
	forms := parsed.(*FileBlock).Forms
	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(context.Background(), forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err)
	err = EvalTypeItems(context.Background(), forms, valueScope)
	require.ErrorContains(t, err, "type T was inferred without a top-level frame")
}

// A type declared in a function, block or type body captures nothing: its
// methods, constructor, field defaults and statics may use its own members,
// the top level and other types, but no binding of the scope around it.
func TestLocalTypesCaptureNothing(t *testing.T) {
	const captures = "cannot use `%s`, which is declared in the scope around %s"
	runEffectCases(t, []effectCase{
		{
			name: "method reading an enclosing local",
			src: `
				interface Getter { get: Int! }
				make: Getter! {
					let n = 0
					type T implements Getter { get: Int! { n } }
					T
				}
			`,
			want: fmt.Sprintf(captures, "n", "T"),
		},
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
			want: fmt.Sprintf(captures, "n", "T"),
		},
		{
			name: "field default reading a parameter",
			src: `
				run(n: Int!): Int! {
					type T { a: Int! = n }
					T.a
				}
			`,
			want: fmt.Sprintf(captures, "n", "T"),
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
			want: fmt.Sprintf(captures, "n", "T"),
		},
		{
			name: "static reading an enclosing local",
			src: `
				run: Int! {
					let n = 1
					type T { self { k: Int! = n + 1 } }
					T.k
				}
			`,
			want: fmt.Sprintf(captures, "n", "T"),
		},
		{
			name: "method calling an enclosing function",
			src: `
				run: Int! {
					helper: Int! { 1 }
					type T { go: Int! { helper } }
					T.go
				}
			`,
			want: fmt.Sprintf(captures, "helper", "T"),
		},
		{
			name: "method calling an enclosing block parameter",
			src: `
				wrap(&cb: Int!): Int! {
					type T { go: Int! { cb } }
					T.go
				}
			`,
			want: fmt.Sprintf(captures, "cb", "T"),
		},
		{
			name: "nested type reading its outer type's field",
			src: `
				type Outer {
					level: Int! = 1
					type Inner { x: Int! { level } }
					go: Int! { Inner.x }
				}
			`,
			want: fmt.Sprintf(captures, "level", "Inner"),
		},
		{
			name: "local scalar method reading an enclosing local",
			src: `
				run: String! {
					let n = 0
					scalar S { bump: Int! { n } }
					"a"
				}
			`,
			want: fmt.Sprintf(captures, "n", "S"),
		},
		{
			// Decoding may build T anywhere, so its methods see no null
			// check made around it.
			name: "method reading a name narrowed around its type",
			src: `
				pub name: String = null
				run: String! {
					if (name != null) {
						type T { shout: String! { name.toUpper } }
						T.shout
					} else {
						""
					}
				}
			`,
			want: "return type mismatch: declared String!, inferred String",
		},
		{
			// It may check the name again itself.
			name: "type code rechecking a name narrowed around its type",
			src: `
				pub name: String = null
				run: String! {
					if (name != null) {
						type T {
							shout: String! { if (name != null) { name.toUpper } else { "" } }
							self { k: String! = name ?? "none" }
						}
						T.shout + T.k
					} else {
						""
					}
				}
			`,
		},
		{
			// The type's item scope binds only the types declared before
			// it, so `x` means the top-level one at runtime too, as it did
			// when checked.
			name: "method reading a top-level name a later local shadows",
			src: `
				let x = 10
				run: Int! {
					type T { w: Int! { x } }
					let x = 20
					T.w + x
				}
			`,
		},
		{
			name: "top level, other types, builtins and own members are fine",
			src: `
				let base = 10
				helper(x: Int!): Int! { x * 2 }
				type Outer {
					type Inner { value: Int! = base }
					type Inner2 { i: Inner! = Inner }
					make: Int! { Inner2.i.value }
				}
				run: Int! {
					type A { v: Int! = helper(base) }
					type B {
						a: A! = A
						total: Int! = 0
						add(x: Int!): B! {
							let local = 0
							bump: Int! { local += 1; total += x; local }
							[1, 2].each { y => bump }
							self
						}
						w: Int! { a.v + [1].length + Outer.make }
						self { k: Int! = base + 1 }
					}
					B.add(1).w + B.k
				}
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
	})
}

// Top-level declarations' initializers write only their own locals, so
// forcing them concurrently (from `{{ }}` fields) can't race.
func TestTopLevelInitializerWrites(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "direct write",
			src: `
				let total = 0
				let a = { total += 1; 1 }
			`,
			want: "cannot assign to `total` in `a`'s initializer",
		},
		{
			name: "write in a block the initializer runs",
			src: `
				let total = 0
				let b = { [1, 2].each { x => total += x }; 2 }
			`,
			want: "cannot assign to `total` in `b`'s initializer",
		},
		{
			name: "field-path write",
			src: `
				type Box { n: Int! = 0 }
				let box = Box
				let c = { box.n = 5; 3 }
			`,
			want: "cannot assign to `box` in `c`'s initializer",
		},
		{
			name: "write in a {{ }} field of an initializer",
			src: `
				let total = 0
				let r = {{ p: { total += 1; 1 } }}
			`,
			want: "this assignment writes `total`, which is declared outside the field",
		},
		{
			// What #233 checked from the reading side: whatever forces a
			// and b concurrently, their initializers are rejected.
			name: "initializers forced from fields through functions",
			src: `
				let total = 0
				let r = {{ p: readA, q: readB }}
				readA: Int! { a }
				readB: Int! { helper }
				helper: Int! { b }
				let a = { total += 1; 1 }
				let b = { total += 1; 2 }
			`,
			want: "cannot assign to `total` in `b`'s initializer",
		},
		{
			name: "locals, method forks and statements are fine",
			src: `
				type Counter { n: Int! = 0; bump: Counter! { self.n += 1; self } }
				let xs = [1, 2, 3]
				let doubled = { let out = []; xs.each { x => out += [x * 2] }; out }
				let sum = xs.reduce(0) { a, x => a + x }
				let c = Counter.bump.bump
				let total = 0
				xs.each { x => total += x }
				let r = {{ p: later, q: c.n, s: doubled }}
				let later = { let t = 0; xs.each { x => t += x }; t }
			`,
		},
	})
}

// An assignment's target is written, not read: an initializer writing a
// binding a statement writes too gets rule 1's error alone.
func TestInitializerWriteToStatementStateReportedOnce(t *testing.T) {
	got := inferWithEffects(t, "let total = 0\ntotal = 3\nlet a = { total += 1; total = 2; 1 }\n")
	require.Contains(t, got, "cannot assign to `total` in `a`'s initializer")
	require.NotContains(t, got, "initializer reads `total`")
}

// A top-level binding some top-level statement writes (W) may only be read
// by statement code: declarations run before every statement, and function
// and type code may run while declarations initialize.
func TestTopLevelStatementState(t *testing.T) {
	runEffectCases(t, []effectCase{
		{
			name: "declaration reading W",
			src: `
				let sum = 0
				[1, 2, 3].each { x => sum += x }
				let avg = sum / 3
			`,
			want: "`avg`'s initializer reads `sum`, which a top-level statement writes (line 3)",
		},
		{
			name: "function reading W",
			src: `
				let sum = 0
				sum = 5
				readSum: Int! { sum }
			`,
			want: "`readSum` reads `sum`, which a top-level statement writes (line 3)",
		},
		{
			name: "field default reading W",
			src: `
				let sum = 0
				sum = 5
				type Box { v: Int! = sum }
			`,
			want: "the body of `Box` reads `sum`",
		},
		{
			// A type declared in a statement is no statement code: its
			// statics run before any statement.
			name: "static of a type declared in a statement reading W",
			src: `
				let sum = 0
				sum = 5
				if (true) {
					type Box { self { v: Int! = sum } }
					print(Box.v)
				}
			`,
			want: "the body of `Box` reads `sum`, which a top-level statement writes (line 3)",
		},
		{
			name: "method of a type declared in a statement reading W",
			src: `
				let sum = 0
				sum = 5
				if (true) {
					type Box { get: Int! { sum } }
					print(Box.get)
				}
			`,
			want: "`Box.get` reads `sum`, which a top-level statement writes (line 3)",
		},
		{
			name: "field-path write puts the root in W",
			src: `
				type Box { n: Int! = 0 }
				let box = Box
				box.n = 5
				let peek = box.n
			`,
			want: "`peek`'s initializer reads `box`, which a top-level statement writes (line 4)",
		},
		{
			// #247: the ref travels in a method slot whose type says
			// nothing about it; readSum is rejected where it reads.
			name: "ref a function returned",
			src: `
				let sum = 0
				readSum: Int! { sum }
				type Box { get: Int! { 0 } }
				mk: Box! { let b = Box; b.get = &readSum; b }
				sum = 5
				let s = mk.get
			`,
			want: "`readSum` reads `sum`, which a top-level statement writes (line 6)",
		},
		{
			name: "escaping block a function result holds",
			src: `
				let sum = 0
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				mk: Box! { Box.store { x => sum + x } }
				sum = 5
				let s = mk.saved(1)
			`,
			want: "`mk` reads `sum`, which a top-level statement writes (line 8)",
		},
		{
			name: "method reached through an interface",
			src: `
				let sum = 0
				interface Getter { get: Int! }
				type Box implements Getter { get: Int! { sum } }
				let held: Getter! = Box
				sum = 5
				let s = held.get
			`,
			want: "`Box.get` reads `sum`, which a top-level statement writes (line 6)",
		},
		{
			name: "W is a set: a statement's position doesn't matter",
			src: `
				let p = null
				let s = p
				p = 1
			`,
			want: "`s`'s initializer reads `p`, which a top-level statement writes (line 4)",
		},
		{
			name: "assignment after a top-level guard",
			// The guard narrows the statements after it, which are
			// inferred in a narrowed clone of the module scope; the rule
			// must still see them as top-level statements.
			src: `
				let p: Int = 1
				if (p == null) { raise "missing" }
				p = 5
				let s = p
			`,
			want: "`s`'s initializer reads `p`, which a top-level statement writes (line 4)",
		},
		{
			name: "storing and calling refs in statements is fine",
			src: `
				let sum = 0
				let xs = [1, 2]
				type Box {
					saved(x: Int!): Int! { x }
					store(&block(x: Int!): Int!): Box! { self.saved = block; self }
				}
				xs.each { x => sum += x }
				print(Box.store { x => sum + x }.saved(1))
				let held = Box.store { x => x * 2 }
				print(held.saved(sum))
			`,
		},
		{
			name: "statements and blocks in them may read W",
			src: `
				let sum = 0
				[1, 2, 3].each { x => sum += x }
				double(n: Int!): Int! { n * 2 }
				print(double(sum))
				[1].each { x => print(sum + x) }
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
