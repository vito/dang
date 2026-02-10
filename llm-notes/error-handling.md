# Error Handling: try/catch and raise

Dang supports user-level error handling via `try`/`catch` expressions and `raise` statements.

## Syntax

### try/catch

`try`/`catch` is a single expression. The `catch` block uses block-arg syntax (`{ err => ... }`):

```dang
pub result = try {
  user(id: "999").name
} catch { err =>
  "unknown: " + err.message
}
```

Both the `try` body and the `catch` handler must return the same type.

### raise

`raise` throws an error. It accepts a `String!` or an `Error!`:

```dang
raise "something went wrong"
raise Error(message: "not found")
```

Bare strings are sugar for `Error(message: "...")`. Errors propagate up the call stack until caught by a `try`/`catch` — uncaught errors crash the program with a source-highlighted error pointing at the `raise` statement.

## The Error Interface

`Error` is a built-in interface with one field:

| Field | Type |
|---|---|
| `message` | `String!` |

Access the error message on the catch parameter:

```dang
try { ... } catch { err =>
  err.message     # String!
}
```

The `Error()` constructor creates an Error value:

```dang
raise Error(message: "not found")
```

For simple cases, `raise "msg"` is equivalent.

## Runtime Errors

`try`/`catch` catches both user-level `raise` errors and runtime errors (division by zero, null access, GraphQL failures, etc.). Runtime errors are wrapped into `ErrorValue` automatically, with the error message available via `err.message`.

```dang
pub safeDivide(a: Int!, b: Int!): Int! {
  try { a / b } catch { err => 0 }
}
```

## Re-raising

Pass an `Error` value to `raise` to re-throw:

```dang
try { ... } catch { err =>
  if (err.message == "expected") {
    "handled"
  } else {
    raise err  # re-raise to outer handler
  }
}
```

## Implementation

### Grammar (`pkg/dang/dang.peg`)

```peg
TryCatch <- TryToken _ body:Block _ CatchToken _ handler:BlockArg
Raise <- RaiseToken _ value:Form
```

`TryCatch` is a `Form` (expression-level), parsed before `Conditional` and other forms. `Raise` is also a `Form`.

### AST (`pkg/dang/ast_errors.go`)

- **TryCatch**: `TryBody *Block`, `Handler *BlockArg`, `Loc *SourceLocation`
- **Raise**: `Value Node`, `Loc *SourceLocation`
- **ErrorValue**: Runtime value with `Message string`
- **RaisedError**: Go `error` wrapper that carries an `ErrorValue` and `*SourceLocation` for propagation

### Type System

- `ErrorType` is an `InterfaceKind` module with one field: `message: String!`
- **TryCatch.Infer**: Infers body type, sets handler's expected param to `Error!`, unifies body and handler return types.
- **Raise.Infer**: Validates the value is `String!` or `Error!`. Returns a fresh type variable (bottom type — `raise` never completes normally).

### Evaluation

- **Raise.Eval**: Creates a `RaisedError` wrapping an `ErrorValue` and the raise location, returns it as a Go error.
- **TryCatch.Eval**: Evaluates the body. On error, extracts or wraps the `ErrorValue`, binds it to the handler parameter, and evaluates the handler block.
- **RaisedError propagation**: `WithEvalErrorHandling` and `EvalNodeWithContext` pass `RaisedError` through without wrapping, so `TryCatch` can intercept it cleanly.

### Error Constructor (`pkg/dang/stdlib.go`)

```go
Builtin("Error").
    Params("message", NonNull(StringType)).
    Returns(NonNull(ErrorType))
```

### Uncaught Errors

Uncaught `RaisedError` values that reach `RunFile` produce a `SourceError` pointing at the `raise` statement.

## Related Files

- `pkg/dang/dang.peg` — Grammar for `try`, `catch`, `raise` tokens
- `pkg/dang/ast_errors.go` — `TryCatch`, `Raise`, `ErrorValue`, `RaisedError`
- `pkg/dang/ast_literals.go` — `ErrorType` definition (InterfaceKind)
- `pkg/dang/env.go` — `Error` interface registered in Prelude
- `pkg/dang/stdlib.go` — `Error()` constructor builtin
- `pkg/dang/errors.go` — `RaisedError` pass-through in `WithEvalErrorHandling`
- `pkg/dang/eval.go` — `RaisedError` pass-through in `EvalNodeWithContext`; uncaught error formatting
- `pkg/dang/format.go` — `formatTryCatch`, `formatRaise`
- `tests/test_try_catch_*.dang` — Language tests
- `tests/errors/raise_*.dang`, `tests/errors/try_catch_*.dang` — Error tests
