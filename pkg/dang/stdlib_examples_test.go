package dang

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// allBuiltins returns every registered builtin: free functions, methods on
// every receiver type, and static methods on every module.
func allBuiltins() []BuiltinDef {
	var defs []BuiltinDef
	ForEachFunction(func(d BuiltinDef) { defs = append(defs, d) })
	for _, recv := range MethodReceivers() {
		ForEachMethod(recv, func(d BuiltinDef) { defs = append(defs, d) })
	}
	for _, mod := range StaticModules() {
		ForEachStaticMethod(mod, func(d BuiltinDef) { defs = append(defs, d) })
	}
	return defs
}

// builtinLabel is the qualified name used to identify a builtin in test output,
// e.g. "List.map", "Random.int", or a bare "print".
func builtinLabel(d BuiltinDef) string {
	switch {
	case d.IsStatic && d.HostModule != nil:
		return d.HostModule.Named + "." + d.Name
	case d.IsMethod && d.ReceiverType != nil:
		return d.ReceiverType.Named + "." + d.Name
	default:
		return d.Name
	}
}

// TestStdlibExamplesEvaluate runs every builtin's .Example(...) through the same
// parse → infer → eval path as the docs REPL, so a pre-seeded example can never
// drift out of sync with the implementation it documents.
func TestStdlibExamplesEvaluate(t *testing.T) {
	for _, d := range allBuiltins() {
		if d.Example == "" {
			continue
		}
		t.Run(builtinLabel(d), func(t *testing.T) {
			if err := evalExample(d.Example); err != nil {
				t.Fatalf("example %q failed: %v", d.Example, err)
			}
		})
	}
}

// TestStdlibBuiltinsHaveExamples enforces that every builtin documents a
// runnable example, so the stdlib reference renders a REPL for each entry.
func TestStdlibBuiltinsHaveExamples(t *testing.T) {
	for _, d := range allBuiltins() {
		if d.Example == "" {
			t.Errorf("builtin %q has no .Example(...)", builtinLabel(d))
		}
	}
}

// evalExample parses, type-checks, and evaluates a snippet against fresh core
// scopes (no GraphQL imports), mirroring the browser REPL's evalForms. It
// returns the first error from any stage, or nil if every form evaluates.
func evalExample(src string) error {
	parsed, err := ParseWithRecovery("example", []byte(src))
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	file, ok := parsed.(*FileBlock)
	if !ok {
		return fmt.Errorf("unexpected parse result %T", parsed)
	}

	typeScope, valueScope := BuildScopesFromImports("", nil)
	fresh := hm.NewSimpleFresher()
	if _, err := InferFormsWithPhases(context.Background(), file.Forms, typeScope, fresh); err != nil {
		return fmt.Errorf("type: %w", err)
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			return fmt.Errorf("eval: %w", err)
		}
	}
	return nil
}
