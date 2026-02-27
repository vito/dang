# Design: Tree-sitter-first LSP Completion

## Status

Proposal (not yet implemented)

## Problem

The LSP completion handler uses three fallback strategies chained
together:

1. **Text-based**: extract the current line up to the cursor, feed it
   to `CompleteInput` which does its own string parsing (`splitDotExpr`,
   `splitArgExpr`) to figure out what kind of completion is needed.
2. **Tree-sitter**: parse the full buffer, walk the CST to find a
   `select_or_call` or `ERROR` node at the cursor, extract receiver
   text as a string, then feed *that* string back into `CompleteInput`
   (which re-parses it with the PEG parser and re-runs type inference).
3. **Lexical**: dump all enclosing scope bindings as completion items.

This has several problems:

- **Redundant parsing.** Tree-sitter produces a full syntax tree, but
  we throw it away and re-parse the extracted text with PEG.
- **Silent fallthrough.** When a strategy returns zero results, we try
  the next one with no signal about *why* it failed. The
  `*CompositeModule` cast bug went unnoticed because the code fell
  through to the lexical dump.
- **Text-based parsing is fragile.** `splitDotExpr` walks backwards
  over bytes counting parens and brackets. It doesn't understand
  strings with escapes, comments, or multi-line expressions. It works
  only because the line-extraction step usually gives it something
  simple enough.
- **Scope resolution is duplicated.** `buildCompletionEnv` (for
  tree-sitter path) and `getLexicalCompletions` (for fallback) both
  call `findEnclosingEnvironments` and merge scopes. The text-based
  path uses `f.TypeEnv` alone and can't resolve local variables at all.
- **No unified cursor context.** Each strategy independently figures
  out "what is the user trying to complete?" — dot member, argument
  name, bare identifier. This should be determined once.

## Design

Replace the three fallback strategies with a single pipeline:

```
tree-sitter parse → classify cursor context → resolve types → emit completions
```

### Step 1: Parse with tree-sitter

Tree-sitter parses the full buffer on every completion request. This is
fast (<1ms for typical files) and handles broken/partial input
gracefully. The result is a concrete syntax tree (CST).

We already do this in the tree-sitter strategy. The change is making it
the *only* strategy — not a fallback.

### Step 2: Classify the cursor context

Walk the CST to find the deepest node containing the cursor, then
classify the completion context. This replaces all of `splitDotExpr`,
`splitArgExpr`, and the line-extraction logic.

```go
type CompletionContext struct {
    Kind     ContextKind
    Receiver *ReceiverInfo  // non-nil for DotMember
    FuncExpr *FuncExprInfo  // non-nil for Argument
    Partial  string         // prefix being typed
}

type ContextKind int
const (
    ContextNone      ContextKind = iota
    ContextDotMember             // ctr.fr|  or  ctr.|
    ContextArgument              // container.from(addr|
    ContextBareIdent             // ctr|  (no dot)
)
```

Classification rules, based on the CST node at cursor:

| CST node at cursor | Context | Receiver/FuncExpr | Partial |
|---|---|---|---|
| Inside `select_or_call`, cursor on `name` field | DotMember | `left` child | text of `name` |
| Inside `ERROR` with `dot_token`, cursor after dot | DotMember | preceding sibling | `""` |
| Inside `arg_values`, cursor on `key_value` key or loose identifier | Argument | walk up to enclosing `call`/`select_or_call` | text of identifier |
| Inside `arg_values`, cursor on `key_value` value | None | — | — |
| Bare identifier not inside a select or call | BareIdent | — | text of identifier |
| Inside `ERROR` with bare identifier (e.g. partial `let x = ct`) | BareIdent | — | text of identifier |

The `findSelectAtCursor` and `findDotErrorAtCursor` functions we
already have cover the DotMember case. Argument and BareIdent are new.

### Step 3: Resolve types from the Dang env

Given a `CompletionContext`, resolve types using the Dang type
environment (which comes from the PEG parser's inference pass — we
are *not* replacing the type checker).

```go
func resolveReceiver(ctx CompletionContext, env dang.Env) hm.Type
```

For DotMember, the receiver is a CST node. We need its type. There are
two approaches:

**Option A: Extract text, re-parse, re-infer (current approach).**
Take the CST node's text, collapse whitespace, feed to
`InferReceiverType`. This round-trips through PEG parsing but reuses
existing infrastructure.

**Option B: Walk the CST structurally to resolve types.**
For a select chain `a.b(x).c`, resolve `a` by name lookup in the env,
then `b` as a member of `a`'s type, then apply the function call to
get the return type, then `c` as a member of that. This avoids
re-parsing entirely but requires reimplementing a subset of type
resolution against CST nodes.

**Recommendation: Start with Option A, migrate to Option B later.**
Option A is a straightforward refactor of what we have. Option B is a
deeper change that would pay off when we need to handle cases where
the PEG parser chokes on partial input (e.g. `container.from("alpine").|`
where the trailing dot makes PEG parsing fail). But Option B is
substantially more work and can be done incrementally per-node-type.

For Argument context, resolve the function expression's type the same
way, then call `ArgsOf` (already exists).

For BareIdent, no type resolution needed — just filter the env
bindings by prefix (already exists as `completeLexical`).

### Step 4: Emit completions

Given the resolved type and context kind, call `MembersOf`, `ArgsOf`,
or `completeLexical` to produce completion items. These functions
already exist in `pkg/dang/complete.go` and don't need to change.

### Scope resolution

`buildCompletionEnv` already handles merging file-level TypeEnv with
enclosing scope bindings from the Dang AST. This stays. The only
change: it gets called once at the top of the handler, not inside a
conditional branch.

The shadowing bug (outermost scope wins) should be fixed: iterate
enclosing envs from outermost to innermost so that inner `Add` calls
overwrite outer ones. Currently the iteration order from
`findEnclosingEnvironments` is already outermost-first, and `Add`
overwrites, so this is actually correct by accident. Add a comment.

### What changes

| Current | Proposed |
|---|---|
| `getLineUpToCursor` + `CompleteInput` | Removed — tree-sitter handles this |
| `splitDotExpr` (byte-walking) | Replaced by CST node classification |
| `splitArgExpr` (byte-walking) | Replaced by CST node classification |
| `tsParseAndFindReceiver` | Generalized to `classifyCursorContext` |
| `findSelectAtCursor` | Kept, used inside `classifyCursorContext` |
| `findDotErrorAtCursor` | Kept, used inside `classifyCursorContext` |
| 3 fallback paths in handler | 1 path: classify → resolve → complete |
| `buildCompletionEnv` called conditionally | Called once at top |
| `getLexicalCompletions` (separate code path) | Folded into main path as BareIdent case |
| `CompleteInput` | Still used by REPL; LSP no longer calls it |

### What doesn't change

- **`pkg/dang/complete.go`**: `MembersOf`, `ArgsOf`,
  `completeLexical`, `InferReceiverType` all stay. They're also used
  by the REPL. `CompleteInput` and `splitDotExpr`/`splitArgExpr` stay
  for REPL use but the LSP stops calling them.
- **PEG parser and type inference**: Still the source of truth for
  types. Tree-sitter doesn't replace the type checker.
- **`findEnclosingEnvironments`**: Still walks the Dang AST to find
  scope bindings. Could eventually be replaced by tree-sitter scope
  analysis, but that's a separate project.
- **Other LSP handlers**: Hover, definition, rename continue using the
  Dang AST. They could benefit from tree-sitter too (especially hover,
  which has its own `findNodeAtPosition`), but that's out of scope.

### New CST helpers needed

```go
// findArgContextAtCursor returns argument completion info when the
// cursor is inside a function call's argument list.
func findArgContextAtCursor(root, source, line, col) *ArgContext

// findBareIdentAtCursor returns the partial identifier at cursor
// when not inside a dot expression or argument list.
func findBareIdentAtCursor(root, source, line, col) string
```

### Handler pseudocode

```go
func handleCompletion(ctx, req) {
    f := waitForFile(uri)
    env := buildCompletionEnv(f, pos)

    cc := classifyCursorContext(f.Text, pos.Line, pos.Character)

    switch cc.Kind {
    case ContextDotMember:
        receiverText := collapseWhitespace(cc.Receiver.Text)
        input := receiverText + "." + cc.Partial
        return completionsFromInput(ctx, env, input)

    case ContextArgument:
        funcText := collapseWhitespace(cc.FuncExpr.Text)
        t := InferReceiverType(ctx, env, funcText)
        return ArgsOf(t, cc.Partial, cc.ProvidedArgs)

    case ContextBareIdent:
        return completeLexical(env, cc.Partial)

    default:
        return nil
    }
}
```

## Implementation plan

### Phase 1: Unify the handler (small, safe)

Merge the three paths into one. Use tree-sitter for cursor
classification, but keep `InferReceiverType` (Option A) for type
resolution. This is mostly rearranging existing code.

1. Move `buildCompletionEnv` to the top of the handler.
2. Add `classifyCursorContext` that wraps the existing
   `findSelectAtCursor` and `findDotErrorAtCursor`, plus new
   `findArgContextAtCursor` and `findBareIdentAtCursor`.
3. Replace the three-path fallback with a single switch on context
   kind.
4. Delete `getLineUpToCursor` from the handler (it's trivial to
   re-add if the REPL needs it).
5. Run all existing LSP tests — they should pass without changes.

Estimated size: ~100 lines changed in `handle_text_document_completion.go`,
~80 lines added to `ts_completion.go`.

### Phase 2: Argument completion via tree-sitter

Add `findArgContextAtCursor`:

- Walk to deepest node at cursor.
- If inside `arg_values`, walk up to find the enclosing `call` or
  `select_or_call`.
- Extract already-provided argument names by iterating `key_value`
  children.
- Return the function expression node's text and the partial arg name.

This replaces `splitArgExpr` for the LSP. Add test cases for argument
completion in `complete.dang`.

### Phase 3: Structural type resolution (Option B, optional)

Replace `InferReceiverType` with CST-walking type resolution for
common cases:

- Bare symbol: `env.SchemeOf(name)`
- Select chain: resolve left, look up field in type's bindings
- Function call: resolve callee, return the function's return type

Fall back to `InferReceiverType` for complex expressions (literals,
string interpolation, etc.). This is a performance win and handles
cases where PEG re-parsing fails on partial input, but it's not
required for correctness.

### Non-goals

- **Replacing the type checker with tree-sitter.** Tree-sitter is a
  parser, not a type system. The Dang PEG parser + HM inference
  remains the source of truth for types.
- **Incremental tree-sitter parsing.** We could cache the tree and
  use `edit` + incremental re-parse, but the full parse is already
  sub-millisecond. Not worth the complexity yet.
- **Porting other handlers to tree-sitter.** Hover, definition, and
  rename could benefit, but each is a separate effort. Completion is
  the most impactful because it runs on broken input (the user is
  mid-keystroke), which is exactly where tree-sitter shines.
- **Replacing `findEnclosingEnvironments`.** This walks the Dang AST
  to find scope bindings. Tree-sitter could do scope analysis, but
  scopes in Dang are semantic (type inference creates them), not
  purely syntactic. The Dang AST stores the inferred environments;
  tree-sitter doesn't have this information.
