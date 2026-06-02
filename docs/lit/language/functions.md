\use-plugin{dang}

# Functions {#functions}

> Meta: emphasize the four "feels weird at first" points: zero-arity has no parens; the body has no `return`; positional args can mix with named; `&fn` is for references. Each one separately surprises people.

## Declaration

```dang
pub add(a: Int!, b: Int!): Int! { a + b }
```

- name, parameter list, return type, body
- last expression is the result — no `return` keyword needed
- multi-statement bodies use newlines or `;`

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
- `add(a: 10, 20)` ✗

### Defaults

- declared on the parameter: `name: String! = "world"`
- can reference earlier parameters or enclosing-scope names

## Function references: `&fn`

- `&greet` — yields the function itself without calling it
- needed for assignment to a function-typed field, passing as an arg, etc.
- combined with `.method` selection: `&user.greet`

## Nested functions

- functions declared inside method bodies can capture enclosing scope
- captured `self` works — nested function still acts as a method on the receiver

> Meta: link forward to [blocks](./blocks.md) — block arguments are the more common form of "pass code." Function refs are for the cases where you need a true callable to store or rebind.

## Docstrings on parameters

```dang
pub greet(
  """name of the person to greet"""
  name: String!
): String! { ... }
```
