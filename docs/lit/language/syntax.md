\use-plugin{dang}

# Syntax {#syntax}

> Meta: this page is the boring foundation. Focus on what would surprise someone coming from JS/Go/Python (semicolons-optional, newline-as-separator, `#` not `//`, no trailing commas in formatted output). Move grammar rules to `reference/grammar.md`.

## File layout

- a file is a sequence of forms separated by newlines or commas
- whitespace is significant only as separators; indentation is conventional, not syntactic

## Comments

- `#` to end of line
- placed on their own line or trailing; both are idiomatic
- formatter keeps them attached to the following or preceding code

> Meta: `///` doc comments don't exist — docstrings are real triple-quoted strings attached to declarations (covered in [bindings](./bindings.md) and [functions](./functions.md)).

## Identifiers

- lowercase: `foo_bar`, `userName` — values and methods
- capitalized: `User`, `String` — types
- single lowercase letters in type positions (`a`, `b`) — type variables
- `_` is **not** a special "ignore" pattern (TBD?)

## Reserved words

- `pub`, `let`, `type`, `interface`, `enum`, `union`, `scalar`, `new`, `implements`
- `if`, `else`, `case`, `for`, `break`, `continue`, `return`
- `try`, `catch`, `raise`
- `import`, `directive`, `on`
- `true`, `false`, `null`
- `and`, `or`
- `self`

## Separators and trailing commas

- newlines and commas are interchangeable inside lists, arg lists, object literals
- formatter strips trailing commas

## Backticks and string-template lexer mode

- backtick strings are templates with `#{expr}` interpolation
- inside backticks, `#` is part of interpolation syntax (not a comment)
- multi-line backtick fences (3, 4, 5+) for nested code blocks
- see [literals](./literals.md)

> Meta: flag the `${` → `#{` migration if/when it lands (see `regexp.md`).
