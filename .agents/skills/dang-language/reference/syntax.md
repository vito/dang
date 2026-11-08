# Syntax, Literals, Operators, Grammar

## File layout

- A file is a sequence of forms separated by **newlines or `;`** (interchangeable; both optional).
- Whitespace is significant only as a separator; indentation is conventional, not syntactic.
- `;` separates forms in a sequence (top-level, block bodies); `,` separates collection/argument elements. A `,` between forms (or a `;` in a list) is a syntax error.
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

## Keywords

```
let pub type interface enum union scalar new implements
if else case break continue return
raise rescue
import directive on
true false null
and or
self
```

- There is no `try`/`catch`, `for` or `while`: use postfix `rescue` and `loop`/`.each` (see control-flow.md).
- To name something like a keyword, write `$name`: `pub $type: String!`, `let $rescue = 1`. It is still named `type`, so `x.type` and `f(type: ...)` need no `$`.

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
- `Int!` — decimal, signed 64-bit. No hex/octal/binary literals; parse them from a string with `"ff".toInt(base: 16)`.
- `Float!` — `3.14`, `1.5e10`, `2.5e-3`. A digit must precede and follow the `.`; exponent `e`/`E` with optional sign. A bare `1.` or `.5` is NOT a float.

### Booleans / Null
- `true`, `false`
- `null` — only assignable to nullable type positions.

### Strings (three flavors)
- **Double-quoted** `"hello"` — single-line, escapes: `\a \b \f \n \r \t \v \" \\`, `\ooo` (octal), `\xNN`, `\uNNNN`, `\UNNNNNNNN`; `\/` yields `/`.
- **Triple-quoted** `"""..."""` — multi-line, **raw** (no escape processing), dedents by minimum indent. A leading/trailing newline adjacent to the fences is stripped (`"""\nhello\n"""` == `"hello"`). Canonical docstring form.
- **Backtick templates** `` `hello ${name}!` `` — single-line, `${...}` interpolation. The ONLY escape is `\${` → literal `${`; every other backslash is literal (`` `\d+` `` stays `\d+`). A lone `$` not followed by `{` is literal. Interpolated values auto-stringify like `toString` (non-strings render as Dang literals, nested strings quoted; `null` → `"null"`).
  - Multi-line: ```` ```...``` ```` — same minimum-indent dedent; fences grow (4+ backticks) to wrap shorter backtick blocks, and the close fence must match the open fence length. Optional language tag (` ```go ... ``` `) is parsed but does not affect the value.

### Lists
- `[1, 2, 3]`, `["a", "b"]`, `[]` (empty), nested `[[1, 2], [3]]`. A trailing comma is fine, in list literals and call arguments alike (`[1, 2,]`, `f(a, b,)`).
- An empty `[]` (or `[:]`) takes its element type from context — typed slots, arguments, `??`/`+` with a typed operand, `if`/`case` branches, `reduce` initials, a later use of an untyped `let`. A hint is only needed when one untyped binding must serve two element types (see stdlib.md). It can be compared directly: `xs == []`.

### Records (object literals) — `{{ ... }}`
- `{{ key: value, other: value }}` — note the **double braces**. Always non-null.
- A bare name is **shorthand** for `name: name`: `{{ name, age }}` ≡ `{{ name: name, age: age }}` (values taken from variables in scope), mirroring object selection's bare-field form (`recv.{{ name }}` ≡ `recv.{{ name: name }}`).
- Same `{{ ... }}` syntax is also a record *type* annotation: `x :: {{foo: Int!, bar: Status!}}!`.
- Nestable. Serialized to JSON with its fields **in the order written**; `JSON.encode(value, sortKeys: true)` sorts them.
- Fields may reference each other in **any order** (`{{ total: a + b, a: 1, b: 2 }}`); a cyclic reference is a compile error.
- A field's **own name resolves to the enclosing scope**, not the field being defined: `{{ user: user.{{name}} }}` reads the outer `user`. Siblings still see the field.
- `{{ }}` is **always parallel**: fields evaluate concurrently (one referencing a sibling waits for it), failing fast on the first error. So a field can't write state declared outside it; fields produce values instead (objects.md "Compile-time rules").

## Operators

### Precedence (low → high)

| level | operators | assoc |
|---|---|---|
| 1 | `??` | right |
| 2 | `rescue` (postfix error handling) | left |
| 3 | `or` | left |
| 4 | `and` | left |
| 5 | `==`, `!=` | left |
| 6 | `<`, `<=`, `>`, `>=` | left |
| 7 | `+`, `-` | left |
| 8 | `*`, `/`, `%` | left |
| 9 | `!`, `-` (unary), `&` (prefix) | — |
| 10 | `.`, `[]`, `()` | left |

- `::` (cast / type hint) is **not** in this chain. It binds only a bare `Term` on its left — wrap compound exprs in parens: `(a + b) :: T!`.
- Unary/postfix (levels 9–10) bind tighter than every binary operator. `&expr`, `!expr`, `-expr`, `.field`, `[i]`, `(args)` all bind as `Term`.
- `if`, `case`, and `raise` are `Form`s, not `Term`s, so one used as a binary operand needs parentheses: `a + (if (c) { x } else { y })`. Without them (`a + if (c) ...`) it's a syntax error.

### Gotchas
- **Untyped template bindings are `String!`**: `` let re = `\d+` `` is a String, not a `Regexp`; the literal-to-scalar coercion happens only where the literal meets a typed slot, so `s.containsMatch(re)` fails with `cannot use String! as Regexp!`. Annotate: `` let re: Regexp! = `\d+` `` (same for `Path!`, `URL!`, ...).
- **Write `!x`, not `x == false`**, on a `Boolean!`. `!` rejects a nullable `Boolean` (narrow it, or `!(x ?? false)`); `x == false` on one is `false` for null.
- **Prefer templates to `+` chains**: `` `${name} (${i})` `` over `name + " (" + toString(i) + ")"` — interpolation stringifies any value.

### Arithmetic
- `+ - * / %` on `Int`/`Float` (mixed promotes to `Float`). Float `%` is Go's `math.Mod`: the remainder takes the dividend's sign (`-7.5 % 2 == -1.5`).
- `/` and `%` on zero → runtime error (`division by zero` / `modulo by zero`).
- `+` overloads on `String!` (concat) and lists (concat). Operands must be **non-null** (a nullable one is a type error — `!`, `??`, or null-check it first); the result is non-null.

### Comparison
- `< <= > >=` on any **Ordered** type — numbers (`Int`/`Float`, mixed allowed), strings (by bytes), `Boolean` (`false < true`), enums (declaration order), custom scalars, lists (lexicographic) and `{{ }}` records (field by field), the same order `.sort` uses — or a type variable bounded `[a: Ordered]` (see types.md). Operands must match and be non-null, like arithmetic. Objects have no order (static error).
- `== !=` only compare operands that could be equal: same type (nullability aside), `Int`/`Float` (numerically), subtype/supertype, union/member, `null`. Unrelated types are a static error: `1 == "1"`, unrelated objects. A string *literal* against an enum or scalar converts to it (`c == "GREEN"`, must name a value — `c == Color.GREEN` reads clearer; `node.id == "User:1"`); a non-literal `String` needs a cast (`(s :: ID!)`). Work on numbers, strings, bools, null, lists, maps, records. Return `Boolean!`. Records and objects: see objects.md "Equality".

### Logical
- `and`, `or` short-circuit (keywords, not `&&`/`||`); result `Boolean!`.
- `!` is unary boolean negation.

### Default `??`
- `nullable ?? fallback` — returns fallback when LHS is null.
- Result type is the **fallback's** type: `T ?? T! → T!`; `T ?? T → T`.
- Right-associative: `a ?? b ?? c` = `a ?? (b ?? c)`.

### Error fallback `rescue`
- `expr rescue fallback` and `expr rescue { clauses }` — postfix error handling; binds tighter than `??`, looser than `or`; left-associative (chains re-raise into the next `rescue`). A bare `{` after `rescue` always starts a clause block. Semantics in control-flow.md.

### Compound assignment
- `+=` desugars to `+`; works on `Int`/`Float`, `String`, lists. Requires LHS to be a mutable field or `let` local (a top-level binding only from top-level script code, not from inside a function or method), and a **non-null** one (`let n: Int = 0; n += 1` is an error: use `Int!` or `n = (n ?? 0) + 1`; `n! += 1` is rejected).
- `=` is plain reassignment, not an operator on the precedence chain.

### Unary
- `!expr` boolean not, `-expr` numeric negation, `&expr` function reference (see objects.md). All bind a `Term`: `-(1 + 2)`, `!(a or b)` need parens.

## Grammar summary

A condensed view of the user-visible structure (the start rule is `Dang`):

```
Dang         := (Expr FormSep)* Expr?     # FormSep = newline or ';' (forms in sequence); Sep = newline or ',' (collection elements)
Expr         := Import | Decl | Reassignment | Form
Import       := 'import' Symbol
Reassignment := Term AssignOp Form
Decl         := DocString? ( InterfaceDecl | UnionDecl | EnumDecl | ScalarDecl
                           | ObjectDecl | NewConstructorDecl | FieldDecl | DirectiveDecl )
Form         := Return | Raise | Conditional
              | Case | Break | Continue | DefaultExpr | TypeHint | Term
Term         := UnaryExpr | IndexOrCall | SelectOrCall | Literal | List
              | ObjectLiteral | Block | ParenForm | SymbolOrCall

Type         := NonNull | NamedType | ListType | ObjectType | TypeVariable
NonNull      := Type '!'                          # postfix wrapper on any inner type
NamedType    := (NamedType '.')? UpperIdent       # qualifier is itself a NamedType
ListType     := '[' Type ']'
ObjectType   := '{{' (ObjectTypeField Sep)* ObjectTypeField? '}}'
TypeVariable := [a-z]                             # single lowercase letter

TypeParamList := '[' TypeParam (Sep TypeParam)* ']'  # right after a function/method name: f[a, k: Ordered](...)
TypeParam     := Word (':' NamedType ('&' NamedType)*)?   # one letter for now; Ordered is the only bound
```

Notable productions:
- `RescueExpr`: `(RescueExpr | Term) 'rescue' ('{' RescueClause* '}' | fallback)` — sits in the binary-operator chain between `??` and `or` (`DefaultExpr` bottoms out in it). Left-recursive, so chains associate left.
- `SelectOrCall`: `Term '.' (ObjectSelection | FieldId ArgValues? BlockArg?)` — field path; zero-arg fields auto-call.
- `BlockArg`: `'{' (BlockParams '=>')? Expr (Sep Expr)* '}'` — trailing block on a call; params optional.
- `ObjectSelection`: `'{{' ... '}}'` after a `.` — a `FieldSelection` list (`user.{{name, posts.{{title}}}}`) or a list of `InlineFragment`s for unions/interfaces. A field may carry a GraphQL-style alias (`user.{{full: name}}`); a bare field is shorthand for `name: name`.
- `InlineFragment`: `'...' 'on' Symbol ('{{' FieldSelection* '}}' | '!'?)` — type-narrowing in a selection; the field set uses double braces like any selection.
