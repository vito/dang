package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// `&x` must not flip AutoCall on the shared AST while it evaluates: the same
// node can be evaluated by several `{{ }}` tasks at once, so a temporary write
// races with their reads. The lazy initializer runs mid-evaluation, inside the
// window the old flip-and-restore left the flag cleared.
func TestFunctionRefEvalLeavesASTUntouched(t *testing.T) {
	ctx := context.Background()

	sym := &Symbol{Name: "f", AutoCall: true}
	ref := &FunctionRef{Expr: sym}

	fn := BuiltinFunction{
		Name:   "f",
		FnType: hm.NewFnType(NewRecordType(""), NonNull(IntType)),
		CallFn: func(context.Context, ValueScope, map[string]Value) (Value, error) {
			return IntValue{Val: 1}, nil
		},
	}

	scope := NewObject(NewType("scope", ObjectKind))
	var during bool
	scope.BindLazy("f", func(context.Context) (Value, error) {
		during = sym.AutoCall
		return fn, nil
	}, PrivateVisibility)

	val, err := EvalNode(ctx, scope, ref)
	require.NoError(t, err)
	require.IsType(t, BuiltinFunction{}, val, "&f yields the function, uncalled")
	require.True(t, during, "AutoCall was flipped on the shared node during evaluation")
	require.True(t, sym.AutoCall)
}
