package dang

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// Evaluation classifies an object body after inference has wrapped each typed
// field's value in a Coerce. A method must still be a function there, as it
// was for inference, rather than a computed variable that every new object
// installs as a lazy initializer and then forces.
func TestClassifyFormsAfterInference(t *testing.T) {
	src := `
type Counter {
  start: Int!
  let step = 1
  let total: Int! = start + step
  next(n: Int!): Int! { total + n }
  doubled: Int! { total * 2 }
}
`
	parsed, err := ParseWithRecovery("classify_test", []byte(src))
	require.NoError(t, err)
	file := parsed.(*FileBlock)
	typeScope, _ := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err)

	body := file.Forms[0].(*ObjectDecl).Value.Forms
	fields := map[string]*FieldDecl{}
	for _, form := range body {
		f := form.(*FieldDecl)
		fields[f.Name.Name] = f
	}
	_, wrapped := fields["next"].Value.(*Coerce)
	require.True(t, wrapped, "inference no longer wraps a method in a Coerce; this test needs another way to reach the inferred form")

	names := func(forms []Node) []string {
		var out []string
		for _, form := range forms {
			out = append(out, form.(*FieldDecl).Name.Name)
		}
		return out
	}
	classified := classifyForms(body)
	require.Equal(t, []string{"next", "doubled"}, names(classified.Functions))
	require.Equal(t, []string{"total"}, names(classified.Variables))
	require.Equal(t, []string{"start", "step"}, names(classified.Constants))
}

func TestDeclarationOnlyTypeBodies(t *testing.T) {
	for _, body := range []string{
		`print("action")`, `assert { true }`, `42`,
		`if (true) { 1 }`, `loop { break }`, `n = 2`, `n += 1`,
	} {
		for _, owner := range []string{"type", "scalar"} {
			for _, static := range []bool{false, true} {
				for _, local := range []bool{false, true} {
					name := fmt.Sprintf("%s/static=%t/local=%t/%s", owner, static, local, body)
					t.Run(name, func(t *testing.T) {
						members := "n: Int! = 0; " + body
						want := "type bodies may only contain declarations"
						if owner == "scalar" {
							members = body
							want = "scalar bodies may only declare methods"
						}
						if static {
							members = "self { n: Int! = 0; " + body + " }"
							want = "self { } bodies may only contain declarations"
						}
						src := owner + " T { " + members + " }"
						if local {
							src = "unused: Int! { " + src + "; 0 }"
						}
						require.Contains(t, inferWithEffects(t, src), want)
					})
				}
			}
		}
	}
}

func TestLocalScalarBodyValidation(t *testing.T) {
	runEffectCases(t, []effectCase{
		{"stored field", `unused: Int! { scalar S { n: Int! = 1 }; 0 }`, "must be a method"},
		{"nested instance type", `unused: Int! { scalar S { type T {} }; 0 }`, "scalar bodies may only declare methods"},
		{"nested self", `unused: Int! { scalar S { self { self {} } }; 0 }`, "self { } cannot be nested"},
		{"static new", `unused: Int! { scalar S { self { new { self } } }; 0 }`, "new() cannot appear inside a self { } block"},
		{"local constructor and statics", `run: String! {
			scalar S {
				new(s: String!) { s.toUpper }
				label: String! { toString(self) }
				self { type Nested { n: Int! = 2 }; count: Int! = 1 }
			}
			S("ok").label
		}`, ""},
	})
}

func TestTypeDeclarationActions(t *testing.T) {
	const src = `
unused: Int! {
  type Dormant {
    self { let initialized: Int! = { print("unused"); 0 } }
    value: Int! = { print("unused default"); 0 }
  }
  0
}
type Active {
  value: Int! = { print("default"); 1 }
  new() { print("new"); self }
  self {
    constant: Int! = 2
    make: Active! { Active() }
  }
}
print("start")
Active()
Active.make
print(Active.constant)
`
	parsed, err := ParseWithRecovery("type_actions", []byte(src))
	require.NoError(t, err)
	file := parsed.(*FileBlock)
	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferTopLevel(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err)
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	_, err = EvaluateFormsWithPhases(ctx, file.Forms, valueScope)
	require.NoError(t, err)
	require.Equal(t, "start\ndefault\nnew\ndefault\nnew\n2\n", out.String())
}
