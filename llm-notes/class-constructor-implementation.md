# Class Constructor Implementation

## Key Design Decision
Classes in Dash are implemented as **constructor functions**, not prototypical objects. This prevents invalid states where required fields could be null.

## Syntax Change
- **Before**: `cls Foo { pub a: String! }` - created prototypical object with null for non-null fields
- **After**: `cls Foo(a: String!) { ... }` - requires constructor call `Foo("value")`

## Implementation Details

### Grammar Changes
- Modified `Class` rule in `pkg/dash/dash.peg` to accept optional `ArgTypes`
- Added `ConstructorArgs []SlotDecl` field to `ClassDecl` struct

### Type System Integration
- **Hoisting Phase**: Classes immediately get constructor function types `(args...) -> ClassName!`
- **Inference Phase**: Constructor function types are refined with proper argument types
- **Evaluation Phase**: Constructor functions clone prototypes and set constructor arguments

### Function Value Extension
- Added `ConstructorPrototype *ModuleValue` field to `FunctionValue`
- Constructor functions clone prototype and set fields during instantiation
- Regular functions work unchanged

## Critical Implementation Points

1. **Hoisting is Key**: Constructor function types MUST be assigned during `Hoist()`, not just `Infer()`, to support forward references and cross-file usage.

2. **Both Syntaxes Work**: 
   - `cls Foo(a: String!) { ... }` → `Foo("value")`
   - `cls Bar { ... }` → `Bar()`

3. **Constructor Arguments Become Fields**: Arguments are automatically available as `self.argName` in methods.

## Testing Patterns
Always test both constructor arguments and field access:
```dash
cls Person(name: String!, age: Int!) {
  pub greet: String! { "Hello, I'm " + self.name }
}

assert { Person("Alice", 30).name == "Alice" }
assert { Person("Alice", 30).greet == "Hello, I'm Alice" }
```