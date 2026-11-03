package dang

import (
	"bytes"
	"context"
	"errors"
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

func TestEvalTopLevelShadowing(t *testing.T) {
	ctx := context.Background()
	types, scope := BuildScopesFromImports("", nil)
	enter := func(src string, env hm.Env, values ValueScope) Value {
		t.Helper()
		parsed, err := ParseWithRecovery("entry", []byte(src))
		require.NoError(t, err)
		forms := parsed.(*FileBlock).Forms
		_, err = InferTopLevel(ctx, forms, env, hm.NewSimpleFresher())
		require.NoError(t, err)
		v, err := EvalTopLevel(ctx, forms, values, nil)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, `"42"`, Repr(enter("let toString = toString(42); toString", types, scope)))
	enter("let x = 1", types, scope)
	child := scope.Derive(true)
	require.Equal(t, "2", Repr(enter("let x = x + 1; x", types.Clone(), child)))
	require.Equal(t, "1", Repr(enter("x", types, scope)))
}

func TestEvalTopLevelResults(t *testing.T) {
	ctx := context.Background()
	types, scope := BuildScopesFromImports("", nil)
	parsed, err := ParseWithRecovery("entry", []byte(`
		print("statement")
		let x = { print("initializer"); 1 }
		x = 2
		x
	`))
	require.NoError(t, err)
	forms := parsed.(*FileBlock).Forms
	_, err = InferTopLevel(ctx, forms, types, hm.NewSimpleFresher())
	require.NoError(t, err)
	var out bytes.Buffer
	ctx = ioctx.StdoutToContext(ctx, &out)
	var nodes []Node
	var values []string
	last, err := EvalTopLevel(ctx, forms, scope, func(node Node, value Value) error {
		nodes = append(nodes, node)
		values = append(values, Repr(value))
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, forms, nodes)
	require.Equal(t, "1", values[1], "declaration reports its original value, not a second evaluation")
	require.Equal(t, "2", Repr(last))
	require.Equal(t, "initializer\nstatement\n", out.String())

	last, err = EvalTopLevel(ctx, nil, scope, func(Node, Value) error {
		t.Fatal("empty entry called yield")
		return nil
	})
	require.NoError(t, err)
	require.Nil(t, last)
}

type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	w.cancel()
	return w.Buffer.Write(p)
}

func TestEvalTopLevelInitializationCancelled(t *testing.T) {
	for _, src := range []string{
		`type T { self { print("cancel"); print("unreached") } }; 1`,
		`let x = { print("cancel"); 1 }; let y = { print("unreached"); 2 }; 1`,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		types, scope := BuildScopesFromImports("", nil)
		parsed, err := ParseWithRecovery("entry", []byte(src))
		require.NoError(t, err)
		forms := parsed.(*FileBlock).Forms
		_, err = InferTopLevel(ctx, forms, types, hm.NewSimpleFresher())
		require.NoError(t, err)
		out := &cancelOnWrite{cancel: cancel}
		ctx = ioctx.StdoutToContext(ctx, out)
		last, err := EvalTopLevel(ctx, forms, scope, func(Node, Value) error {
			t.Fatal("initialization was cancelled: no results should be reported")
			return nil
		})
		cancel()
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, last)
		require.Equal(t, "cancel\n", out.String())
	}
}

func TestEvalTopLevelStops(t *testing.T) {
	for _, mode := range []string{"before", "during", "yield error", "statement error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			types, scope := BuildScopesFromImports("", nil)
			src := `1; print("unreached")`
			if mode == "statement error" {
				src = `1; { print("before error"); raise "stop" }; print("unreached")`
			}
			parsed, err := ParseWithRecovery("entry", []byte(src))
			require.NoError(t, err)
			forms := parsed.(*FileBlock).Forms
			_, err = InferTopLevel(ctx, forms, types, hm.NewSimpleFresher())
			require.NoError(t, err)
			var out bytes.Buffer
			ctx = ioctx.StdoutToContext(ctx, &out)
			if mode == "before" {
				cancel()
			}
			stop := errors.New("yield stopped")
			calls := 0
			last, err := EvalTopLevel(ctx, forms, scope, func(Node, Value) error {
				calls++
				if mode == "during" {
					cancel()
				}
				if mode == "yield error" {
					return stop
				}
				return nil
			})
			require.Nil(t, last)
			switch mode {
			case "before", "during":
				require.ErrorIs(t, err, context.Canceled)
			case "yield error":
				require.ErrorIs(t, err, stop)
			case "statement error":
				require.ErrorContains(t, err, "stop")
			}
			if mode == "before" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			if mode == "statement error" {
				require.Equal(t, "before error\n", out.String())
			} else {
				require.Empty(t, out.String())
			}
		})
	}
}
