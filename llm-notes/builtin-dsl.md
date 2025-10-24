# Builtin Function and Method DSL

This document describes how to add new builtin functions and methods to Dang using the declarative DSL.

## Overview

Prior to this DSL, adding a single builtin method like `String.split` required modifications across 6+ files. Now, builtins are defined in a single location (`pkg/dang/stdlib.go`) using a fluent, type-safe API.

## Adding a Standalone Function

Standalone functions are defined using the `Builtin()` builder:

```go
Builtin("functionName").
    Doc("description of what the function does").
    Params("paramName", paramType, "param2", type2).
    Returns(returnType).
    Impl(func(ctx context.Context, args Args) (Value, error) {
        // Implementation
    })
```

### Example: print function

```go
Builtin("print").
    Doc("prints a value to stdout").
    Params("value", TypeVar('a')).
    Returns(TypeVar('n')).
    Impl(func(ctx context.Context, args Args) (Value, error) {
        val, _ := args.Get("value")
        writer := ioctx.StdoutFromContext(ctx)
        fmt.Fprintln(writer, val.String())
        return NullValue{}, nil
    })
```

## Adding a Method to a Type

Methods are bound to receiver types using the `Method()` builder:

```go
Method(ReceiverType, "methodName").
    Doc("description of what the method does").
    Params("paramName", paramType).
    Returns(returnType).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        // Implementation - self is the receiver
    })
```

### Example: String.toUpper method

```go
Method(StringType, "toUpper").
    Doc("converts a string to uppercase").
    Returns(NonNull(StringType)).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        str := self.(StringValue).Val
        return StringValue{Val: strings.ToUpper(str)}, nil
    })
```

### Example: String.split method with optional parameters

```go
Method(StringType, "split").
    Doc("splits a string by separator").
    Params(
        "separator", NonNull(StringType),
        "limit", IntType, IntValue{Val: 0},  // default value
    ).
    Returns(NonNull(ListOf(NonNull(StringType)))).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        str := self.(StringValue).Val
        sep := args.GetString("separator")
        limit := args.GetInt("limit")
        // ... implementation
    })
```

## Type Helpers

The DSL provides helper functions for common type patterns:

- `TypeVar(rune)` - creates a type variable (e.g., `TypeVar('a')`)
- `NonNull(Type)` - wraps a type as non-nullable
- `ListOf(Type)` - creates a list type
- `Optional(Type, defaultValue)` - creates a nullable parameter with default (unused currently)

### Available Receiver Types

- `StringType` - for String methods
- `IntType` - for Int methods
- `BooleanType` - for Boolean methods

## Args Helper

The `Args` type provides type-safe access to function arguments:

- `args.Get(name)` - returns `(Value, bool)`
- `args.GetString(name)` - returns `string` (empty string if missing)
- `args.GetInt(name)` - returns `int` (0 if missing)
- `args.GetBool(name)` - returns `bool` (false if missing)
- `args.GetList(name)` - returns `[]Value` (nil if missing)
- `args.Require(name)` - panics if argument not found

## Optional Parameters with Defaults

To define optional parameters with default values, include the default after the type:

```go
Params(
    "required", NonNull(StringType),
    "optional", IntType, IntValue{Val: 42},  // defaults to 42
)
```

When the parameter is omitted by the caller, the default value is automatically supplied.

## Best Practices

1. **Always provide documentation** - Use `.Doc()` to describe what the function/method does
2. **Use type-safe accessors** - Prefer `args.GetString()` over raw type assertions
3. **Handle errors gracefully** - Return descriptive errors wrapped with context
4. **Follow naming conventions** - Use camelCase for function names
5. **Keep implementations focused** - Each builtin should do one thing well

## Adding New Builtins

To add a new builtin:

1. Open `pkg/dang/stdlib.go`
2. Add your definition inside the `registerStdlib()` function
3. Run tests to ensure it works: `dagger call test --filter="your_test"`

That's it! The DSL handles:
- Type registration in the Prelude
- Implementation registration in the eval environment
- Method dispatch for receiver types
- Default value application

## Example: Complete Workflow

Let's add a new `String.toLower()` method:

```go
// In pkg/dang/stdlib.go, inside registerStdlib():
Method(StringType, "toLower").
    Doc("converts a string to lowercase").
    Returns(NonNull(StringType)).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        str := self.(StringValue).Val
        return StringValue{Val: strings.ToLower(str)}, nil
    })
```

That's the entire implementation! No need to touch any other files.
