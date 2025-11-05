# Task: Replace Lambda with Block Args

## Goal

Replace the generic `\x -> ...` lambda syntax with a special block arg syntax `: { x -> ... }` that has dedicated bidirectional type inference semantics. This will simplify the type system by removing the need to special-case lambda arguments in generic function call handling.

## Design Principles

1. **Retire Lambda completely** - Remove `\x -> ...` syntax from the language
2. **Block Args are special** - They are not just syntactic sugar for lambdas
3. **Bidirectional inference** - Both parameter types AND expected return type flow from the definition site
4. **Separate AST node** - Block args should be their own AST node type, not Lambda
5. **Explicit in FunCall** - Block args should be a separate field, not just another argument

## Architecture Changes

### 1. New AST Node: `BlockArg`

Create a new AST node type that is distinct from `Lambda`:

```go
type BlockArg struct {
    InferredTypeHolder
    Args []*SlotDecl
    Body Node
    Loc  *SourceLocation
}
```

Key differences from Lambda:
- Does NOT implement the same inference logic as Lambda
- Uses bidirectional inference with both parameter AND return type constraints
- Only allowed in function call positions (not as standalone expressions)

### 2. Modify `FunCall` Structure

Add block arg as explicit field:

```go
type FunCall struct {
    InferredTypeHolder
    Fun      Node
    Args     Record
    BlockArg *BlockArg  // NEW: separate from Args
    Loc      *SourceLocation
}
```

### 3. Remove Lambda from Grammar

**Before:**
```peg
Form <- Conditional / ForLoop / Lambda / Match / Assert / Break / Continue / DefaultExpr / TypeHint / Term
```

**After:**
```peg
Form <- Conditional / ForLoop / Match / Assert / Break / Continue / DefaultExpr / TypeHint / Term
```

Remove the entire `Lambda` rule and `LambdaArgs` parsing.

### 4. Keep BlockArg Grammar

The block arg grammar stays mostly the same, but returns a `BlockArg` node instead of `Lambda`:

```peg
BlockArg <- _ ':' _ '{' _ params:BlockParams _ ArrowToken _ body:Form _ '}' {
  return &BlockArg{
    Args: params.([]*SlotDecl),
    Body: body.(Node),
    Loc: c.Loc(),
  }, nil
}
```

### 5. Update Grammar Actions

In both `Call` and `SelectOrCall`, don't append block arg to args - just attach it:

**Before:**
```go
if blockArg != nil {
    blockLambda := blockArg.(*Lambda)
    argRecord = append(argRecord, Keyed[Node]{
        Key:   "fn",
        Value: blockLambda,
        Positional: false,
    })
}
```

**After:**
```go
// Don't append - just attach as separate field
return &FunCall{
    Fun: ...,
    Args: argRecord,
    BlockArg: blockArg.(*BlockArg),  // Store directly
    Loc: c.Loc(),
}
```

## Type Inference Changes

### Current Problem

The current `checkArgumentType` tries to handle lambdas generically:
- It only constrains parameter types
- Return type inference is left to the lambda itself
- This doesn't work well for nested cases

### New Approach: Bidirectional Inference for Block Args

When type checking a `FunCall` with a `BlockArg`:

1. **Infer the function type** first
2. **Extract the expected block type** from the function signature (look for "fn" parameter)
3. **Constrain both parameters AND return type** on the block arg
4. **Infer the block body** with these constraints
5. **Verify** the body type matches the expected return type

#### Example Flow

```dang
pub numbers: [Int!]! = [1, 2, 3]
pub doubled: [Int!]! = numbers.map: { x -> x * 2 }
```

1. Infer `numbers.map` → type is `(fn: (item: Int!) -> b) -> [b]!`
2. Instantiate with fresh type variable: `(fn: (item: Int!) -> b₁) -> [b₁]!`
3. Extract expected block type: `(item: Int!) -> b₁`
4. Constrain block arg:
   - Parameter `x` : `Int!`
   - Expected return type: `b₁`
5. Infer body `x * 2` with `x: Int!` → type is `Int!`
6. Unify `Int!` with `b₁` → `b₁ = Int!`
7. Return type of call: `[Int!]!`

#### Nested Example

```dang
pub nested: [[Int!]!]! = [[1, 2], [3, 4]]
pub doubled: [[Int!]!]! = nested.map: { inner ->
  inner.map: { x -> x * 2 }
}
```

1. Outer `map`: `(fn: (item: [Int!]!) -> b₁) -> [[b₁]!]!`
2. Constrain outer block:
   - `inner`: `[Int!]!`
   - Expected return: `b₁`
3. Infer body `inner.map: { x -> x * 2 }`:
   - Inner `map`: `(fn: (item: Int!) -> b₂) -> [b₂]!`
   - Constrain inner block: `x`: `Int!`, return: `b₂`
   - Infer `x * 2` → `Int!`
   - Unify `b₂ = Int!`
   - Inner call returns: `[Int!]!`
4. Unify outer return: `b₁ = [Int!]!`
5. Outer call returns: `[[Int!]!]!` ✅

## Implementation Steps

### Phase 1: Create BlockArg AST Node

- [x] Create new `BlockArg` type in `ast_expressions.go`
- [x] Implement all Node interface methods (DeclaredSymbols, ReferencedSymbols, etc.)
- [x] Implement `Infer()` with bidirectional inference logic
- [x] Implement `Eval()` (similar to Lambda)
- [x] Implement `Walk()`

### Phase 2: Update FunCall Structure

- [x] Add `BlockArg *BlockArg` field to `FunCall` struct
- [x] Update `FunCall.ReferencedSymbols()` to include block arg symbols
- [x] Update `FunCall.Walk()` to traverse block arg
- [x] Update `FunCall.Infer()` to handle block arg specially
- [x] Update `FunCall.Eval()` to pass block arg to function

### Phase 3: Update Grammar

- [ ] Remove `Lambda` from `Form` rule
- [ ] Remove `Lambda` rule and `LambdaArgs`/`LambdaArg` rules
- [ ] Remove `LambdaToken` (or keep for better error messages?)
- [ ] Update `BlockArg` rule to return `&BlockArg{}` instead of `&Lambda{}`
- [ ] Update `Call` rule to store block arg in struct field, not append to args
- [ ] Update `SelectOrCall` rule similarly
- [ ] Run `./hack/generate` to regenerate parser

### Phase 4: Implement Bidirectional Inference

- [x] In `FunCall.Infer()`, when block arg is present:
  - [x] Infer the function type
  - [x] Look for "fn" parameter in the function's argument record
  - [x] Extract the expected function type `(params...) -> returnType`
  - [x] Pass both parameter types AND return type to block arg
- [x] In `BlockArg.Infer()`:
  - [x] Accept expected parameter types (set `ContextInferredType` on each param)
  - [x] Accept expected return type (store as field or pass through context)
  - [x] Infer body with parameters in scope
  - [x] Unify body type with expected return type
  - [x] Return the function type

### Phase 5: Update Tests

- [x] Update all existing tests that use `\x -> ...` to use `: { x -> ... }`
- [x] Find all test files with lambda syntax
- [x] Replace with block arg syntax
- [x] Ensure all tests pass (1 pre-existing bug with block arg scoping in conditionals)

### Phase 6: Clean Up

- [x] Remove `Lambda` type completely (or keep with error message?)
- [x] Remove lambda-specific handling from `checkArgumentType`
- [x] Make block args more special - not passed as 'fn' arg
- [x] Update Args type in stdlib to have a special Block field
- [x] Update FunCall.Eval() to set the Block field instead of 'fn' arg
- [x] Update all stdlib functions that use block args to access the Block field
- [ ] Update `llm-notes/block-arg-syntax.md` with new semantics
- [ ] Create `llm-notes/type-inference.md` documenting the bidirectional approach
- [x] Remove or update any Lambda-related comments

## Key Implementation Details

### BlockArg.Infer() Signature

```go
func (b *BlockArg) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error)
```

But we need to pass expected types. Options:

1. **Add to context**: Store expected types in context
2. **Separate method**: `InferWithExpected(ctx, env, fresh, expectedParamTypes, expectedReturnType)`
3. **Field on BlockArg**: Set fields before calling `Infer()`

**Recommendation**: Use approach #3 - set fields on BlockArg before inference:

```go
type BlockArg struct {
    InferredTypeHolder
    Args                []*SlotDecl
    Body                Node
    ExpectedParamTypes  []hm.Type     // Set by FunCall before inference
    ExpectedReturnType  hm.Type       // Set by FunCall before inference
    Loc                 *SourceLocation
}
```

### Error Messages

When block arg type doesn't match expected:
```
block argument body has type X but expected type Y
  at block_arg.dang:5:32
  block expects to return: [Int!]!
  but body returns: Int!
```

### Evaluation

Block args evaluate to function values just like lambdas did. The `FunCall.Eval()` needs to:
1. Evaluate the block arg to a function value
2. Pass it as the "fn" argument to the function being called

## Testing Strategy

### Test Coverage Needed

1. **Basic block args** - already have `test_block_arg_basic.dang`
2. **Nested block args** - should now work! Test in `test_block_arg_nested.dang`
3. **Type mismatches** - ensure good error messages
4. **All list methods** - update existing tests
5. **Method chaining** - ensure still works
6. **Empty lists** - ensure inference works

### Test Execution

```bash
./tests/run_all_tests.sh
```

## Success Criteria

- [x] All tests pass (except 1 pre-existing bug)
- [x] No more `Lambda` syntax accepted
- [x] Block args have bidirectional inference (both params and return type)
- [x] Nested block args work correctly
- [x] Cleaner type checking code (no lambda special cases in generic code)
- [x] Good error messages for type mismatches

## Open Questions

1. **Should we keep Lambda AST node for error messages?** 
   - Could detect `\` token and give helpful error: "Lambda syntax removed, use block args: `: { x -> ... }`"

2. **What about standalone function values?**
   - Previously: `pub f = \x -> x * 2`
   - Now: Not possible? Or support block args in value position?
   - **Decision needed**: Should we support `pub f: (Int!) -> Int! = { x -> x * 2 }` (without colon)?

3. **Backward compatibility**
   - This is a breaking change
   - Should we support both syntaxes temporarily?
   - **Recommendation**: Clean break, update all code at once

## Checklist

### Phase 1: Create BlockArg AST Node
- [x] Create `BlockArg` type with fields in `ast_expressions.go`
- [x] Add `ExpectedParamTypes []hm.Type` field
- [x] Add `ExpectedReturnType hm.Type` field
- [x] Implement `DeclaredSymbols()` method
- [x] Implement `ReferencedSymbols()` method
- [x] Implement `Body()` method
- [x] Implement `GetSourceLocation()` method
- [x] Implement `Walk()` method
- [x] Implement `Infer()` method (bidirectional)
- [x] Implement `Eval()` method

### Phase 2: Update FunCall Structure
- [x] Add `BlockArg *BlockArg` field to `FunCall`
- [x] Update `ReferencedSymbols()` to include block arg
- [x] Update `Walk()` to traverse block arg
- [x] Update `Infer()` to extract expected types and set on block arg
- [x] Update `Eval()` to evaluate block arg and pass as "fn" argument

### Phase 3: Update Grammar
- [x] Remove `Lambda` from `Form` rule in `dang.peg`
- [x] Remove `Lambda` rule
- [x] Remove `LambdaArgs` and `LambdaArg` rules
- [x] Update `BlockArg` rule to return `&BlockArg{}`
- [x] Update `Call` rule to set `BlockArg` field
- [x] Update `SelectOrCall` rule to set `BlockArg` field
- [x] Run `./hack/generate`

### Phase 4: Implement Bidirectional Inference
- [x] In `FunCall.Infer()`, detect block arg presence
- [x] Extract "fn" parameter from function type
- [x] Extract parameter types from function type
- [x] Extract return type from function type
- [x] Set `ExpectedParamTypes` on block arg
- [x] Set `ExpectedReturnType` on block arg
- [x] Call block arg `Infer()`
- [x] Verify types match

### Phase 5: Update Tests
- [x] Find all test files with `\` lambda syntax
- [x] Update to use `: { ... }` block arg syntax
- [x] Run `./tests/run_all_tests.sh`
- [x] Fix any broken tests

### Phase 6: Clean Up
- [x] Remove `Lambda` type from codebase (or make it error)
- [x] Remove lambda-specific code from `checkArgumentType`
- [ ] Update `llm-notes/block-arg-syntax.md`
- [ ] Add `llm-notes/bidirectional-inference.md`
- [x] Remove Lambda-related comments
- [x] Run final test suite
