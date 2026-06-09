\use-plugin{dang}

# Grammar notes {#grammar}

> Meta: not a full BNF — the source of truth is `pkg/dang/dang.peg`. This page captures the user-visible regularities so people don't have to read PEG.

## Source of truth

- `pkg/dang/dang.peg` — Pigeon-PEG grammar; generates `pkg/dang/dang.peg.go`
- the tree-sitter grammar at `treesitter/` is derived from the same source

## Top-level structure

> The start rule is `Dang`, not `Module`. `Import` and `Reassignment` are siblings of `Decl`/`Form`, not members of `Decl`.

```
Dang         := (Expr Sep)* Expr?         # Sep = newline or comma
Expr         := Import | Decl | Reassignment | Form
Import       := 'import' Symbol
Reassignment := Term AssignOp Form
Decl         := DocString? ( InterfaceDecl | UnionDecl | EnumDecl | ScalarDecl
                           | ObjectDecl | NewConstructorDecl | FieldDecl | DirectiveDecl )
Form         := Return | TryCatch | Raise | Conditional | ForLoop
              | Case | Break | Continue | DefaultExpr | TypeHint | Term
Term         := UnaryExpr | NonNullAssert | IndexOrCall | SelectOrCall | Literal
              | MapLiteral | List | ObjectLiteral | Block | ParenForm | SymbolOrCall
NonNullAssert := Term '!'
```

## Separators

- newlines and commas are interchangeable inside arg lists, lists, records
- top-level forms are separated by newlines

## Expression form (precedence)

> Meta: keep this in sync with [#operators] precedence table — duplicating it isn't ideal but it's the kind of thing readers expect on a grammar page.

## Type syntax

> `Type` dispatches to one of these; non-null is a suffix `!` wrapping any inner type. See [#types].

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

## Lexical

- identifiers: `[a-zA-Z_][a-zA-Z0-9_]*` (lowercase-leading = value, uppercase-leading = type)
- comments: `#` to end of line
- strings:
  - `"..."` (with escapes), `"""..."""` triple-quoted (docstrings reuse this)
  - `` `...` `` backtick templates with `${expr}` interpolation; `\${` escapes a literal `${`. Longer backtick fences (` ``` `) nest. See [#strings].
  - `%word{...}` quoted/raw form
- numbers: `Int` decimal; `Float` requires a fraction (`1.0`) or exponent (scientific notation)

## Reserved words

- keyword tokens (each `!WordChar`-terminated): `and`, `break`, `case`, `catch`, `continue`, `directive`, `else`, `enum`, `false`, `for`, `if`, `implements`, `import`, `interface`, `let`, `new`, `null`, `on`, `or`, `pub`, `raise`, `return`, `scalar`, `self`, `true`, `try`, `type`, `union`
- `pub` is optional — the visibility keyword is parsed but no longer required; declarations are public by default (see [#fields])
- see [#syntax]

## Notable productions

- `SelectOrCall`: `Term '.' (ObjectSelection | FieldId ArgValues? BlockArg?)` — the field path; zero-arg fields auto-call. See [#fields].
- `BlockArg`: `'{' (BlockParams '=>')? Expr (Sep Expr)* '}'` — trailing block attached to a call; params are optional. See [#blocks].
- `ObjectSelection`: `"{{" ... "}}"` after a `.` — two forms: a `FieldSelection` list (`user.{{name, posts.{{title}}}}`), or a list of `InlineFragment`s for unions/interfaces. The double braces mirror record literals `{{ }}`. See [#objects].
- `DotBlock`: `'.' BlockArg` — a single-brace block applied to the receiver (`foo.{ bar(_) }` ≡ `bar(foo)`), a sibling of `ObjectSelection` and method calls at `.` precedence. See [#blocks]'s [#dot-block].
- `FieldSelection`: `Id ArgValues? ('.' ObjectSelection)?` — a field in a selection, optionally with args and a nested `{{ }}` selection.
- `InlineFragment`: `'...' 'on' Symbol ('{' FieldSelection* '}' | '!'?)` — type-narrowing in a selection. See [#interfaces-unions].
