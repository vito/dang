\use-plugin{dang}

# Grammar notes {#grammar}

> Meta: not a full BNF — the source of truth is `pkg/dang/dang.peg`. This page captures the user-visible regularities so people don't have to read PEG.

## Source of truth

- `pkg/dang/dang.peg` — Pigeon-PEG grammar; generates `pkg/dang/dang.peg.go`
- the tree-sitter grammar at `treesitter/` is derived from the same source

## Top-level structure

```
Module    := (Decl | Form)*
Decl      := Import | Interface | Union | Enum | Scalar
           | Class | NewConstructor | Slot | DirectiveDecl
Form      := Return | TryCatch | Raise | Conditional | ForLoop
           | Case | Break | Continue | DefaultExpr | TypeHint | Term
```

## Separators

- newlines and commas are interchangeable inside arg lists, lists, records
- top-level forms are separated by newlines

## Expression form (precedence)

> Meta: keep this in sync with [operators](../language/operators.md) precedence table — duplicating it isn't ideal but it's the kind of thing readers expect on a grammar page.

## Type syntax

```
Type      := NamedType ('!')?
NamedType := (Type '.')? UpperIdent
           | '[' Type ']'
           | TypeVar
TypeVar   := [a-z]   # single lowercase letter
```

## Lexical

- identifiers: `[a-zA-Z_][a-zA-Z0-9_]*` (lowercase-leading = value, uppercase-leading = type)
- comments: `#` to end of line
- strings: `"..."`, `"""..."""`, `` `...` ``, ` ```...``` `
- numbers: standard decimal, scientific notation for floats

## Reserved words

- see [syntax](../language/syntax.md#reserved-words)

## Notable productions

- `SelectOrCall`: `term '.' (selection | identifier args? block?)` — auto-call when zero-arg
- `BlockArg`: trailing `{ params => body }` attached to a call
- `ObjectSelection`: `term '.' '{' fieldList '}'` — multi-field GraphQL selection
- `InlineFragment`: `'...' 'on' Type ('{' fieldList '}')?`
