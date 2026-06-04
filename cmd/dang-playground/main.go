//go:build js && wasm

// Command dang-playground is a WebAssembly module that evaluates Dang source
// entirely client-side in the browser. It backs the interactive code snippets
// on the documentation site.
//
// It exposes a single global function, dangEval(source), which parses,
// type-checks, and evaluates a Dang program with the standard library in
// scope (no GraphQL imports), returning a result object to JavaScript.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -o docs/js/dang.wasm ./cmd/dang-playground
package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
)

func main() {
	js.Global().Set("dangEval", js.FuncOf(dangEval))
	// dangReady lets the page know the module has finished initializing.
	js.Global().Set("dangReady", js.ValueOf(true))
	if cb := js.Global().Get("onDangReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	// Block forever so the exported function stays callable.
	select {}
}

// result builds the JS object returned to the page.
//
//	{ ok: bool, value: string, output: string, error: string, stage: string }
//
// stage is "" on success, or "parse" | "type" | "eval" identifying which
// phase failed.
func result(value, output, errMsg, stage string) any {
	return map[string]any{
		"ok":     errMsg == "",
		"value":  value,
		"output": output,
		"error":  errMsg,
		"stage":  stage,
	}
}

// dangEval is the JS-facing entry point: dangEval(source) -> result object.
func dangEval(_ js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return result("", "", "dangEval expects a source string", "parse")
	}
	source := args[0].String()

	// Parse.
	parsed, err := dang.ParseWithRecovery("playground", []byte(source))
	if err != nil {
		return result("", "", err.Error(), "parse")
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return result("", "", "unexpected parse result", "parse")
	}
	forms := file.Forms

	// Fresh standard-library scopes per run, so re-running a snippet is
	// idempotent and never leaks state between evaluations.
	typeScope, valueScope := dang.BuildScopesFromImports("", nil)

	// Type-check.
	fresh := hm.NewSimpleFresher()
	if _, err := dang.InferFormsWithPhases(context.Background(), forms, typeScope, fresh); err != nil {
		return result("", "", err.Error(), "type")
	}

	// Evaluate, capturing anything written to stdout/stderr (e.g. log()).
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)

	var last dang.Value
	for _, node := range forms {
		val, err := dang.EvalNode(ctx, valueScope, node)
		if err != nil {
			return result("", strings.TrimRight(out.String(), "\n"), err.Error(), "eval")
		}
		last = val
	}

	value := ""
	if last != nil {
		value = fmt.Sprint(last.String())
	}
	return result(value, strings.TrimRight(out.String(), "\n"), "", "")
}
