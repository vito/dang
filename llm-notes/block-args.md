# Block Arguments

Block arguments are special function parameters that allow passing code blocks to functions, similar to Ruby's block syntax. They enable higher-order function patterns with clean syntax and bidirectional type inference.

## Syntax

### Declaring Functions with Block Arguments

Block arguments are declared in function signatures using the `&` prefix, followed by the parameter name and its function type:

```dang
pub myFun(&block(x: Int!): String!): String! {
  block(42)
}
```

Syntax breakdown:
- `&block` - the parameter name prefixed with `&`
- `(x: Int!)` - the block's parameter list
- `: String!` - the block's return type

### Calling Functions with Block Arguments

Functions with block arguments are called using curly braces `{}` with a lambda-style syntax:

```dang
pub result: String! = myFun() { x -> toJSON(x) }
```

The block can ignore parameters it doesn't need:

```dang
pub result: String! = myFun() { "constant value" }
```

### Combining Regular Arguments and Block Arguments

Functions can have both regular arguments and a block argument. The block argument must come last in the signature:

```dang
pub withArg(prefix: String!, &block(x: Int!): String!): String! {
  prefix + block(99)
}

pub result: String! = withArg("Number: ") { x -> toJSON(x) }
```

## Implementation Details

### AST Structure

**SlotDecl** (`pkg/dang/slots.go`):
- `IsBlockParam bool` - marks this SlotDecl as a block parameter

**FunctionBase** (`pkg/dang/ast_declarations.go`):
- `BlockParam *SlotDecl` - holds the optional block parameter declaration

**FunctionValue** (`pkg/dang/eval.go`):
- `BlockParamName string` - stores the name of the block parameter for runtime binding

### Type Inference

During function type inference (`inferFunctionType`):

1. Block parameter type is inferred from its type annotation
2. The type must be a function type (`*hm.FunctionType`)
3. The block parameter is added to the function's environment with its actual name
4. The function type's block is set using `fnType.SetBlock(blockType)`

This enables bidirectional type inference - the function signature communicates to callers what type of block is expected, and the block implementation is type-checked against this signature.

### Runtime Evaluation

During function evaluation:

1. **At call site** (`FunCall.Eval`): The block argument is extracted and stored in the context using `blockArgContextKey`
2. **In function body** (`FunctionValue.Call`): 
   - The block value is retrieved from the context
   - It's bound to the function's environment using the block parameter name
   - The block can then be called like any other function

This design allows the block to be referenced by name within the function body (e.g., `block(42)`), making it a first-class value rather than requiring a special keyword.

## Examples

### Simple Block Call

```dang
pub myFun(&block(x: Int!): String!): String! {
  block(42)
}

pub result: String! = myFun() { x -> toJSON(x) }
assert { result == "42" }
```

### Multiple Block Calls

Blocks can be called multiple times within the function:

```dang
pub twice(&block(x: Int!): Int!): Int! {
  block(1) + block(2)
}

pub result: Int! = twice() { x -> x * 10 }
assert { result == 30 }  # 10 + 20
```

### Standard Library Usage

The standard library uses block arguments extensively. For example, `List.map`:

```dang
pub numbers: [Int!]! = [1, 2, 3]
pub doubled: [Int!]! = numbers.map { x -> x * 2 }
assert { doubled == [2, 4, 6] }
```

## Design Rationale

Block arguments were chosen over lambdas for several reasons:

1. **Simpler type inference**: Block types are explicitly declared in function signatures, eliminating the need for complex constraint solving or row polymorphism
2. **Bidirectional inference**: The function signature tells the block what types to expect, and the block implementation is validated against this
3. **Named parameters**: Blocks are named in the function signature (e.g., `&block`), making them visible and callable as regular function values
4. **No special keywords needed**: The block is just a named parameter that can be called normally, rather than requiring special syntax like `yield`

## Limitations

- Only one block argument per function is supported
- Block arguments must come after all regular arguments
- Block arguments cannot have default values
- The block parameter name is required (cannot be anonymous)
