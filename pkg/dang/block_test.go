package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
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
