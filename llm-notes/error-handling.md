# Error Handling: try/catch and raise

Dang supports user-level error handling via `try`/`catch` expressions and `raise` statements.

## Syntax

### try/catch

`try`/`catch` is a single expression. The `catch` block uses the same clause syntax as `case` — type patterns for discrimination, or a bare name for catch-all:

```dang
# catch-all: bind the error and handle it
pub result = try {
  user(id: "999").name
} catch {
  err => "unknown: " + err.message
}

# type patterns: discriminate by error type
pub result = try {
  doSomething()
} catch {
  v: ValidationError => "invalid " + v.field
  n: NotFoundError   => "missing " + n.resource
  err                => "other: " + err.message
}
```

Both the `try` body and every `catch` clause must return the same type.

### raise

`raise` throws an error. It accepts a `String!` or any type implementing `Error`:

```dang
raise "something went wrong"
raise Error(message: "not found")
raise ValidationError(message: "too short", field: "name")
```

Bare strings are sugar for `Error(message: "...")`. Errors propagate up the call stack until caught by a `try`/`catch` — uncaught errors crash the program with a source-highlighted error pointing at the `raise` statement.

## The Error Interface

`Error` is a built-in interface with one field:

| Field | Type |
|---|---|
| `message` | `String!` |

Custom error types implement it:

```dang
type NotFoundError implements Error {
  pub message: String!
  pub resource: String!
}

type ValidationError implements Error {
  pub message: String!
  pub field: String!
}
```

The `Error()` constructor creates a plain Error value:

```dang
raise Error(message: "not found")
```

For simple cases, `raise "msg"` is equivalent.

## Catch Clauses

Catch clauses use the same syntax as `case`:

- **Type pattern**: `v: ValidationError => expr` — matches if the error is the named type, binds it
- **Catch-all**: `err => expr` — always matches, binds the error as `Error`

If no clause matches, the error is re-raised.

```dang
try {
  riskyOperation()
} catch {
  v: ValidationError => "field " + v.field + ": " + v.message
  n: NotFoundError   => "missing: " + n.resource
  err                => "unexpected: " + err.message
}
```

## Runtime Errors

`try`/`catch` catches both user-level `raise` errors and runtime errors (division by zero, null access, GraphQL failures, etc.). Runtime errors are wrapped into an Error value automatically.

```dang
pub safeDivide(a: Int!, b: Int!): Int! {
  try { a / b } catch { err => 0 }
}
```

## Re-raising

Use `raise` inside a catch clause to re-throw:

```dang
try { ... } catch {
  v: ValidationError => raise v  # let it propagate
  err => "handled"
}
```

## Implementation

### Grammar (`pkg/dang/dang.peg`)

```peg
TryCatch <- TryToken _ body:Block _ CatchToken _ '{' _ CatchClause* _ '}'
CatchClause <- binding:Symbol ':' typeName:Symbol '=>' expr:Form   # type pattern
             / binding:Symbol '=>' expr:Form                        # catch-all
Raise <- RaiseToken _ value:Form
```

Catch clauses reuse `CaseClause` AST nodes.

### AST (`pkg/dang/ast_errors.go`)

- **TryCatch**: `TryBody *Block`, `Clauses []*CaseClause`, `Loc *SourceLocation`
- **Raise**: `Value Node`, `Loc *SourceLocation`
- **ErrorValue**: Runtime value with `Message string`, `Original Value`
- **RaisedError**: Go `error` wrapper carrying `ErrorValue` + `*SourceLocation`

### Type Inference

- **TryCatch.Infer**: Infers body type, validates each catch clause (type patterns must implement Error), unifies all clause return types with the body type.
- **Raise.Infer**: Validates the value is `String!` or implements `Error`. Returns a fresh type variable (bottom type).

### Evaluation

- **Raise.Eval**: Creates a `RaisedError` wrapping an `ErrorValue`. For custom types (`*ModuleValue`), extracts `message` and stores the original value.
- **TryCatch.Eval**: Evaluates the body. On error, dispatches through catch clauses using `matchesType` (same as `case`). If no clause matches, re-raises.
- **RaisedError propagation**: `WithEvalErrorHandling` and `EvalNodeWithContext` pass `RaisedError` through without wrapping.

### Uncaught Errors

Uncaught `RaisedError` values produce a `SourceError` pointing at the `raise` statement.

## Related Files

- `pkg/dang/dang.peg` — Grammar for `try`, `catch`, `raise`, `CatchClause`
- `pkg/dang/ast_errors.go` — `TryCatch`, `Raise`, `ErrorValue`, `RaisedError`
- `pkg/dang/ast_patterns.go` — `CaseClause`, `Case.inferTypePatternClause` (reused by catch)
- `pkg/dang/ast_literals.go` — `ErrorType` definition (InterfaceKind)
- `pkg/dang/env.go` — `Error` interface registered in Prelude
- `pkg/dang/stdlib.go` — `Error()` constructor builtin
- `pkg/dang/errors.go` — `RaisedError` pass-through in `WithEvalErrorHandling`
- `pkg/dang/eval.go` — `RaisedError` pass-through in `EvalNodeWithContext`; uncaught error formatting
- `pkg/dang/format.go` — `formatTryCatch`, `formatRaise`
- `tests/test_try_catch_*.dang` — Language tests
- `tests/errors/raise_*.dang`, `tests/errors/try_catch_*.dang` — Error tests
