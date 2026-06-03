\use-plugin{dang}

# Functions {#functions}

> Meta: emphasize the four "feels weird at first" points: zero-arity has no parens; the body has no `return`; positional args can mix with named; `&fn` is for references. Each one separately surprises people.

## Declaration

```dang
pub add(a: Int!, b: Int!): Int! { a + b }
```

- name, parameter list, return type, body
- last expression is the result — no `return` keyword needed for the normal result
- `return expr` is available for *early* exit and unwinds through enclosing blocks/loops (verified: test_early_return.dang); also valid in `new(...)` constructors
- `return` outside any function/method/constructor errors: `return outside of function` (verified: errors/return_outside_function.dang)
- multi-statement bodies separate forms with newlines or `,` (there is no `;` separator — verified: grammar `Sep`)

## Zero-arity

```dang
pub motd: String! { "hello" }
```

- omit the parentheses; the function is a *field* with a function body
- callers also omit the parens: `motd`, not `motd()`

## Auto-calling

- a zero-arity function/method *invokes* on reference, like a property
- `&name` (see below) suppresses invocation
- the same rule applies to GraphQL fields with no required args

## Arguments

### Named

```dang
greet(name: "Alice")
```

### Positional

```dang
greet("Alice")
```

### Mixed

- positional args first, then named
- `add(10, b: 20)` ✓
- `add(a: 10, 20)` ✗ — error: `positional arguments must come before named arguments` (verified: ast_expressions.go; same rule on directive applications, errors/directive_positional_after_named.dang)

### Defaults

- declared on the parameter: `name: String! = "world"`
- a default may reference *earlier parameters* in the same list; the param shadows any outer binding of the same name (verified: test_default_arg_forward_ref.dang)
- in a free function a default may reference enclosing-scope names (verified: test_default_field_ref.dang)
- in a method a default may reference fields of the same type (verified: test_default_field_ref.dang)
- a nullable arg passed `null` falls back to its default; a nullable arg with no default stays `null` (verified: test_default_args.dang, test_nullable_args.dang)
- same default rules apply to `new(...)` constructor params (verified: test_default_arg_forward_ref.dang)

## Function references: `&fn`

- the `&` prefix operator (see [#operators]) yields the function itself without calling it
- `&greet` — captures a zero-arity function/method without auto-calling it; it stays live and re-reads its closure each call (verified: test_function_ref.dang)
- needed for assignment to a function-typed field, passing as an arg, etc.
- combined with `.method` selection: `&user.greet`
- a captured ref must still satisfy the target's block-parameter signature (verified: errors/function_ref_block_incompatible.dang)

## Nested functions

- functions declared inside method bodies can capture enclosing scope
- captured `self` works — nested function still acts as a method on the receiver

> Meta: link forward to [#blocks] — block arguments are the more common form of "pass code." Function refs are for the cases where you need a true callable to store or rebind.

## Docstrings on parameters

```dang
pub greet(
  """name of the person to greet"""
  name: String!
): String! { ... }
```
