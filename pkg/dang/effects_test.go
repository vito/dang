package dang

import (
	"context"
	"strings"
	"testing"

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
	})
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
	})
}
