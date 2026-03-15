---
name: add-builtin
description: Add a builtin method or function to the Dang standard library. Use when implementing new methods on types like String, Int, List, Boolean, Float, or adding standalone builtin functions.
---

# Adding Builtins

All builtins are defined in `pkg/dang/stdlib.go` inside `registerStdlib()` using a fluent DSL. No other files need modification — the DSL handles type registration, eval registration, and method dispatch automatically.

## DSL Reference

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

For standalone functions, use `Builtin("name")` instead of `Method(Type, "name")`. The rest of the DSL is the same (no `self` argument is used).

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

## Zero-Parameter Methods

Methods with no `Params()` are auto-callable — they're invoked on field access without parentheses (e.g., `"hello".length` not `"hello".length()`).

## Complete Examples

### Zero-parameter method (field-style access)

In `pkg/dang/stdlib.go`:
```go
	// String.length method: length -> Int!
	Method(StringType, "length").
		Doc("returns the number of characters in the string").
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			return ToValue(len([]rune(str)))
		})
```

Test at `tests/test_string_length.dang`:
```dang
# Test String.length method

pub empty_length = "".length
assert { empty_length == 0 }

pub hello_length = "hello".length
assert { hello_length == 5 }

print("String.length tests passed!")
```

### Method with parameters

In `pkg/dang/stdlib.go`:
```go
	// String.trimPrefix method: trimPrefix(prefix: String!) -> String!
	Method(StringType, "trimPrefix").
		Doc("removes the specified prefix from the string if present").
		Params("prefix", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			str := self.(StringValue).Val
			prefix := args.GetString("prefix")
			return ToValue(strings.TrimPrefix(str, prefix))
		})
```

Test at `tests/test_string_trimprefix.dang`:
```dang
# Test String.trimPrefix method

pub trimmed = "/workspace/file.txt".trimPrefix("/workspace")
assert { trimmed == "/file.txt" }

pub no_match = "hello".trimPrefix("goodbye")
assert { no_match == "hello" }

print("String.trimPrefix tests passed!")
```

## Testing

Always create a test at `tests/test_<feature>.dang` and run `test:language` to validate. Test conventions:
- Use `pub` for top-level bindings
- End with a `print("Description tests passed!")` line
- Test edge cases (empty strings, zero values, boundary conditions)
