# LSP Completion Support Improvement Plan

## Executive Summary

The current completion implementation works for basic scenarios (as seen in tests) but fails in real-world usage (Zed) because it doesn't properly communicate with clients about filtering, context, and when to request new completions. The implementation needs to be upgraded from a minimal proof-of-concept to a spec-compliant system.

## Current State Analysis

### What Works ✓
- Basic `textDocument/completion` handler exists
- Trigger character support (`.` for member access)
- Type-aware member completions (e.g., `container.from`)
- Local symbol completions (file-level definitions)
- Lexical bindings (function parameters)
- Global functions from GraphQL schema (Query type fields)
- Returns `CompletionItem[]` with `label` and `kind`

### Critical Problems ✗

1. **No completion list metadata** - Returns raw array instead of `CompletionList`
   - Missing `isIncomplete` flag → clients don't know when to re-request
   - **Root cause of Zed issue**: Client caches first response and never asks again

2. **Missing context awareness** - Ignores `CompletionContext` entirely
   - Can't distinguish manual invoke vs trigger character vs incomplete follow-up
   - Can't adapt behavior based on trigger type

3. **No client-side filtering support** - Missing `filterText` and `sortText`
   - Members like `container.from` appear even when typing `foo.`
   - No way to guide client filtering behavior
   - **Root cause of poor contextualization**

4. **No text replacement** - Missing `textEdit` and `insertText`
   - Can't replace partial input (e.g., `cont` → `container`)
   - Only appends, never replaces
   - Poor UX when completing partial words

5. **Bug: Wrong JSON field name** - `CompletionContext` field is `"contentChanges"` instead of `"context"`
   - Params never parse correctly
   - Context information is always missing

6. **No lazy resolution** - Missing `completionItem/resolve` handler
   - Could defer expensive documentation lookups
   - Minor but impacts performance with large schemas

7. **No streaming** - No partial result support
   - All-or-nothing responses
   - Minor but impacts responsiveness

## Improvement Plan

### Phase 1: Core Fixes (Critical - Unblocks Zed)

**Goal**: Make completion work correctly in real-world editors by implementing proper filtering and incremental behavior.

#### 1.1 Fix CompletionContext Parsing
- **File**: `pkg/lsp/lsp.go`
- Change `CompletionContext` field tag from `"contentChanges"` to `"context"`
- Add test to verify context parsing

#### 1.2 Implement CompletionList Response
- **File**: `pkg/lsp/lsp.go`
- Add `CompletionList` struct:
  ```go
  type CompletionList struct {
      IsIncomplete bool              `json:"isIncomplete"`
      Items        []CompletionItem  `json:"items"`
  }
  ```
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Change return type from `[]CompletionItem` to `CompletionList`
- Set `isIncomplete: false` for now (enables client-side filtering)

#### 1.3 Add FilterText Support
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Determine what user has typed before cursor
  - Parse line up to cursor position
  - Extract partial identifier (word boundaries)
- Set `filterText` on all items to match what should be filtered
  - For members: `filterText = partialMember` (not the full path)
  - For globals: `filterText = label`
- This enables proper client-side filtering

#### 1.4 Add TextEdit Support
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Calculate the range to replace:
  - Start: beginning of current word/identifier
  - End: cursor position
- Set `textEdit` with:
  - Range: calculated replacement range
  - NewText: the completion text
- Remove `label` from inserted text (use for display only)

**Expected Outcome**: Completions update as user types, properly filtered and contextualized.

---

### Phase 2: Context-Aware Behavior (Important)

**Goal**: Adapt completion behavior based on how it was triggered.

#### 2.1 Parse and Use CompletionContext
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Add logic to check `params.CompletionContext.TriggerKind`:
  ```go
  const (
      Invoked                          = 1 // Ctrl+Space or manual invoke
      TriggerCharacter                 = 2 // Typed `.` or `(`
      TriggerForIncompleteCompletions  = 3 // Follow-up after isIncomplete
  )
  ```

#### 2.2 Optimize for Trigger Characters
- When `triggerKind == TriggerCharacter` and `triggerCharacter == "."`:
  - **Only** return member completions
  - Skip global functions and local symbols
  - Return empty list if not after a receiver
- Improves performance and reduces noise

#### 2.3 Optimize for Manual Invocation
- When `triggerKind == Invoked`:
  - Return full completion set (members + globals + locals)
  - More expensive but expected for manual triggers

**Expected Outcome**: Faster, more relevant completions based on context.

---

### Phase 3: Advanced Features (Nice-to-Have)

**Goal**: Add polish and performance optimizations.

#### 3.1 Implement completionItem/resolve
- **File**: `pkg/lsp/lsp.go`
- Add `CompletionProvider.ResolveProvider: true` to capabilities
- **File**: `pkg/lsp/handle_completion_item_resolve.go` (new)
- Implement handler to lazily load:
  - `documentation` (GraphQL field descriptions)
  - `detail` (full type signatures)
- Store minimal data in `CompletionItem.Data` to identify what to resolve

#### 3.2 Add SortText for Better Ordering
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Add `sortText` to control display order:
  - Exact prefix matches first
  - Fuzzy matches later
  - Deprecated items last
- Format: `"0"` (highest priority) to `"9"` (lowest)

#### 3.3 Incremental Completions (isIncomplete)
- **File**: `pkg/lsp/handle_text_document_completion.go`
- Detect expensive scenarios:
  - Very large schemas (>1000 fields)
  - Complex type inference in progress
- Return `isIncomplete: true` to force re-requests
- Handle `TriggerForIncompleteCompletions` to refine results

#### 3.4 Snippet Support
- **File**: `pkg/lsp/handle_text_document_completion.go`
- For functions, add `insertTextFormat: Snippet`
- Include placeholders for parameters:
  - `container.from($1)$0`
- Check client capability `completionItem.snippetSupport` first

#### 3.5 Additional Completion Sources
- Import completions (when typing `import `)
- Type name completions (when declaring variables)
- Field name completions (when in object literals)
- String literal completions (for enum values)

**Expected Outcome**: Professional-grade completion experience comparable to TypeScript/Rust LSPs.

---

### Phase 4: Testing & Validation

**Goal**: Ensure robustness across different editors and scenarios.

#### 4.1 Expand Test Coverage
- **File**: `pkg/lsp/testdata/complete.dang`
- Add test cases for:
  - Partial word completion (`cont` → `container`)
  - Multiple triggers in sequence (`foo.bar.baz`)
  - Completion after non-trigger typing
  - Empty completion contexts
  - Invalid positions

#### 4.2 Manual Editor Testing
- Test in Neovim (already working)
- Test in VS Code
- Test in Zed (primary target)
- Document any editor-specific quirks

#### 4.3 Performance Testing
- Benchmark large schemas (>5000 fields)
- Profile completion latency
- Optimize hot paths if needed

---

## Implementation Order

### Week 1: Unblock Zed
1. Phase 1.1: Fix context parsing (1 hour)
2. Phase 1.2: CompletionList response (2 hours)
3. Phase 1.3: FilterText support (4 hours)
4. Phase 1.4: TextEdit support (4 hours)
5. Testing in Zed (2 hours)

### Week 2: Polish
6. Phase 2.1-2.3: Context-aware behavior (6 hours)
7. Phase 3.2: SortText (2 hours)
8. Phase 4.1: Expand tests (4 hours)

### Future (Optional)
9. Phase 3.1: Lazy resolve (8 hours)
10. Phase 3.3: Incremental completions (4 hours)
11. Phase 3.4: Snippet support (6 hours)
12. Phase 3.5: Additional sources (varies)

---

## Success Criteria

### Minimum Viable (Phase 1 Complete)
- [ ] Zed shows completions that update as you type
- [ ] Typing `container.f` filters to fields starting with `f`
- [ ] Completing `cont` replaces it with `container` (not `contcontainer`)
- [ ] All existing tests still pass

### Good (Phase 2 Complete)
- [ ] `.` only shows members, not globals
- [ ] Manual invoke shows everything
- [ ] Completions appear within 100ms
- [ ] No spurious re-requests

### Excellent (Phase 3 Complete)
- [ ] Documentation appears on selection
- [ ] Smart ordering (exact matches first)
- [ ] Snippet support for functions
- [ ] Works in VS Code, Neovim, and Zed

---

## Technical Notes

### LSP Spec References
- **Completion Request**: `textDocument/completion`
- **Resolve Request**: `completionItem/resolve`
- **Spec Sections**:
  - `types/CompletionParams.md`
  - `types/CompletionItem.md`
  - `types/CompletionList.md`
  - `types/partialResults.md`

### Client Filtering Behavior
Per LSP spec: "For speed, clients should be able to filter an already received completion list if the user continues typing."

**Two modes:**
1. **Client-side (default)**: Return `isIncomplete: false`, client filters locally
2. **Server-side (opt-in)**: Return `isIncomplete: true`, client re-requests

We should use mode 1 (client-side) for best performance.

### TextEdit vs InsertText
- **insertText**: Simple string insertion at cursor
  - Client guesses word boundaries
  - Good for append-only scenarios

- **textEdit**: Precise range replacement
  - Server controls exact behavior
  - Required for proper word completion
  - Always preferred when available

### FilterText Semantics
- Used by client to filter items as user types
- Should match the **actual text being typed**, not the full completion
- Example: For `container.from` after typing `cont`, filterText should be `cont`, not `container.from`

---

## Risks & Mitigations

### Risk: Breaking Existing Tests
- **Mitigation**: Run test suite after each change
- **Rollback**: Keep phases small and atomic

### Risk: Editor-Specific Quirks
- **Mitigation**: Test in multiple editors early
- **Fallback**: Add editor detection if needed (not ideal)

### Risk: Performance Regression
- **Mitigation**: Benchmark before/after
- **Optimization**: Use lazy resolve for expensive operations

### Risk: Incomplete Context Information
- **Mitigation**: Gracefully degrade when context is missing
- **Default**: Assume `Invoked` if context is absent

---

## Open Questions

1. **Should we implement trigger on `(`?**
   - Pros: Could show parameter hints
   - Cons: Not standard for completion (use `signatureHelp` instead)
   - **Decision**: Not for now, Phase 3.5 if needed

2. **How to handle incomplete type inference?**
   - If receiver type isn't inferred yet, what to return?
   - **Decision**: Return empty list, set `isIncomplete: true`

3. **Should completions include non-exported fields?**
   - Only show public API vs show everything
   - **Decision**: Only public (current behavior is correct)

4. **Support for fuzzy matching?**
   - Allow `ctfr` to match `container.from`
   - **Decision**: Phase 3.5, not critical

---

## References

- [LSP Specification - Completion](pkg/lsp/spec/specification.md)
- [Current Implementation](pkg/lsp/handle_text_document_completion.go)
- [Test Cases](pkg/lsp/testdata/complete.dang)
- [Zed Issue](Original user report about Zed barely working)

---

## Implementation Checklist

Use this checklist to track progress. Mark items as `[x]` when complete.

### Phase 1: Core Fixes (Critical) 🔴

**1.1 Fix CompletionContext Parsing**
- [ ] Change JSON tag in `pkg/lsp/lsp.go` line 142 from `"contentChanges"` to `"context"`
- [ ] Verify params.CompletionContext is parsed correctly
- [ ] Run existing tests to ensure no breakage

**1.2 Implement CompletionList Response**
- [ ] Add `CompletionList` struct to `pkg/lsp/lsp.go`:
  ```go
  type CompletionList struct {
      IsIncomplete bool             `json:"isIncomplete"`
      Items        []CompletionItem `json:"items"`
  }
  ```
- [ ] Update `handleTextDocumentCompletion` return type from `[]CompletionItem` to `CompletionList`
- [ ] Wrap all return statements: `return CompletionList{IsIncomplete: false, Items: items}, nil`
- [ ] Test that response structure is correct

**1.3 Add FilterText Support**
- [ ] Extract text before cursor in `handleTextDocumentCompletion`
- [ ] Parse current line up to cursor position
- [ ] Identify partial word/identifier being typed
- [ ] Set `filterText` on all completion items:
  - Members: just the member name (not receiver)
  - Globals: the function name
  - Locals: the variable name
- [ ] Test filtering behavior in editor

**1.4 Add TextEdit Support**
- [ ] Calculate word start position (scan backward from cursor for word boundary)
- [ ] Create Range for replacement: `{Start: wordStart, End: cursorPos}`
- [ ] Set `textEdit` on all completion items with:
  - Range: calculated range
  - NewText: the completion text
- [ ] Test that partial words are replaced correctly (e.g., `cont` → `container`)

**Phase 1 Validation**
- [ ] Test in Zed: completions update as you type
- [ ] Test filtering: `container.f` only shows members starting with `f`
- [ ] Test replacement: `cont<complete>` produces `container`, not `contcontainer`
- [ ] All existing tests pass: `make test-lsp`

---

### Phase 2: Context-Aware Behavior (Important) 🟡

**2.1 Parse and Use CompletionContext**
- [ ] Define constants for trigger kinds at top of handler:
  ```go
  const (
      CompletionTriggerKindInvoked = 1
      CompletionTriggerKindTriggerCharacter = 2
      CompletionTriggerKindTriggerForIncompleteCompletions = 3
  )
  ```
- [ ] Add logic to branch on `params.CompletionContext.TriggerKind`

**2.2 Optimize for Trigger Characters**
- [ ] When `triggerKind == TriggerCharacter` and `triggerCharacter == "."`:
  - [ ] Skip adding global functions
  - [ ] Skip adding local symbols
  - [ ] Skip adding lexical bindings
  - [ ] Only return member completions
- [ ] Return empty list if no receiver found

**2.3 Optimize for Manual Invocation**
- [ ] When `triggerKind == Invoked` or missing context:
  - [ ] Return all completion sources (members + globals + locals + lexical)
- [ ] Ensure backward compatibility for editors that don't send context

**Phase 2 Validation**
- [ ] Test in Zed: typing `.` only shows members
- [ ] Test in Zed: Ctrl+Space shows everything
- [ ] Measure completion latency (should be <100ms)
- [ ] All existing tests still pass

---

### Phase 3: Advanced Features (Nice-to-Have) 🟢

**3.1 Implement completionItem/resolve**
- [ ] Add `ResolveProvider: true` to CompletionProvider in `handle_initialize.go`
- [ ] Create `pkg/lsp/handle_completion_item_resolve.go`
- [ ] Implement handler registration in `handler.go`
- [ ] Add `completionItem/resolve` case in request handler
- [ ] Store item identifier in `CompletionItem.Data`
- [ ] Lazily load documentation and detail fields
- [ ] Test that docs appear on item selection

**3.2 Add SortText for Better Ordering**
- [ ] Calculate sort priority based on:
  - Exact prefix match: `"0"`
  - Prefix match: `"1"`
  - Contains match: `"2"`
  - Other: `"3"`
- [ ] Set `sortText` on all completion items
- [ ] Test that exact matches appear first

**3.3 Incremental Completions (isIncomplete)**
- [ ] Detect expensive scenarios (large schemas, inference in progress)
- [ ] Return `isIncomplete: true` when appropriate
- [ ] Handle `TriggerForIncompleteCompletions` trigger kind
- [ ] Test that completions refine as you type

**3.4 Snippet Support**
- [ ] Check client capability `completionItem.snippetSupport`
- [ ] For function completions, set `insertTextFormat: SnippetTextFormat`
- [ ] Generate snippet text with placeholders: `functionName($1)$0`
- [ ] Test snippet expansion in supporting editors

**3.5 Additional Completion Sources**
- [ ] Import path completions
- [ ] Type name completions
- [ ] Field name completions
- [ ] Enum value completions

---

### Phase 4: Testing & Validation 🔵

**4.1 Expand Test Coverage**
- [ ] Add test case: partial word completion (`cont` → `container`)
- [ ] Add test case: multiple trigger sequence (`foo.bar.baz`)
- [ ] Add test case: completion after non-trigger typing
- [ ] Add test case: empty/missing context
- [ ] Add test case: invalid cursor positions

**4.2 Manual Editor Testing**
- [ ] Test full workflow in Neovim
- [ ] Test full workflow in VS Code
- [ ] Test full workflow in Zed
- [ ] Document any editor-specific quirks found

**4.3 Performance Testing**
- [ ] Benchmark completion with small schema (<100 fields)
- [ ] Benchmark completion with large schema (>1000 fields)
- [ ] Profile hot paths
- [ ] Optimize if latency >100ms

---

## Progress Summary

- **Phase 1**: ☐ Not Started / ◐ In Progress / ☑ Complete
- **Phase 2**: ☐ Not Started / ◐ In Progress / ☑ Complete
- **Phase 3**: ☐ Not Started / ◐ In Progress / ☑ Complete
- **Phase 4**: ☐ Not Started / ◐ In Progress / ☑ Complete

**Current Status**: Ready to begin Phase 1.1

**Next Action**: Fix CompletionContext JSON tag in `pkg/lsp/lsp.go`

ALWAYS update ./current-task.md as you check off items in the above checklist.
