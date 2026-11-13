---
name: testing
description: How to run tests, add error tests, update golden files, report nice errors, and add pitfall diagnostics (hints for common mistakes) in Dang. Use when running or writing tests, or improving error messages.
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

## Editor Highlight Tests

Editor syntax highlighting tests live in `editors/` and run via the `TestEditors` suite:

```bash
go test ./editors/ -v
go test ./editors/ -run "TestEditors/TestNeovimHighlights" -update
```

- **TestNeovimHighlights**: Renders each case in `editors/highlights/corpus/*.txt` through Neovim with the tree-sitter parser and compares highlight spans.
- **TestZedHighlightQueryCompatibility**: Smoke-tests the Zed highlight queries against the corpus with the `tree-sitter` CLI.

These tests Skip unless the `editors/nvim` and `editors/zed` submodules are initialized (`git submodule update --init`).

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

### Failed declarations don't cascade

A declaration that fails to type-check binds its name to `poisonType`
(`assignFallbackType`, at the top level and in `Block.Infer`), unless it gives
the name a type itself: a function's declared signature
(`FunctionBase.declared`), a field's annotation (`FieldDecl.annotated`), or a
type's constructor, bound when it was hoisted.
Reading a poisoned name (`Symbol.Infer`, `Select.infer`) fails with a
`poisonedError`, which `InferenceErrors.Add` keeps as a failure without
reporting it: the expression reading it fails quietly, a declaration doing so
is poisoned in turn, and the original error is reported once. Code that
collects errors must go through `InferenceErrors` to get this; a new
collector that appends errors itself would report `"c" failed to type-check`
at every use. A REPL entry that fails restores the names it redefined and
takes back the ones it declared (`sessionEntry.rollback`), so no poisoned
name outlives it.

## Pitfalls

A pitfall is a common mistake Dang recognizes (`//` comments, `&&`, `for`
loops, truthiness, ...). Its diagnostic carries a one-line hint and a docs
link, shown by the CLI (`= hint:` / `= docs:` under the snippet),
`analysis.Diagnostic` (`Pitfall`, `Hint`, `Docs`; `Format` adds `hint:` /
`docs:` lines) and the LSP (code, code description, hint in the message).

The registry is `Pitfalls` in `pkg/dang/pitfalls.go`. Each `Pitfall` has an
ID, `Hint`, `Docs` (`docsURL(page, tag)`: a `{#tag}` heading on the docs page
tagged `{#page}`), and one way to be detected (or both):

- `syntax`: called at a parse failure with the source and the PEG parser's
  failure offset (both builds, before tree-sitter). It returns the message
  that replaces the parser's, and the span to report. `codeMask` tells code
  from comments and string text (interpolations count as code), so a `//` or
  `&&` inside a string never matches.
- `typeErr`: called by `NewInferError` with the inner error and node. Match on
  a typed error (e.g. `notBooleanErr`, `nameNotFoundError`), not message text;
  the message is kept and the hint added.

Detection must be precise: a hint on the wrong code misleads, which is worse
than none. To add one:

1. Add the `Pitfall` to `pitfalls.go` and to `Pitfalls`, with its detector. Tag
   the docs heading it links to if it has no `{#tag}` yet.
2. Add rows to `TestPitfalls` (`pkg/dang/analysis/pitfalls_test.go`): source
   in, rendered diagnostics out, plus negative rows — similar-looking code,
   valid or not, that must not match. `TestPitfallsAreDocumented` checks the
   docs link resolves to a heading in `docs/lit`.

A golden fixture under `tests/errors/` shows the CLI rendering, but one per
kind is enough; the table is where cases go.

## PEG Grammar

The parser is defined in `pkg/dang/dang.peg` (pigeon PEG parser). After editing:

```bash
go generate ./pkg/dang/
```

This regenerates `pkg/dang/dang.peg.go`.

## Formatter

The formatter lives in `pkg/dang/format.go`. When adding a new AST node type, update:

- `formatNode()` — add a case to format the node
- Any relevant classification helpers e.g. `.GetSourceLocation() for blank line insertion logic

## Names

A name may be spelled like a reserved word (`$rescue` is the identifier
`rescue`), so new syntax that holds a name must pick its rule by position:

| Position | Grammar | Formatter |
|---|---|---|
| Identifier: a declaration, a bare reference, a param, a binding (anywhere an expression could also start) | `Id` | `f.ident(name)` |
| Only a name can go: a named argument, an object or object-type key, a field in `{{ }}` | `Id / ReservedName` | `f.fieldName(name)` |
| After a dot | `FieldId` | `f.fieldName(name)` |

Never `f.write(name)` a name raw: `dang fmt` and `dang migrate` would print
`rescue` where it must be `$rescue`, and the output wouldn't parse.
`TestReservedWords` and `TestKeywordNamesRoundTrip` (`pkg/dang/version_test.go`)
exercise the common positions; extend them for a new one.
