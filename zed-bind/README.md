# Bind Language Extension for Zed

This extension provides [Bind language](https://github.com/vito/bind) support for the Zed editor.

## Features

- **Syntax Highlighting**: Full syntax highlighting for Bind language constructs
- **Tree-sitter Grammar**: Uses the official tree-sitter grammar from the Bind repository
- **Code Folding**: Support for folding functions, classes, conditionals, and other blocks
- **Indentation**: Smart indentation for Bind code structures
- **Bracket Matching**: Automatic bracket completion and matching

## Language Features Supported

### Core Language Constructs
- **Variables**: `pub name = value`, `let name = value`
- **Functions**: `pub func(arg: Type): ReturnType { body }`
- **Classes**: `cls ClassName { ... }`
- **Conditionals**: `if condition { then } else { else }`
- **Let bindings**: `let x = value in expression`
- **Lambda expressions**: `\x -> expression`
- **Pattern matching**: `match expr with cases`

### Data Types
- **Primitives**: strings (`"hello"`), integers (`42`), booleans (`true`/`false`), null
- **Collections**: lists (`[1, 2, 3]`), records (`{key: value}`)
- **Type annotations**: `Type!` (non-null), `[Type]` (list), custom types

### Dagger Integration
- Special highlighting for Dagger-related operations like `container`, `directory`, `file`, etc.
- Support for container orchestration syntax

## Installation

1. Open Zed
2. Press `Cmd+Shift+P` (Mac) or `Ctrl+Shift+P` (Linux/Windows)
3. Type "zed: install dev extension"
4. Select the `zed-bind` directory

## About Bind

Bind is a functional programming language designed for [Dagger](https://dagger.io) with:
- **Hindley-Milner type inference**: Strong typing without explicit type annotations
- **Container orchestration**: Built-in integration with Dagger's container API
- **Functional paradigm**: Immutable data structures and pure functions
- **Type safety**: All types derived from Dagger's GraphQL API

## Example Code

```bind
# Simple variable declaration
pub greeting = "Hello, Bind!"

# Function with type inference
pub identity = \x -> x

# Conditional expression
pub result = if true { "success" } else { "failure" }

# Let binding
pub computed = let x = 10 in x * 2

# Class definition for container operations
type MyContainer {
  pub build(): Container! {
    container.from("alpine:latest")
      .withExec(["echo", "Hello from Bind!"])
  }
}
```

## Contributing

This extension is part of the [Bind language project](https://github.com/vito/bind).
Contributions are welcome!

## License

MIT License - see the main Bind repository for details.
