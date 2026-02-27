# Design: Unified Tree-sitter Completion

## Status

Proposal (not yet implemented)

## Problem

### Two completion systems that don't share code

The REPL and LSP both need to answer the same question — "given a
cursor position in some Dang text, what should we suggest?" — but
they do it through completely different code paths:

**REPL** (`cmd/dang/repl_completion.go`):
1. Calls `dang.CompleteInput(env, input, cursorPos)` which does
   byte-level string parsing (`splitDotExpr`, `splitArgExpr`) to
   classify the context.
2. Falls back to a static keyword/name list.
3. Has its own `splitForSuggestion` and `lastIdent` helpers that
   duplicate logic from `pkg/dang/complete.go`.

**LSP** (`pkg/lsp/handle_text_document_completion.go`):
1. Extracts the current line up to the cursor, feeds it to
   `CompleteInput` (same as REPL, but only the current line).
2. Falls back to tree-sitter parsing of the full buffer to find
   `select_or_call` nodes, extracts receiver text, feeds it *back*
   into `CompleteInput`.
3. Falls back to dumping all enclosing scope bindings.

Both share `CompleteInput` and its helpers, but the LSP wraps it in
two additional strategies and the REPL wraps it in its own fallback
chain. Bugs fixed in one path don't help the other.

### The LSP fallback chain is fragile

The three LSP strategies chain silently. When strategy 1 returns zero
results, we try strategy 2, then 3. The `*CompositeModule` cast bug
went unnoticed because the code fell through to the lexical dump. Each
strategy independently figures out "what is the user trying to
complete?" — there's no single point where the cursor context is
determined.

### String-based parsing is the wrong abstraction

`splitDotExpr` walks backwards over bytes counting parens and brackets
to find where a receiver expression starts. It doesn't understand
string escapes, comments, multi-line expressions, or block arguments.
It works only because the inputs are usually simple enough. The REPL
has the same limitation — it can't complete inside multi-line input
that spans block boundaries.

### The REPL can't resolve local variables

The REPL's `typeEnv` is flat — it has all top-level bindings from
imports plus whatever the user has defined. But if the user types a
multi-line block with `let` bindings, `CompleteInput` can't resolve
variables defined inside the block because they're not in `typeEnv`.
The LSP solved this with `buildCompletionEnv` +
`findEnclosingEnvironments`, but none of that machinery is available
to the REPL.

## Design

### Core idea

Build a single completion engine in `pkg/dang/complete.go` that both
the LSP and REPL call. The engine uses tree-sitter to classify the
cursor context (what kind of completion?) and the Dang type
environment to resolve types (what are the candidates?).

```
input text + cursor position + type env
    → tree-sitter parse
    → classify cursor context
    → resolve types from env
    → emit []Completion
```

### The shared API

```go
// pkg/dang/complete.go

// Complete returns completions for the given source text at the
// given cursor position, using env for type resolution.
//
// This is the single entry point for both LSP and REPL completion.
// It uses tree-sitter to parse the text and classify the cursor
// context, then resolves types from the provided environment.
func Complete(ctx context.Context, env Env, text string, line, col int) []Completion
```

The key difference from the current `CompleteInput` signature:

| | `CompleteInput` (current) | `Complete` (proposed) |
|---|---|---|
| Input | single line string + byte offset | full text + line/col |
| Parsing | byte-walking (`splitDotExpr`) | tree-sitter CST |
| Multi-line | no | yes |
| Local vars | only if caller merges envs | same (caller provides env) |

The callers are responsible for building the right `env`:

- **LSP**: `buildCompletionEnv(f, pos)` — merges `f.TypeEnv` with
  enclosing scope bindings from the Dang AST.
- **REPL**: `r.typeEnv` directly — the REPL env is already flat with
  all bindings in scope. (If multi-line REPL blocks eventually need
  local var completion, the REPL can adopt the same scope-merging
  approach.)

### Cursor context classification

Tree-sitter parses the full text and produces a CST. We walk it to
find the deepest node at the cursor, then classify:

```go
type CompletionContext struct {
    Kind         ContextKind
    ReceiverText string   // for DotMember: normalized receiver source
    FuncText     string   // for Argument: normalized function expression
    Partial      string   // prefix the user is typing
    ProvidedArgs []string // for Argument: already-present named args
}

type ContextKind int
const (
    ContextNone      ContextKind = iota
    ContextDotMember             // ctr.fr|  or  ctr.|
    ContextArgument              // container.from(addr|
    ContextBareIdent             // ct|  (no dot, no parens)
)
```

Classification rules based on CST node at cursor:

| CST structure | Kind | Details |
|---|---|---|
| Inside `select_or_call`, cursor on `name` field | DotMember | receiver = `left` child text, partial = `name` text |
| `ERROR` node with `dot_token` | DotMember | receiver = preceding sibling text, partial = `""` |
| Inside `arg_values` of a `call` or `select_or_call` | Argument | func = callee text, partial = current ident, provided = `key_value` keys |
| Inside `arg_values`, cursor in a `key_value` *value* position | None | user is typing a value, not an arg name |
| Bare identifier (not inside select or arg_values) | BareIdent | partial = identifier text |
| Inside `ERROR` with bare identifier | BareIdent | partial = identifier text |

The existing `findSelectAtCursor` and `findDotErrorAtCursor` already
handle the DotMember cases. New helpers needed:

```go
func findArgContextAtCursor(root, source, line, col) *argContext
func findBareIdentAtCursor(root, source, line, col) string
```

### Type resolution

Given a classified context, resolve types using the Dang env:

- **DotMember**: `InferReceiverType(ctx, env, receiverText)` to get
  the receiver's type, then `MembersOf(type, partial)` for candidates.
- **Argument**: `InferReceiverType(ctx, env, funcText)` to get the
  function type, then `ArgsOf(type, partial, providedArgs)` for
  candidates.
- **BareIdent**: `completeLexical(env, partial)` — filter env bindings
  by prefix.

All three resolution functions already exist. `InferReceiverType`
parses the receiver text with the PEG parser and runs type inference
— this is the "Option A" bridge that reuses existing infrastructure.

### REPL integration

The REPL currently calls:
```go
completions := dang.CompleteInput(r.ctx, r.typeEnv, input, cursorPos)
```

It would change to:
```go
completions := dang.Complete(r.ctx, r.typeEnv, input, line, col)
```

For the REPL, `text` is the full input buffer (which may be
multi-line if the user pressed Alt+Enter) and `line`/`col` is the
cursor position within that buffer.

The REPL's own completion provider still handles two things that are
outside the scope of the shared engine:

1. **Command completions** (`:help`, `:reset`, etc.) — checked first,
   before calling `Complete`.
2. **Static keyword list** (`let`, `if`, `true`, etc.) — used as a
   fallback when `Complete` returns nothing. This could eventually
   move into `Complete` itself as a `ContextBareIdent` enhancement,
   but it's fine as a REPL-specific layer for now.

The REPL's `splitForSuggestion` and `lastIdent` helpers become dead
code — `Complete` handles the input splitting internally via
tree-sitter. The `dangCompletions` wrapper stays, since it converts
`dang.Completion` to `tuist.Completion` with REPL-specific formatting
(display labels, insert text, sort order).

### LSP integration

The LSP handler simplifies to:

```go
func handleCompletion(ctx, req) {
    f := waitForFile(uri)
    env := buildCompletionEnv(f, pos)
    if env == nil {
        return nil
    }

    completions := dang.Complete(ctx, env, f.Text, pos.Line, pos.Character)
    return completionsToItems(completions)
}
```

The three fallback paths, `getLineUpToCursor`, `tsParseAndFindReceiver`,
and `getLexicalCompletions` all collapse into the single `Complete`
call. `buildCompletionEnv` stays in the LSP because scope merging
from the Dang AST is LSP-specific (the REPL doesn't have nested file
scopes).

### What moves where

| Current location | Proposed |
|---|---|
| `pkg/lsp/ts_completion.go` — tree-sitter parsing, CST walking | → `pkg/dang/complete_ts.go` (shared) |
| `pkg/lsp/danglang/` — CGo bindings for tree-sitter grammar | → `pkg/dang/danglang/` (shared) |
| `pkg/dang/complete.go` — `splitDotExpr`, `splitArgExpr` | Kept for `CompleteInput` backward compat; new code uses tree-sitter |
| `pkg/dang/complete.go` — `MembersOf`, `ArgsOf`, `completeLexical`, `InferReceiverType` | Unchanged — called by both old and new paths |
| `pkg/lsp/handle_text_document_completion.go` — 3 fallback paths | → single `dang.Complete` call |
| `cmd/dang/repl_completion.go` — `dang.CompleteInput` call | → `dang.Complete` call |
| `cmd/dang/repl.go` — `splitForSuggestion`, `lastIdent` | Dead code after migration |

### What doesn't change

- **PEG parser and type inference.** Still the source of truth for
  types. Tree-sitter doesn't replace the type checker.
- **`findEnclosingEnvironments` / `buildCompletionEnv`.** Still in
  `pkg/lsp/` — walks the Dang AST for scope bindings. LSP-specific.
- **Other LSP handlers.** Hover, definition, rename keep using the
  Dang AST. They could benefit from tree-sitter too, but that's a
  separate effort.
- **`CompleteInput`.** Kept alongside `Complete` for backward
  compatibility. It can be deprecated once all callers migrate, or
  kept as the REPL's simple-case fast path if desired.
- **REPL command completions and static keywords.** Stay in
  `cmd/dang/repl_completion.go` as a REPL-specific layer.

## Implementation plan

### Phase 1: Move tree-sitter code to `pkg/dang`

Move `ts_completion.go` and `danglang/` from `pkg/lsp/` to
`pkg/dang/` so both the REPL and LSP can use them. No behavior
change — just reorganizing imports.

1. Move `pkg/lsp/danglang/` → `pkg/dang/danglang/`.
2. Move tree-sitter helpers (`walkTS`, `tsContains`,
   `collapseWhitespace`, `tsNodeEquals`, `findSelectAtCursor`,
   `findDotErrorAtCursor`) to `pkg/dang/complete_ts.go`.
3. Update LSP imports. All tests pass unchanged.

### Phase 2: Add `Complete` function

Add `dang.Complete(ctx, env, text, line, col)` in
`pkg/dang/complete_ts.go`:

1. Parse `text` with tree-sitter.
2. Call `classifyCursorContext` (wraps existing `findSelectAtCursor` +
   `findDotErrorAtCursor`, plus new `findBareIdentAtCursor`).
3. Switch on context kind, call `MembersOf` / `ArgsOf` /
   `completeLexical`.
4. Write unit tests against `Complete` directly — no Neovim needed.

### Phase 3: Wire up LSP

Replace the three-path handler with a single `dang.Complete` call.
Delete `getLineUpToCursor`, `getLexicalCompletions`, and the
tree-sitter invocation from the handler. Keep `buildCompletionEnv`.

All existing Neovim LSP tests should pass unchanged.

### Phase 4: Wire up REPL

Replace `dang.CompleteInput` call in `buildCompletionProvider` with
`dang.Complete`. The REPL needs to convert its input string + cursor
byte offset into line/col — trivial since single-line input is
line=0, col=cursorPos, and multi-line input just counts newlines.

Delete `splitForSuggestion` and `lastIdent` from `cmd/dang/repl.go`.
The `dangCompletions` wrapper updates its `ReplaceFrom` calculation
to use the context info from `Complete` (or the `Completion` items
can carry replace-from info).

### Phase 5: Argument completion via tree-sitter

Add `findArgContextAtCursor` to `complete_ts.go`:

- Walk to deepest node at cursor.
- If inside `arg_values`, walk up to the enclosing `call` or
  `select_or_call`.
- Extract already-provided argument names from `key_value` children.
- Return func expression text and partial arg name.

This replaces `splitArgExpr` for both LSP and REPL. Add test cases.

### Phase 6: Deprecate `CompleteInput` (optional)

Once both LSP and REPL use `Complete`, `CompleteInput` and its string
parsing helpers (`splitDotExpr`, `splitArgExpr`) can be deprecated or
removed. They could stay for simple programmatic use cases (tests,
scripts) where constructing line/col is awkward, but the main
completion paths no longer need them.

### Phase 7: Structural type resolution (optional, future)

Replace `InferReceiverType` with CST-walking type resolution for
common node shapes:

- Bare symbol → `env.SchemeOf(name)`
- Select chain → resolve left, look up field in type's bindings
- Function call → resolve callee, return its return type

Fall back to `InferReceiverType` for complex expressions. This avoids
PEG re-parsing and handles cases where PEG chokes on partial input.

## Open questions

### Should `Complete` return replace-from info?

The REPL needs `ReplaceFrom` (byte offset where the completed token
starts) to know what text to replace. The LSP doesn't — Neovim
handles replacement via the completion item's `textEdit`. Options:

1. `Complete` returns `[]Completion` only (current). REPL computes
   `ReplaceFrom` separately using the partial and input length.
2. `Complete` returns a result struct with `Items` and `ReplaceFrom`.
   Cleaner for the REPL, ignored by the LSP.

Recommendation: option 2. It's a small addition and avoids the REPL
needing to re-derive what `Complete` already knows.

### Should the tree-sitter parser be a global or passed in?

Currently `tsParser` is a package-level `var` initialized in `init()`.
Moving it to `pkg/dang` means the tree-sitter grammar loads on import
even if completion is never used (e.g. `dang run` without REPL). This
is fine — the parser init is cheap (<1ms) and the grammar is compiled
into the binary via CGo. But if we want to be strict about it, we
could lazy-init on first `Complete` call.

### How does this interact with LSP's `buildCompletionEnv`?

`Complete` takes an `Env` and doesn't know about LSP scope merging.
The LSP is responsible for building the right env before calling
`Complete`. The REPL passes its flat `typeEnv`. This is the right
separation — scope resolution is about *what's in scope*, which
depends on the context (file with nested functions vs. REPL session),
while completion is about *what to suggest given a scope*.

## Non-goals

- **Replacing the Dang type checker.** Tree-sitter is a parser.
  Types come from PEG + HM inference.
- **Incremental tree-sitter parsing.** Full parse is sub-millisecond.
  Caching adds complexity for no measurable gain.
- **Porting hover/definition/rename to tree-sitter.** Each is a
  separate effort. Completion is the highest-impact target because it
  runs on broken mid-keystroke input.
- **Replacing `findEnclosingEnvironments`.** Scope resolution is
  semantic — the Dang AST stores inferred environments that
  tree-sitter doesn't have.
