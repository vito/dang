# Syntax, Literals, Operators, Grammar

## File layout

- A file is a sequence of forms separated by **newlines or commas** (interchangeable).
- Whitespace is significant only as a separator; indentation is conventional, not syntactic.
- There is no `;` separator. Multi-statement bodies use newlines or `,`.
- Declarations are **hoisted** and order-independent within a file/directory (forward references work).

## Comments

- `#` to end of line. Own-line or trailing both idiomatic.
- `//` is NOT a comment. `///` doc comments do NOT exist.
- Inside backtick templates, `#` is a literal character, not a comment.

## Identifiers

- lowercase (`foo_bar`, `userName`) — values and methods
- Capitalized (`User`, `String`) — types
- single lowercase letter (`a`, `b`) in type position — type variables
- `_` is a normal identifier character, not a discard/ignore pattern

## Reserved words

```
let type interface enum union scalar new implements
if else case for break continue return
try catch raise
import directive on
true false null
and or
self
```

## Docstrings

- A **triple-quoted** `"""..."""` string placed immediately before a declaration attaches as documentation.
- Works on: modules, types, fields, functions, function parameters, directives, directive args.
- This is the only docstring mechanism — there is no `///`.

```dang
"""
Greets the named user.
"""
greet(
  """name of the person to greet"""
  name: String!
): String! { `hi, ${name}` }
```

## Literals

### Numbers
- `Int!` — decimal, signed 64-bit. No hex/octal/binary.
- `Float!` — `3.14`, `1.5e10`, `2.5e-3`. A digit must precede and follow the `.`; exponent `e`/`E` with optional sign. A bare `1.` or `.5` is NOT a float.

### Booleans / Null
- `true`, `false`
- `null` — only assignable to nullable type positions.

### Strings (three flavors)
- **Double-quoted** `"hello"` — single-line, escapes: `\a \b \f \n \r \t \v \" \\`, `\ooo` (octal), `\xNN`, `\uNNNN`, `\UNNNNNNNN`; `\/` yields `/`.
- **Triple-quoted** `"""..."""` — multi-line, **raw** (no escape processing), dedents by minimum indent. A leading/trailing newline adjacent to the fences is stripped (`"""\nhello\n"""` == `"hello"`). Canonical docstring form.
- **Backtick templates** `` `hello ${name}!` `` — single-line, `${...}` interpolation. The ONLY escape is `\${` → literal `${`; every other backslash is literal (`` `\d+` `` stays `\d+`). A lone `$` not followed by `{` is literal. Interpolated values auto-stringify like `toString` (non-strings JSON-encode; `null` → `"null"`).
  - Multi-line: ```` ```...``` ```` — same minimum-indent dedent; fences grow (4+ backticks) to wrap shorter backtick blocks, and the close fence must match the open fence length. Optional language tag (` ```go ... ``` `) is parsed but does not affect the value.

### Lists
- `[1, 2, 3]`, `["a", "b"]`, `[]` (empty), nested `[[1, 2], [3]]`.
- Empty list needs a type hint to pin its element type: `[] :: [String!]!`. It can still be compared directly: `xs == []`.

### Records (object literals) — `{{ ... }}`
- `{{ key: value, other: value }}` — note the **double braces**. Always non-null.
- Same `{{ ... }}` syntax is also a record *type* annotation: `x :: {{foo: Int!, bar: Status!}}!`.
- Nestable. Serialized to JSON with keys **sorted alphabetically**, not declaration order.

## Operators

### Precedence (low → high)

| level | operators | assoc |
|---|---|---|
| 1 | `??` | right |
| 2 | `or` | left |
| 3 | `and` | left |
| 4 | `==`, `!=` | left |
| 5 | `<`, `<=`, `>`, `>=` | left |
| 6 | `+`, `-` | left |
| 7 | `*`, `/`, `%` | left |
| 8 | `!`, `-` (unary), `&` (prefix) | — |
| 9 | `.`, `[]`, `()` | left |

- `::` (cast / type hint) is **not** in this chain. It binds only a bare `Term` on its left — wrap compound exprs in parens: `(a + b) :: T!`.
- Unary/postfix (levels 8–9) bind tighter than every binary operator. `&expr`, `!expr`, `-expr`, `.field`, `[i]`, `(args)` all bind as `Term`.

### Arithmetic
- `+ - * /` on `Int`/`Float` (mixed promotes to `Float`). `%` is `Int`-only.
- `/` and `%` on zero → runtime error (`division by zero` / `modulo by zero`).
- `+` overloads on `String!` (concat) and lists (concat). Result type unifies operands.

### Comparison
- `< <= > >=` on **numbers only** (`Int`/`Float`, mixed allowed) — NOT on strings.
- `== !=` are type-safe: mismatched types compare `false`, no coercion (`num == str` is `false`). Work on numbers, strings, bools, null, lists, records. Return `Boolean!`.

### Logical
- `and`, `or` short-circuit (keywords, not `&&`/`||`); result `Boolean!`.
- `!` is unary boolean negation.

### Default `??`
- `nullable ?? fallback` — returns fallback when LHS is null.
- Result type is the **fallback's** type: `T ?? T! → T!`; `T ?? T → T`.
- Right-associative: `a ?? b ?? c` = `a ?? (b ?? c)`.

### Compound assignment
- `+=` desugars to `+`; works on `Int`/`Float`, `String`, lists. Requires LHS to be a mutable field or `let` local.
- `=` is plain reassignment, not an operator on the precedence chain.

### Unary
- `!expr` boolean not, `-expr` numeric negation, `&expr` function reference (see objects.md). All bind a `Term`: `-(1 + 2)`, `!(a or b)` need parens.

## Grammar summary

A condensed view of the user-visible structure (the start rule is `Dang`):

```
Dang         := (Expr Sep)* Expr?         # Sep = newline or comma
Expr         := Import | Decl | Reassignment | Form
Import       := 'import' Symbol
Reassignment := Term AssignOp Form
Decl         := DocString? ( InterfaceDecl | UnionDecl | EnumDecl | ScalarDecl
                           | ObjectDecl | NewConstructorDecl | FieldDecl | DirectiveDecl )
Form         := Return | TryCatch | Raise | Conditional | ForLoop
              | Case | Break | Continue | DefaultExpr | TypeHint | Term
Term         := UnaryExpr | IndexOrCall | SelectOrCall | Literal | List
              | ObjectLiteral | Block | ParenForm | SymbolOrCall

Type         := NonNull | NamedType | ListType | ObjectType | TypeVariable
NonNull      := Type '!'                          # postfix wrapper on any inner type
NamedType    := (NamedType '.')? UpperIdent       # qualifier is itself a NamedType
ListType     := '[' Type ']'
ObjectType   := '{{' (ObjectTypeField Sep)* ObjectTypeField? '}}'
TypeVariable := [a-z]                             # single lowercase letter
```

Notable productions:
- `SelectOrCall`: `Term '.' (ObjectSelection | FieldId ArgValues? BlockArg?)` — field path; zero-arg fields auto-call.
- `BlockArg`: `'{' (BlockParams '=>')? Expr (Sep Expr)* '}'` — trailing block on a call; params optional.
- `ObjectSelection`: `'{' ... '}'` after a `.` — a `FieldSelection` list (`user.{name, posts.{title}}`) or a list of `InlineFragment`s for unions/interfaces.
- `InlineFragment`: `'...' 'on' Symbol ('{' FieldSelection* '}' | '!'?)` — type-narrowing in a selection.
