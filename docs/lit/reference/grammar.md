\use-plugin{dang}
\literate-fences

# Grammar notes {#grammar}

> Meta: not a full BNF — the source of truth is `pkg/dang/dang.peg`. This page captures the user-visible regularities so people don't have to read PEG.

> The `dang` examples on this page are live: the docs build runs them and
> bakes in the results. The grammar sketches are plain text.

## Source of truth

- `pkg/dang/dang.peg` — Pigeon-PEG grammar; generates `pkg/dang/dang.peg.go`
- the tree-sitter grammar at `treesitter/` is derived from the same source

## Top-level structure

> The start rule is `Dang`, not `Module`. `Import` and `Reassignment` are siblings of `Decl`/`Form`, not members of `Decl`.

```
Dang         := (Expr FormSep)* Expr?     # FormSep = newline or ';' (forms in sequence); Sep = newline or ',' (collection elements)
Expr         := Import | Decl | Reassignment | Form
Import       := 'import' Symbol
Reassignment := Term AssignOp Form
Decl         := DocString? ( InterfaceDecl | UnionDecl | EnumDecl | ScalarDecl
                           | ObjectDecl | NewConstructorDecl | FieldDecl | DirectiveDecl )
Form         := Return | RescueExpr | Raise | Conditional
              | Case | Break | Continue | DefaultExpr | TypeHint | Term
Term         := UnaryExpr | NonNullAssert | IndexOrCall | SelectOrCall | Literal
              | MapLiteral | List | ObjectLiteral | Block | ParenForm | SymbolOrCall
NonNullAssert := Term '!'
RescueExpr   := (RescueExpr | Term) 'rescue' (RescueClauses | Fallback)   # left-associative chaining; Fallback is any Term
RescueClauses := '{' RescueClause+ '}'    # a bare '{' after 'rescue' always starts clauses; a record fallback is '{{ ... }}'
RescueClause := (Symbol ':' Type '!' | 'else') '=>' Form   # a type pattern, as in a case clause: any type, written non-null
```

## Separators

- forms (top-level, block bodies) are separated by newlines or `;` — `FormSep`
- newlines and commas are interchangeable inside arg lists, lists, records — `Sep`

## Expression form (precedence)

> Meta: keep this in sync with [#operators] precedence table — duplicating it isn't ideal but it's the kind of thing readers expect on a grammar page.

- `rescue` sits between `??` and `or`: it binds tighter than `??` and looser than `or`, so `x rescue null ?? "default"` is `(x rescue null) ?? "default"` (see [#errors])

```dang
(JSON.decode("null") :: Int) rescue null ?? -1
```

Read the other way, as `… rescue (null ?? -1)`, this would be `null`: the
decode succeeds, so the fallback never applies.

## Type syntax

> `Type` dispatches to one of these; non-null is a suffix `!` wrapping any inner type. See [#nullability].

```
Type         := NonNull | AppliedType | NamedType | ListType | ObjectType | TypeVariable
NonNull      := Type '!'
AppliedType  := NamedType '[' (Type Sep)* Type? ']'   # generic application, e.g. List[a], Map[a]
NamedType    := (NamedType '.')? UpperIdent     # qualifier is itself a NamedType
ListType     := '[' Type ']'                    # shorthand for List[...]
ObjectType   := '{{' (ObjectTypeField Sep)* ObjectTypeField? '}}'
TypeVariable := [a-z]                           # single lowercase letter
```

> `[a]` is shorthand for `List[a]`. `Map[a]` is a string-keyed map of `a` values
> (only the built-in `List` and `Map` accept type arguments today).

A function or method declaration may list its type parameters right after
its name (see [#type-params]):

```
TypeParamList := '[' TypeParam (Sep TypeParam)* Sep? ']'   # immediately after the name: f[a, k: Ordered](...), and on an interface member header
TypeParam     := Word (':' Bound ('&' Bound)*)?           # a single lowercase letter
Bound         := NamedType                                # Ordered or Numeric
```

> The grammar reads a parameter as a word and a bound as a type name; a
> parameter must be one lowercase letter, and the bounds are `Ordered` and
> `Numeric`.

Each form in use:

```dang
import Demo

let grid: List[[Int!]!]! = [[1, 2], [3]]
let tags: Map[[String!]!]! = ["fruit": ["apple"], "veg": []]
let origin: {{x: Int!, y: Int!}}! = {{x: 0, y: 0}}
let viewer: Demo.User = null
pick(first: a, second: a): a { first }
largest[a: Ordered](xs: [a!]!): a { xs.sort(descending: true)[0] }

[grid.length, tags["fruit"]!.length, origin.x, pick(1, 2), largest([2, 7, 3])]
```

## Lexical

- identifiers: `[a-zA-Z_][a-zA-Z0-9_]*` (lowercase-leading = value, uppercase-leading = type)
- comments: `#` to end of line
- strings:
  - `"..."` and `"""..."""`: escapes and `${expr}` interpolation; `\$` escapes a dollar sign, so `\${` is a literal `${` (before Dang 2.5, `"..."` takes `${` literally and `"""` is raw)
  - `'...'` and `'''...'''`: verbatim, with no escapes or interpolation; `'...'` can't hold a `'`
  - `` `...` `` backtick templates: raw text with `${expr}` interpolation; `\${` escapes a literal `${`. Longer backtick fences (` ``` `) nest
  - a tripled delimiter (`"""`, `'''`, ` ``` `) makes a multi-line, dedented string whose opening line holds at most a language tag (`"""sql`), unless it closes on the same line
  - docstrings are triple-quoted but always verbatim, and may start on the opening line. See [#literals] and [#strings].
- numbers: `Int` decimal; `Float` requires a fraction (`1.0`) or exponent (scientific notation)

## Reserved words

- keyword tokens (each `!WordChar`-terminated): `and`, `break`, `case`, `continue`, `directive`, `else`, `enum`, `false`, `if`, `implements`, `import`, `interface`, `let`, `new`, `null`, `on`, `or`, `pub`, `raise`, `rescue`, `return`, `scalar`, `self`, `true`, `type`, `union` (`try` and `catch` are ordinary identifiers now; legacy `try`/`catch` blocks still parse but warn at type-check — see [#errors])
- of these, `null` and `rescue` are never identifiers (`WordToken` excludes them). Where only a name can go (`FieldId`, `KeyValue` keys, `FieldSelection`, object type keys), `rescue` is a name (`ReservedName`). Elsewhere, the rarely needed escape `$rescue` (`RawWordToken`) spells the identifier `rescue`; `dang migrate` writes it for names a newer version reserves
- `pub` is optional — the visibility keyword is parsed but no longer required; declarations are public by default (see [#fields])
- see [#syntax]

## Notable productions

- `SelectOrCall`: `Term '.' (ObjectSelection | FieldId ArgValues? BlockArg?)` — the field path; zero-arg fields auto-call. See [#fields].
- `BlockArg`: `'{' (BlockParams '=>')? Expr (Sep Expr)* '}'` — trailing block attached to a call; params are optional. See [#blocks].
- `ObjectSelection`: `"{{" ... "}}"` after a `.` — two forms: a `FieldSelection` list (`user.{{name, posts.{{title}}}}`), or a list of `InlineFragment`s for unions/interfaces. The double braces mirror record literals `{{ }}`. See [#objects].
- `DotBlock`: `'.' BlockArg` — a single-brace block applied to the receiver (`foo.{ bar(_) }` ≡ `bar(foo)`), a sibling of `ObjectSelection` and method calls at `.` precedence. See [#blocks]'s [#dot-block].
- `FieldSelection`: `(Id ':')? Id ArgValues? ('.' ObjectSelection)?` — a field in a selection, optionally with args and a nested `{{ }}` selection. A leading `Id ':'` is a GraphQL-style **alias** that renames the field in the result (`user.{{ full: name }}`); a bare field is shorthand for `name: name`.
- `InlineFragment`: `'...' 'on' Symbol ('{{' FieldSelection* '}}' | '!'?)` — type-narrowing in a selection; the field set uses the same double braces as any selection. See [#interfaces-unions].
