---
name: add-custom-type
description: Add a new custom value type to Dang (like Semver or YAML). Use when adding a structured type with its own methods, equality, and comparison semantics.
---

# Adding a Custom Value Type

Custom value types are structured types (not backed by GraphQL) with their own methods, equality, and optionally comparison operators. Examples: `SemverValue`, `YAMLValue`.

## Files to Modify

| # | File | What to do |
|---|------|------------|
| 1 | `pkg/dang/stdlib_<name>.go` | **Create**: Module var, Value struct, `register<Name>()` with methods |
| 2 | `pkg/dang/stdlib.go` | Call `register<Name>()` from `registerStdlib()` |
| 3 | `pkg/dang/env.go` | Register in Prelude (`AddClass` + `Add`) and add to `registerBuiltinTypes()` receiver list |
| 4 | `pkg/dang/eval.go` | Add to receiver type list in runtime method registration |
| 5 | `pkg/dang/ast_expressions.go` | Add `case <Name>Value:` in `Select.Eval` for method dispatch |
| 6 | `pkg/dang/ast.go` | Add equality case in `valuesEqual` |
| 7 | `pkg/dang/ast_operators.go` | *(Optional)* Add comparison operator cases if type is orderable |

## Step 1: Create the type file (`pkg/dang/stdlib_<name>.go`)

```go
package dang

import (
    "context"
    "fmt"
    "github.com/vito/dang/pkg/hm"
)

// FooModule is the "Foo" namespace
var FooModule = NewModule("Foo", ObjectKind)

// FooValue represents a Foo instance
type FooValue struct {
    // fields...
}

var _ Value = FooValue{}

func (f FooValue) Type() hm.Type {
    return hm.NonNullType{Type: FooModule}
}

func (f FooValue) String() string {
    return fmt.Sprintf("...")
}

func (f FooValue) MarshalJSON() ([]byte, error) {
    return []byte(fmt.Sprintf("%q", f.String())), nil
}

func registerFoo() {
    FooModule.SetModuleDocString("description")

    // Static constructor: Foo.create(...) -> Foo!
    StaticMethod(FooModule, "create").
        Doc("creates a Foo").
        Params("input", NonNull(StringType)).
        Returns(NonNull(FooModule)).
        Impl(func(ctx context.Context, args Args) (Value, error) {
            input := args.GetString("input")
            return FooValue{/* ... */}, nil
        })

    // Instance method: Foo.bar -> String!
    Method(FooModule, "bar").
        Doc("returns bar").
        Returns(NonNull(StringType)).
        Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
            f := self.(FooValue)
            return StringValue{Val: f.Bar}, nil
        })
}
```

## Step 2: Call register function (`pkg/dang/stdlib.go`)

Add `registerFoo()` to `registerStdlib()`:

```go
func registerStdlib() {
    registerRandomAndUUID()
    registerYAML()
    registerSemver()
    registerFoo()       // <-- add here
    // ...
}
```

## Step 3: Register in Prelude (`pkg/dang/env.go`)

In the `init()` function, add both class and value registration:

```go
Prelude.AddClass("Foo", FooModule)
Prelude.Add("Foo", hm.NewScheme(nil, hm.NonNullType{Type: FooModule}))
```

In `registerBuiltinTypes()`, add `FooModule` to the receiver type slice:

```go
for _, receiverType := range []*Module{StringType, IntType, FloatType, BooleanType, ListTypeModule, YAMLModule, SemverModule, FooModule} {
```

## Step 4: Runtime method registration (`pkg/dang/eval.go`)

Add `FooModule` to the same receiver type slice (mirrors the one in `env.go`):

```go
for _, receiverType := range []*Module{StringType, IntType, FloatType, BooleanType, ListTypeModule, YAMLModule, SemverModule, FooModule} {
```

## Step 5: Method dispatch (`pkg/dang/ast_expressions.go`)

In `Select.Eval`, add a case in the type switch (before `default:`):

```go
case FooValue:
    // Handle methods on Foo values by looking them up in the evaluation environment
    methodKey := fmt.Sprintf("_foo_%s_builtin", d.Field.Name)
    if method, found := env.Get(methodKey); found {
        if builtinFn, ok := method.(BuiltinFunction); ok {
            return BoundBuiltinMethod{Method: builtinFn, Receiver: rec}, nil
        }
    }
    return nil, fmt.Errorf("Foo value does not have method %q", d.Field.Name)
```

The `_foo_` prefix is derived from the **lowercase module name**. It is generated automatically by the builtin DSL framework based on the receiver type's `Named` field.

## Step 6: Equality (`pkg/dang/ast.go`)

Add a case in `valuesEqual`:

```go
case FooValue:
    if r, ok := right.(FooValue); ok {
        return l == r  // or custom equality logic
    }
```

## Step 7 (Optional): Comparison operators (`pkg/dang/ast_operators.go`)

If the type is orderable, add cases in all four comparison operator switches (`<`, `>`, `<=`, `>=`). Implement a `CompareTo` method returning `-1`, `0`, or `1`:

```go
case FooValue:
    if rv, ok := rightVal.(FooValue); ok {
        return BoolValue{Val: lv.CompareTo(rv) < 0}, nil  // for <
    }
```

## Testing

Create `tests/test_foo.dang` and run `test:language`.
