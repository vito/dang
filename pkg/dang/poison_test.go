package dang

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// inferErrors type-checks src as inferWithEffects does and returns each
// error reported, nested ones (a block's, a function body's) flattened.
func inferErrors(t *testing.T, src string) []string {
	t.Helper()
	parsed, err := ParseWithRecovery("poison", []byte(src))
	require.NoError(t, err)
	file := parsed.(*FileBlock)
	typeScope, _ := BuildScopesFromImports("", nil)
	ctx, solver, _ := withEffectSolver(withTopLevelFrame(context.Background(), typeScope))
	var msgs []string
	var flatten func(error)
	flatten = func(err error) {
		var ie *InferenceErrors
		if errors.As(err, &ie) {
			for _, e := range ie.Errors {
				flatten(e)
			}
			return
		}
		msgs = append(msgs, err.Error())
	}
	if _, err := InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher()); err != nil {
		flatten(err)
	}
	for _, err := range solver.solve() {
		flatten(err)
	}
	return msgs
}

// A declaration that fails to type-check reports its error once: every
// later use of the name it would have bound is quiet, rather than "not
// found" or an error about a type it never had. Errors that don't involve
// it are still reported.
func TestFailedDeclarationsDontCascade(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "top-level let",
			src: `let c = 1 + "x"
let d = c.foo
print(c.length)
let e: String! = c
let f = c(1)
let g = "${c}"
let h = c.{{a}}
let i = c[0]
let j = &c
let k = case (c) { x: Int! => 1, else => 2 }`,
			want: []string{"operator addition is not defined between types Int! and String!"},
		},
		{
			name: "transitively",
			src:  "let a = nope\nlet b = a + 1\nlet c = b.x",
			want: []string{`"nope" not found`},
		},
		{
			name: "let in a block",
			src:  "let g = {\n  let e = undefinedThing\n  e + 1\n}",
			want: []string{`"undefinedThing" not found`},
		},
		{
			name: "let in a block argument",
			src:  "let r = [1].map { x =>\n  let q = x.nope\n  q + 1\n}",
			want: []string{`field "nope" not found in Int`},
		},
		{
			name: "let in a function body",
			src:  "pub f: Int! {\n  let q = nope\n  q + 1\n}",
			want: []string{`"nope" not found`},
		},
		{
			name: "top-level let read in a function body",
			src:  "let c = nope\npub f: Int! { c + 1 }",
			want: []string{`"nope" not found`},
		},
		{
			name: "reassigning a failed let",
			src:  "let c = nope\nc = 2\nc += 1",
			want: []string{`"nope" not found`},
		},
		{
			name: "nested function in a block",
			src:  "let r = {\n  let f(x: Int!): Int! { x + \"a\" }\n  f(1) + 1\n}",
			want: []string{"operator addition is not defined between types Int! and String!"},
		},
		{
			name: "private field of a type",
			src:  "type T {\n  let a = nope\n  pub b: Int! { a.foo }\n}",
			want: []string{`"nope" not found`},
		},
		{
			name: "private field of a type read through self",
			src:  "type U {\n  let p = nope\n  pub r: String! { self.p }\n  pub s: Int! { self.p + 1 }\n}",
			want: []string{`"nope" not found`},
		},
		{
			name: "an annotated let keeps its type",
			src:  "let c: Int! = nope\nlet d = c.foo",
			want: []string{`"nope" not found`, `field "foo" not found in Int`},
		},
		{
			name: "an annotated let in a block keeps its type",
			src:  "let f = {\n  let y: Int! = nope\n  y.foo\n}",
			want: []string{`"nope" not found`, `field "foo" not found in Int`},
		},
		{
			name: "a type with a failed field keeps its constructor",
			src:  "type T {\n  pub a: Int! = nope\n  pub b: Int! = 1\n}\nlet x = T(b: \"str\")\nlet y = T.zzz",
			want: []string{`"nope" not found`, `argument "b"`, `field "zzz" not found`},
		},
		{
			name: "a nested function keeps its declared signature",
			src:  "let r = {\n  let f(x: Int!): Int! { x + \"a\" }\n  f(\"s\") + 1\n}",
			want: []string{"operator addition is not defined between types Int! and String!", `argument "x"`},
		},
		{
			name: "independent errors are still reported",
			src:  "let a = nope\nlet b = alsoNope",
			want: []string{`"nope" not found`, `"alsoNope" not found`},
		},
		{
			name: "later errors in the block are still reported",
			src:  "let r = {\n  let a = nope\n  let b = 1\n  a + b.foo\n  b.bar\n}",
			want: []string{`"nope" not found`, `field "bar" not found in Int`},
		},
		{
			name: "a top-level function keeps its signature",
			src:  "pub f(x: Int!): Int! { x + \"a\" }\nlet p = f(1).foo",
			want: []string{"operator addition is not defined between types Int! and String!", `field "foo" not found in Int`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inferErrors(t, tc.src)
			require.Len(t, got, len(tc.want), "errors:\n%s", got)
			for _, want := range tc.want {
				found := false
				for _, msg := range got {
					if strings.Contains(msg, want) {
						found = true
						break
					}
				}
				require.True(t, found, "no error contains %q:\n%s", want, got)
			}
		})
	}
}
