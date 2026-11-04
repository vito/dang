package dang

import (
	"context"
	"fmt"
)

// TypeRuntime is the closure of one evaluation of a type declaration. Inferred
// types are shared by recursive calls and prepared evaluations; their evaluated
// statics, scalar methods and hooks must not be. Records are immutable after
// publication. Scalar values and constructors retain their declaring record.
type TypeRuntime struct {
	statics       *Object
	scalarMethods *Object
	scalarHook    FunctionValue
	scalarHookArg string
	hasScalarHook bool
}

// typeRuntimeFor resolves a declaration's environment, never a same-named type.
// Try the lexical scope before the imported module: a callback can execute in
// its declaring scope while that module's valueScope is still initializing.
func typeRuntimeFor(ctx context.Context, scope ValueScope, mod *Type) (*TypeRuntime, error) {
	mod = canonicalModule(mod)
	if scope != nil {
		if runtime, ok := scope.LookupTypeRuntime(mod); ok {
			return runtime, nil
		}
	}
	if home := mod.homeModule; mod.localFrame == nil && home != nil && home.dang != nil && home.dang != evaluatingModule(ctx) {
		values, err := home.dang.valueScope(ctx)
		if err != nil {
			return nil, err
		}
		if runtime, ok := values.LookupTypeRuntime(mod); ok {
			return runtime, nil
		}
	}
	if mod.scalarNeedsRuntime {
		return nil, fmt.Errorf("runtime environment for scalar %s is unavailable; materialize it in its declaring scope or use its captured constructor", mod.Name())
	}
	return nil, nil
}

func staticsFor(ctx context.Context, scope ValueScope, mod *Type) (*Object, error) {
	runtime, err := typeRuntimeFor(ctx, scope, mod)
	if err != nil {
		return nil, err
	}
	if runtime != nil {
		return runtime.statics, nil
	}
	// Only Go-native builtins install statics on a type.
	return mod.Statics(), nil
}
