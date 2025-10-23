# LSP Type-Aware Completion Implementation

## Goal
Implement type-aware completions in the Dang LSP so that expressions like `container.withDir<TAB>` offer completions based on the inferred type of `container` (the `Container` type from the GraphQL schema).

## Current State

### What Works
- Basic completions for:
  - Local bindings (`let hello = 42`)
  - Global functions from GraphQL schema (Query type fields)
  - Lexical bindings (function parameters)

### What's Missing
- **Type-aware completions**: When the user types `receiver.field<TAB>`, the LSP needs to know the type of `receiver` to offer completions for `field`.

### Current Flow (in `pkg/lsp/handler.go:updateFile`)
1. Parse file → AST
2. Build symbol table (definitions/references)
3. Build lexical analyzer (scoped bindings)
4. ❌ **Type inference is NOT run**

### Type Inference Flow (in `pkg/dang/eval.go`)
1. Parse → AST
2. Hoist (multi-pass for forward references)
3. **Infer** → Type information (Hindley-Milner)
4. Eval → Runtime execution

The LSP stops at step 1 (parsing), so it never gets type information.

---

## Implementation Plan

### Phase 1: Store Types on AST Nodes

**File**: `pkg/dang/ast.go`

Add methods to the `Node` interface to store/retrieve inferred types:

```go
type Node interface {
    hm.Expression
    hm.Inferer
    GetSourceLocation() *SourceLocation
    DeclaredSymbols() []string
    ReferencedSymbols() []string

    // NEW: Type annotation storage
    SetInferredType(hm.Type)
    GetInferredType() hm.Type
}
```

**Implementation approach**: Add a mixin struct that all AST nodes can embed:

```go
// InferredTypeHolder stores the inferred type for a node
type InferredTypeHolder struct {
    inferredType hm.Type
}

func (h *InferredTypeHolder) SetInferredType(t hm.Type) {
    h.inferredType = t
}

func (h *InferredTypeHolder) GetInferredType() hm.Type {
    return h.inferredType
}
```

**Action items**:
1. Add `InferredTypeHolder` struct to `pkg/dang/ast.go`
2. Update `Node` interface with `SetInferredType` and `GetInferredType` methods
3. Embed `InferredTypeHolder` in all concrete AST node types:
   - `Select` (in `ast_expressions.go`)
   - `Symbol` (in `ast_expressions.go`)
   - `FunCall` (in `ast_expressions.go`)
   - `Lambda` (in `ast_expressions.go`)
   - `List` (in `ast_literals.go`)
   - `String`, `Int`, `Boolean`, `Null` (in `ast_literals.go`)
   - All other node types in `ast_*.go` files

**Note**: This is a mechanical change - add the field to every struct that implements `Node`.

### Phase 2: Annotate Types During Inference

**File**: `pkg/dang/infer.go` and AST node `Infer()` methods

Modify the inference process to store types on nodes as they're inferred.

**Strategy**: Update each node's `Infer()` method to call `SetInferredType()` before returning:

Example for `Select.Infer()` (in `pkg/dang/ast_expressions.go`):

```go
func (d Select) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
    return WithInferErrorHandling(d, func() (hm.Type, error) {
        // ... existing inference logic ...

        // NEW: Store the inferred type on the node
        defer func() {
            if t != nil {
                d.SetInferredType(t)
            }
        }()

        // ... rest of inference logic ...
        return t, nil
    })
}
```

**Action items**:
1. Update all `Infer()` methods in `ast_expressions.go` to store their inferred type
2. Update all `Infer()` methods in `ast_literals.go` to store their inferred type
3. Update `Block.Infer()` to recursively annotate all forms
4. Test that types are being stored correctly (add logging if needed)

**Important**: Make sure to store types even for intermediate expressions, not just top-level declarations. The LSP needs to know the type of `container` in `container.withDir`, so `Symbol{Name: "container"}` needs its type annotated.

### Phase 3: Run Type Inference in LSP

**File**: `pkg/lsp/handler.go`

Modify `updateFile()` to run type inference after parsing.

**Current code** (lines 135-186):
```go
func (h *langHandler) updateFile(ctx context.Context, uri DocumentURI, text string, version *int) error {
    // ... existing code ...

    parsed, err := dang.Parse(string(uri), []byte(text))
    if err != nil {
        // Handle parse error
    } else {
        block, ok := parsed.(dang.Block)
        if !ok {
            // Handle type assertion failure
        } else {
            f.Symbols = h.buildSymbolTable(uri, block.Forms)
            f.LexicalAnalyzer = h.buildLexicalAnalyzer(uri, block.Forms)
            // ❌ No type inference here!
        }
    }
}
```

**New code**:
```go
func (h *langHandler) updateFile(ctx context.Context, uri DocumentURI, text string, version *int) error {
    // ... existing code ...

    parsed, err := dang.Parse(string(uri), []byte(text))
    if err != nil {
        // Handle parse error
    } else {
        block, ok := parsed.(dang.Block)
        if !ok {
            // Handle type assertion failure
        } else {
            f.Symbols = h.buildSymbolTable(uri, block.Forms)
            f.LexicalAnalyzer = h.buildLexicalAnalyzer(uri, block.Forms)

            // NEW: Run type inference to annotate AST with types
            if h.schema != nil {
                typeEnv := dang.NewEnv(h.schema)
                _, err := dang.Infer(ctx, typeEnv, block, true) // true = run hoisting
                if err != nil {
                    // Log the error but don't fail - we still want completions to work
                    slog.WarnContext(ctx, "type inference failed", "error", err)
                }
            }
        }
    }
}
```

**Action items**:
1. Update `updateFile()` in `pkg/lsp/handler.go` to run `dang.Infer()` after parsing
2. Handle inference errors gracefully (log but don't fail)
3. Store the parsed AST on the `File` struct so we can query it later

**New field in `File` struct** (line 38):
```go
type File struct {
    LanguageID      string
    Text            string
    Version         int
    Diagnostics     []Diagnostic
    Symbols         *SymbolTable
    LexicalAnalyzer *LexicalAnalyzer
    AST             dang.Block  // NEW: Store the parsed and type-annotated AST
}
```

### Phase 4: Find Node at Cursor Position

**New file**: `pkg/lsp/ast_query.go`

Create utilities to find AST nodes at a given cursor position.

```go
package lsp

import (
    "github.com/vito/dang/pkg/dang"
)

// FindNodeAt returns the AST node at the given position
func FindNodeAt(block dang.Block, line, col int) dang.Node {
    var found dang.Node
    walkNodes(block.Forms, func(node dang.Node) bool {
        loc := node.GetSourceLocation()
        if loc == nil {
            return true // continue
        }

        // Check if the cursor position is within this node's location
        if containsPosition(loc, line, col) {
            found = node
            return true // continue to find more specific (nested) nodes
        }

        return false // stop traversing this branch
    })
    return found
}

// containsPosition checks if a source location contains a line/column position
func containsPosition(loc *dang.SourceLocation, line, col int) bool {
    // LSP uses 0-based line/col, SourceLocation uses 1-based
    dangLine := line + 1
    dangCol := col + 1

    // For now, just check if it's on the same line
    // TODO: Handle multi-line nodes properly
    return loc.Line == dangLine
}

// walkNodes recursively walks all nodes in the AST
func walkNodes(nodes []dang.Node, fn func(dang.Node) bool) {
    for _, node := range nodes {
        if !fn(node) {
            return
        }

        // Recursively walk nested nodes
        switch n := node.(type) {
        case dang.Block:
            walkNodes(n.Forms, fn)
        case dang.Select:
            if n.Receiver != nil {
                walkNodes([]dang.Node{n.Receiver}, fn)
            }
        case dang.FunCall:
            walkNodes([]dang.Node{n.Fun}, fn)
            for _, arg := range n.Args {
                walkNodes([]dang.Node{arg.Value}, fn)
            }
        case *dang.Lambda:
            walkNodes([]dang.Node{n.FunctionBase.Body}, fn)
        case *dang.ClassDecl:
            walkNodes(n.Value.Forms, fn)
        case dang.SlotDecl:
            if n.Value != nil {
                walkNodes([]dang.Node{n.Value}, fn)
            }
        // Add more cases as needed
        }
    }
}

// FindReceiverAt finds the receiver expression for a Select node at the cursor
// For "container.withDir", when cursor is after ".", return the "container" Symbol node
func FindReceiverAt(block dang.Block, line, col int) dang.Node {
    node := FindNodeAt(block, line, col)
    if node == nil {
        return nil
    }

    // If we found a Select node, return its Receiver
    if sel, ok := node.(dang.Select); ok {
        return sel.Receiver
    }

    return nil
}
```

**Action items**:
1. Create `pkg/lsp/ast_query.go` with the above utilities
2. Test with different cursor positions to ensure accuracy
3. Handle edge cases (multi-line nodes, nested expressions, etc.)

### Phase 5: Type-Aware Completions

**File**: `pkg/lsp/handle_text_document_completion.go`

Enhance the completion handler to offer type-aware completions.

**Current code** (lines 12-62):
```go
func (h *langHandler) handleTextDocumentCompletion(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
    // ... existing code ...

    var items []CompletionItem

    // Add all defined symbols
    for name, info := range f.Symbols.Definitions {
        items = append(items, CompletionItem{...})
    }

    // Add lexical bindings
    // ... existing code ...

    // Add global functions
    items = append(items, h.getSchemaCompletions()...)

    return items, nil
}
```

**New code**:
```go
func (h *langHandler) handleTextDocumentCompletion(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
    // ... existing code ...

    var items []CompletionItem

    // NEW: Check if we're completing after a "." (member access)
    if h.isAfterDot(f, params.Position) {
        // Find the receiver expression before the dot
        receiver := FindReceiverAt(f.AST, params.Position.Line, params.Position.Character)
        if receiver != nil {
            // Get the inferred type of the receiver
            receiverType := receiver.GetInferredType()
            if receiverType != nil {
                // Offer completions for this type's members
                items = h.getMemberCompletions(receiverType)
                return items, nil
            }
        }
    }

    // Fall back to existing completions
    for name, info := range f.Symbols.Definitions {
        items = append(items, CompletionItem{...})
    }

    // ... rest of existing code ...

    return items, nil
}

// isAfterDot checks if the cursor is immediately after a "."
func (h *langHandler) isAfterDot(f *File, pos Position) bool {
    lines := strings.Split(f.Text, "\n")
    if pos.Line >= len(lines) {
        return false
    }

    line := lines[pos.Line]
    if pos.Character == 0 {
        return false
    }

    // Check if the previous character is a "."
    return line[pos.Character-1] == '.'
}

// getMemberCompletions returns completion items for a type's members
func (h *langHandler) getMemberCompletions(t hm.Type) []CompletionItem {
    var items []CompletionItem

    // Unwrap NonNullType if needed
    if nn, ok := t.(hm.NonNullType); ok {
        t = nn.Type
    }

    // Check if the type is an Env (object/module type)
    env, ok := t.(dang.Env)
    if !ok {
        return items
    }

    // Iterate over all members of the type
    for name, scheme := range env.Bindings(dang.PublicVisibility) {
        memberType, _ := scheme.Type()

        // Determine completion kind based on member type
        kind := VariableCompletion
        if _, isFn := memberType.(*hm.FunctionType); isFn {
            kind = MethodCompletion
        }

        items = append(items, CompletionItem{
            Label:  name,
            Kind:   kind,
            Detail: memberType.String(),
        })
    }

    return items
}
```

**Action items**:
1. Add `isAfterDot()` helper to detect member access context
2. Integrate `FindReceiverAt()` to get the receiver node
3. Add `getMemberCompletions()` to offer type members
4. Test with various scenarios:
   - `container.withDir<TAB>` → Container methods
   - `directory.entries<TAB>` → Directory methods
   - Nested: `container.withDirectory("/", directory).with<TAB>` → Container methods

### Phase 6: Add Test Case

**File**: `pkg/lsp/testdata/complete.dang`

Add a test case for type-aware completions:

```dang
# local bindings
let hello = 42

[] # test: ahel<C-x><C-o> => [hello┃]

# global functions (from Query type)
[] # test: adir<C-x><C-o> => [directory┃]
[] # test: acont<C-x><C-o> => [container┃]

# lexical bindings
pub foo(jxkqv: Int!): [Int!] {
  [] # test: ^ajx<C-x><C-o> => [jxkqv┃]
}

# NEW: type-aware completions
pub bar: Container! {
  let c = container
  [] # test: ac.with<C-x><C-o> => [c.withDirectory┃]
}
```

**Action items**:
1. Add test case for member access completion
2. Run tests with `testLsp` tool
3. Fix any issues that arise

---

## Testing Strategy

### Manual Testing
1. Start the LSP server
2. Open a `.dang` file in an editor with LSP support
3. Type `container.with` and trigger completion (Ctrl+Space or similar)
4. Verify that Container methods appear in the completion list

### Automated Testing
1. Run `testLsp -filter=Completion`
2. Verify all existing tests still pass
3. Verify new type-aware completion test passes

### Edge Cases to Test
1. **Nullable types**: `let x: Container = null; x.with<TAB>` → Should still offer completions (the type is Container, even if the value might be null)
2. **Chained calls**: `container.withDirectory("/", directory).with<TAB>` → Should infer the return type of `withDirectory`
3. **Function calls**: `container().with<TAB>` → Should infer the return type of the function call
4. **Parse errors**: If the file has syntax errors, completions should still work for the valid parts
5. **Inference errors**: If type inference fails, fall back to basic completions

---

## Performance Considerations

### Current Approach (Simple)
- Run full type inference on every file change
- Store types on AST nodes
- Query types when needed for completions

**Pros**: Simple, correct, easy to implement
**Cons**: Could be slow for large files

### Future Optimizations (if needed)
1. **Debouncing**: Only run inference 500ms after the last keystroke
2. **Incremental inference**: Only re-infer changed parts of the AST
3. **Caching**: Cache inference results per file version
4. **Lazy inference**: Only infer types when completions are requested

**For now, start with the simple approach.** Optimize only if performance becomes an issue.

---

## Success Criteria

✅ `container.withDir<TAB>` offers completions for Container type methods
✅ Completions show the correct method names from the GraphQL schema
✅ Completions work for nested expressions (e.g., `container.withDirectory("/", dir).with<TAB>`)
✅ All existing completion tests still pass
✅ New test case for type-aware completions passes

---

## Notes

- **Don't break existing functionality**: Make sure basic completions (symbols, lexical bindings, global functions) still work
- **Graceful degradation**: If type inference fails, fall back to basic completions
- **Follow existing patterns**: Use the same coding style and patterns as the existing LSP code
- **Test incrementally**: Test each phase before moving to the next one

---

## Implementation Order

Update the checkboxes below in ./current-task.md as you complete each phase:

1. [x] **Phase 1**: Add type storage to AST nodes (mechanical change)
2. [x] **Phase 2**: Annotate types during inference (update Infer() methods)
3. [x] **Phase 3**: Run inference in LSP (integrate with updateFile)
4. [x] **Phase 4**: Find nodes at cursor (AST query utilities)
5. [x] **Phase 5**: Type-aware completions (enhance completion handler)
6. [x] **Phase 6**: Add test case (verify it works)

## Final Status

✅ **COMPLETE** - All phases implemented and tested successfully!

### Key Implementation Details

1. **Type storage on AST nodes**: Added `InferredTypeHolder` mixin that all nodes embed
2. **Type annotation during inference**: Each `Infer()` method stores its result on the node
3. **LSP integration**: `updateFile()` runs type inference after parsing
4. **AST querying**: `FindReceiverAt()` finds receiver nodes and their types at cursor position
5. **Type-aware completions**: Completion handler checks for member access and offers type-specific completions
6. **Chained completions**: Test framework supports `{delay:Nms}` markers to allow LSP re-parsing between completions

### Test Results

All 8 completion tests pass:
- ✅ Local bindings (`hello`)
- ✅ Global functions (`directory`, `container`)
- ✅ Lexical bindings (`jxkqv`)
- ✅ Type-aware member access (`container.from`, `container.withDirectory`)
- ✅ **Chained type-aware completions** (`git(url).head.tree`)

The chained completion test required a 200ms delay between completions to allow the LSP to re-parse and re-infer the file after the first completion inserts text.

