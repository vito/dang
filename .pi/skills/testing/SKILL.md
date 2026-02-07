---
name: testing
description: How to run tests, add error tests, update golden files, and report nice errors in Dang. Use when running or writing tests.
---

# Testing & Error Reporting

## Running Tests

All language and error tests live in `tests/` and run via the `TestDang` suite:

```bash
# Run all tests
go test ./tests/ -v

# Run a specific test by name
go test ./tests/ -run "TestDang/TestLanguage/test_foo" -v
go test ./tests/ -run "TestDang/TestErrorMessages/some_error" -v
go test ./tests/ -run "TestDang/TestFormatLanguage/test_foo" -v
```

## Test Suites

The `DangSuite` (in `tests/integration_test.go`) has three sub-suites:

- **TestLanguage**: Runs each `tests/test_*.dang` file and expects it to succeed.
- **TestFormatLanguage**: Formats each `tests/test_*.dang` file, re-parses the formatted output, runs it, and checks it still produces the same result.
- **TestErrorMessages**: Runs each `tests/errors/*.dang` file, expects an error, and compares the output against `tests/testdata/<name>.golden`.

## Error Message Golden Files

Error golden files live in `tests/testdata/` and contain the exact error output including ANSI escape codes for colored/highlighted source annotations.

To update golden files after changing error messages:

```bash
go test ./tests/ -run "TestDang/TestErrorMessages" -update
```

## Adding a New Error Test

1. Create `tests/errors/my_error.dang` with code that should produce an error.
2. Run with `-update` to generate the golden file:
   ```bash
   go test ./tests/ -run "TestDang/TestErrorMessages/my_error" -update
   ```
3. Review `tests/testdata/my_error.golden` to confirm the error message is correct.

## Dagger SDK Tests

The Dagger SDK has its own integration test suite in `dagger-sdk/tests/` that exercises modules against a real Dagger engine via `dagger call`.

### Running SDK Tests

```bash
# Run all SDK tests
go test ./dagger-sdk/tests/ -v

# Run a specific test
go test ./dagger-sdk/tests/ -run "TestDaggerSDK/TestMismatch" -v
```

### Test Structure

- **Test suite**: `DaggerSDKSuite` in `dagger-sdk/tests/integration_test.go`
- **Test modules**: Each test has a Dagger module in `dagger-sdk/testdata/<module-name>/` containing:
  - `main.dang` — the module source
  - `dagger.json` — module config with `"sdk": "../.."` pointing to the SDK

### Adding a New SDK Test

1. Create `dagger-sdk/testdata/my-test/main.dang` with a Dagger module.
2. Create `dagger-sdk/testdata/my-test/dagger.json`:
   ```json
   {
     "name": "my-test",
     "engineVersion": "v0.19.11",
     "sdk": "../.."
   }
   ```
3. Add a test method on `DaggerSDKSuite` in `dagger-sdk/tests/integration_test.go`:
   ```go
   func (DaggerSDKSuite) TestMyTest(ctx context.Context, t *testctx.T) {
       t.Run("sub test", func(ctx context.Context, t *testctx.T) {
           out := requireDagger(ctx, t, "my-test", "some-function", "--arg", "value")
           require.Contains(t, out, "expected output")
       })
   }
   ```

### Helpers

- `runDagger(ctx, module, args...)` — runs `dagger -m <testdata/module> call <args>`, returns stdout, stderr, error
- `requireDagger(ctx, t, module, args...)` — same but fails the test on error, returns stdout

## Reporting Nice Errors

Dang has two error types for attaching source locations:

### InferError (type checking phase)

Use `NewInferError` in `Infer()` methods to attach source location to type errors. These get converted to `SourceError` with full source highlighting when displayed.

```go
return nil, NewInferError(fmt.Errorf("descriptive message"), node)
```

`WrapInferError` avoids double-wrapping if the error is already an `InferError`:

```go
return nil, WrapInferError(err, node)
```

### SourceError (display/eval phase)

`SourceError` holds the error, source location, and source text, and renders with highlighted code snippets:

```go
return nil, NewSourceError(fmt.Errorf("message"), location, sourceCode)
```

During eval, use the `EvalContext` helper:

```go
return nil, evalCtx.CreateSourceError(err, node)
```

### Conversion Flow

`InferError` → `ConvertInferError()` → `SourceError` (reads the source file and produces highlighted output). This happens automatically in `RunFile`.

### WithInferErrorHandling / WithEvalErrorHandling

Wrapper helpers that automatically attach source location to errors that don't already have one:

```go
func (n *MyNode) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
    return WithInferErrorHandling(n, func() (hm.Type, error) {
        // ... your logic here
    })
}
```

## PEG Grammar

The parser is defined in `pkg/dang/dang.peg` (pigeon PEG parser). After editing:

```bash
go generate ./pkg/dang/
```

This regenerates `pkg/dang/dang.peg.go`.

## Formatter

The formatter lives in `pkg/dang/format.go`. When adding a new AST node type, update:

- `formatNode()` — add a case to format the node
- `nodeLocation()` — return the node's `*SourceLocation`
- Any relevant classification helpers (e.g. `isFunctionDef`) for blank line insertion logic
