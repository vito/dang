---
name: add-builtin
description: Add a builtin method or function to the Dang standard library. Use when implementing new methods on types like String, Int, List, Boolean, Float, or adding standalone builtin functions.
---

# Adding Builtins

All builtins are defined in `pkg/dang/stdlib.go` inside `registerStdlib()` using a fluent DSL. No other files need modification — the DSL handles type registration, eval registration, and method dispatch automatically.

## Adding a Method on a Type

```go
Method(ReceiverType, "methodName").
    Doc("description").
    Params("paramName", NonNull(ParamType)).          // optional, repeat for more params
    Params("optParam", ParamType, DefaultValue).      // optional param with default
    Returns(NonNull(ReturnType)).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        // self.(StringValue).Val, self.(ListValue).Elements, etc.
        // args.GetString("name"), args.GetInt("name"), args.GetBool("name")
        return ToValue(result) // converts Go string/int/bool/[]string/etc. to Dang Value
    })
```

## Available Types

- `StringType`, `IntType`, `FloatType`, `BooleanType`, `IDType` — scalars
- `ListTypeModule` — for List methods
- `NonNull(t)` — non-nullable wrapper
- `ListOf(t)` — list type
- `TypeVar('a')` — type variable for polymorphism

## Value Extraction

| Dang Type | Go Extraction | Go Type |
|-----------|--------------|---------|
| String | `self.(StringValue).Val` | `string` |
| Int | `self.(IntValue).Val` | `int` |
| Float | `self.(FloatValue).Val` | `float64` |
| Boolean | `self.(BooleanValue).Val` | `bool` |
| List | `self.(ListValue).Elements` | `[]Value` |

## Adding a Standalone Function

```go
Builtin("functionName").
    Doc("description").
    Params("param", NonNull(ParamType)).
    Returns(NonNull(ReturnType)).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        return ToValue(result)
    })
```

## Zero-Parameter Methods

Methods with no `Params()` are auto-callable — they're invoked on field access without parentheses (e.g., `"hello".length` not `"hello".length()`).

## Testing

Always create a test at `tests/test_<feature>.dang` and run `test:language` to validate.
