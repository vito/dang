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

	// A methods-only scalar may be materialized by an earlier module type's
	// static initializer. Retain that evaluation's environment until the
	// declaration publishes its record normally; never run it early or wait.
	deferredScope ValueScope
}

// lookupTypeRuntime resolves a declaration's environment, never a same-named
// type. Try the lexical scope before the imported module: a callback can execute
// in its declaring scope while that module's valueScope is still initializing.
// The second result is the environment searched, for deferred materialization.
func lookupTypeRuntime(ctx context.Context, scope ValueScope, mod *Type) (*TypeRuntime, ValueScope, error) {
	mod = canonicalModule(mod)
	if scope != nil {
		if runtime, ok := scope.LookupTypeRuntime(mod); ok {
			return runtime, scope, nil
		}
	}
	if home := mod.homeModule; mod.localFrame == nil && home != nil && home.dang != nil && home.dang != evaluatingModule(ctx) {
		values, err := home.dang.valueScope(ctx)
		if err != nil {
			return nil, nil, err
		}
		scope = values
		if runtime, ok := scope.LookupTypeRuntime(mod); ok {
			return runtime, scope, nil
		}
	}
	return nil, scope, nil
}

func unavailableScalarRuntime(mod *Type) error {
	return fmt.Errorf("runtime environment for scalar %s is unavailable; materialize it in its declaring scope or use its captured constructor", mod.Name())
}

func typeRuntimeFor(ctx context.Context, scope ValueScope, mod *Type) (*TypeRuntime, error) {
	runtime, _, err := lookupTypeRuntime(ctx, scope, mod)
	if err == nil && runtime == nil && mod.scalarNeedsRuntime {
		err = unavailableScalarRuntime(mod)
	}
	return runtime, err
}

func scalarRuntimeForMaterialization(ctx context.Context, scope ValueScope, mod *Type) (*TypeRuntime, error) {
	runtime, declaring, err := lookupTypeRuntime(ctx, scope, mod)
	if err != nil || runtime != nil || !mod.scalarNeedsRuntime {
		return runtime, err
	}
	if mod.localFrame == nil && !mod.scalarHasHook && declaring != nil {
		return &TypeRuntime{deferredScope: declaring}, nil
	}
	// In particular, new() must run now: validation and normalization cannot
	// be postponed, and a missing local environment is never guessed.
	return nil, unavailableScalarRuntime(mod)
}

func (s ScalarValue) methodRuntime(ctx context.Context, scope ValueScope, mod *Type) (*TypeRuntime, error) {
	if s.Runtime == nil {
		return typeRuntimeFor(ctx, scope, mod)
	}
	if s.Runtime.deferredScope != nil {
		// Only the captured scope may complete this value's environment. Do not
		// ask the caller (or enter an imported module's once) while initializing.
		if runtime, ok := s.Runtime.deferredScope.LookupTypeRuntime(mod); ok {
			return runtime, nil
		}
		return nil, fmt.Errorf("scalar %s is not initialized yet", mod.Name())
	}
	return s.Runtime, nil
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
