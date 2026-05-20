---
name: builtin-dsl
description: How to add or modify builtin functions and methods in `pkg/dang/stdlib.go`. Use when extending Dang's standard library.
---

# Builtin Function/Method DSL

All builtins are defined in a single place: the `registerStdlib()` function in
`pkg/dang/stdlib.go`. The DSL handles type registration, eval-env registration,
method dispatch, and default-value application — you only edit one file.

## Design principle: prefer methods over global functions

Avoid polluting the global namespace. Methods on types compose better with
autocomplete and chaining.

- `"hello".toUpper()` not `toUpper("hello")`
- `users.length` not `len(users)`
- `"a,b".split(",")` not `split("a,b", ",")`

Only add a global function when it doesn't naturally belong to one type
(`print`, `assert`).

## Adding a global function

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

## Adding a method

```go
Method(StringType, "toUpper").
    Doc("converts a string to uppercase").
    Returns(NonNull(StringType)).
    Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
        str := self.(StringValue).Val
        return ToValue(strings.ToUpper(str))
    })
```

Receiver types currently available: `StringType`, `IntType`, `BooleanType`.

## Optional parameters with defaults

Include the default value after the type in `Params()`:

```go
Params(
    "separator", NonNull(StringType),
    "limit",     IntType, IntValue{Val: 0},  // optional, default 0
)
```

## Type helpers

- `TypeVar(rune)` — type variable, e.g. `TypeVar('a')`
- `NonNull(t)` — non-null wrapper
- `ListOf(t)` — list type

## Args accessors

`args.Get(name) (Value, bool)`, `args.GetString`, `args.GetInt`, `args.GetBool`,
`args.GetList`, `args.Require` (panics if missing).

## `ToValue` — the single source of truth for Go→Dang

Use `ToValue(any) (Value, error)` to convert Go values. It's shared between
`stdlib.go`, `eval.go` (GraphQL result conversion), and `ast_expressions.go`
(GraphQL field values). If you need a new Go→Dang conversion, extend
`ToValue` rather than open-coding it.

Supported: `nil`, any `Value`, `string`, all int kinds (float truncates),
`bool`, `[]string`, `[]int`, `[]bool`, `[]any` (element type inferred).

## Workflow

1. Edit `pkg/dang/stdlib.go` inside `registerStdlib()`.
2. Run tests: `go test ./tests/ -run "TestDang/TestLanguage/your_test" -v`.
