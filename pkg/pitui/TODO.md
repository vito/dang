# pitui TODO

## What's Good

**The core rendering model is excellent.** The scrollback-buffer differential
renderer (no alternate screen) is a smart choice for a REPL — it preserves
terminal history and plays well with copy/paste and scrollback. Synchronized
output (`?2026h/l`) prevents tearing. The line-diff approach with fallback to
full redraw for off-screen changes is pragmatic and well-tested.

**Compo's generation-counter caching is clever.** Using `atomic.Int64`
generations instead of a dirty boolean elegantly solves the
concurrent-update-during-render race (as `TestConcurrentUpdateNotLost`
validates). The parent propagation via `Update()` is clean and makes finalized
entries truly O(1).

**The Component/Container/Slot model is simple and sufficient.** It's the right
level of abstraction — not trying to be a full widget toolkit, just enough
structure for a line-oriented TUI.

**Good test coverage.** Golden tests for overlay compositing, regression tests
for specific bugs (segment reset accumulation, concurrent updates, shrink
handling), and the mock terminal make the rendering engine trustworthy.

---

## Critiques of `pkg/pitui/`

### 1. `doRender()` is a 200-line monolith

The core render function has too many responsibilities: snapshot state, render
components, composite overlays, diff lines, build escape sequences, write
output, update cursor, emit debug stats. The nested `fullRender` closure
captures and mutates surrounding variables, making control flow hard to follow.
The `computeLineDiff` closure also closes over mutable `prevViewportTop` and
`viewportTop` variables that get mutated mid-function. This should be
decomposed.

### 2. Mixed locking disciplines

`TUI.mu` guards a grab-bag of fields, but the discipline is inconsistent:

- `doRender` takes the lock, snapshots fields, releases, then later re-takes to
  store results — this is fine for correctness but means the "state" is spread
  across local variables and struct fields in a confusing way.
- ~~`overlayEntry.options` and `overlayEntry.hidden` were mutated without the
  TUI's lock~~ — **FIXED**: `SetOptions` and `SetHidden` now hold `tui.mu`, and
  `doRender` snapshots overlay entries by value.
- ~~Focus management was entangled with overlay visibility~~ — **FIXED**:
  overlays are now pure rendering constructs; callers manage focus explicitly.

### 3. Key constants are legacy dead weight

`keys.go` defines raw byte constants (`KeyCtrlA`, `KeyUp`, etc.) and the REPL
uses `string(data) == pitui.KeyCtrlC` comparisons. But `TextInput` has already
migrated to ultraviolet's `EventDecoder`. The REPL's `onKey` callback still
receives legacy byte sequences via `KeyToBytes()` — a lossy round-trip
conversion from ultraviolet events back to raw bytes, which then get
string-compared. This two-system approach means some key combos silently won't
work (e.g., `KeyToBytes` doesn't handle F-keys, PgUp/PgDown, or most modifier
combos).

### 4. `TextInput.HandleInput` uses a deferred `Update()` unconditionally

Every `handleKeyPress` call defers `Update()` and runs `OnChange` diffing.
Cursor-only movements (left, right, Home, End, word-left, word-right) trigger
`Update()` even though `OnChange` is guarded — but the Compo is still marked
dirty and a full re-render is scheduled. This is correct but inefficient;
cursor-only movement could use a lighter-weight path.

### 5. No scroll/viewport for the base content

The Container is an unbounded vertical stack. With hundreds of REPL entries, the
"rendered output" grows without bound. The diff engine handles this by only
repainting changed lines, but every `Container.Render` still iterates all
children (even if each one hits its Compo cache). For a REPL session with
thousands of entries, this linear scan becomes meaningful. There's no viewport
that could skip children entirely outside the visible range.

### 6. Overlay compositing is O(width × height) per overlay per frame

`CompositeLineAt` does per-line column surgery with ANSI-aware slicing. For
overlays with 10+ lines this is fine, but the approach won't scale if overlays
get complex. More importantly, the two-pass overlay layout (render once to get
height, resolve layout, potentially re-render with clamped height) means overlay
components can be rendered twice per frame.

### 7. Terminal interface is thin but inflexible

`Terminal` has no way to query capabilities (sixel, kitty images, true color).
`ProcessTerminal` hardcodes the Kitty keyboard protocol enablement, which could
break on terminals that don't support it (no graceful degradation based on the
query response — the response is just decoded as input and silently ignored).

---

## Critiques of REPL Usage (`cmd/dang/repl_pitui.go`)

### 8. `replComponent` is a god object (~1450 lines)

It handles: eval, completion, completion menu positioning, detail bubble
management, history, commands, Dagger module loading, doc browser lifecycle,
input routing, and spinner swapping. This is essentially the entire REPL in one
struct with 30+ fields behind a single `sync.Mutex`. The locking is also
irregular — `mu` is sometimes held across UI operations, sometimes not, and
`ev.mu` is nested inside `r.mu`, creating a two-level lock hierarchy that's easy
to deadlock on.

### 9. Completion menu positioning is fragile

The menu uses `r.entryContainer.LineCount()` to decide whether to place the menu
above or below input. This is a snapshot that can become stale if content is
added concurrently (e.g., streaming Dagger output). The `completionXOffset`
calculation manually measures the prompt width via lipgloss, duplicating
knowledge of how `TextInput` renders its prompt.

### 10. The `pituiSyncWriter` → `activeEntryView` chain is brittle

Dagger log output streams through `pituiSyncWriter`, which grabs `r.mu` via
`SetRepl`, then accesses the last child of `entryContainer`, then grabs `ev.mu`
to write. If eval finishes and a new entry is added concurrently, the log output
could go to the wrong entry. The double-mutex pattern (`r.mu` → `ev.mu`) is
maintained by convention, not by type safety.

### ~~11. Eval input routing is a workaround~~ — FIXED

~~During eval, a `TUI.AddInputListener` intercepted all input, re-decoded it
through a second `uv.EventDecoder`, converted back to legacy key bytes via
`KeyToBytes`, and called `r.onKey`.~~ The TUI now owns a single decoder and
dispatches typed `uv.Event`s. The eval listener receives `uv.KeyPressEvent`
directly. `Interactive.HandleKeyPress` replaces `HandleInput([]byte)`.
`InputListener` receives `uv.Event` and returns `bool`.

### 12. History is simplistic

`/tmp/dang_history` is hardcoded, no XDG_DATA_HOME. `saveHistory` rewrites the
entire file on every entry. Multi-line entries are lost (newlines in history get
split into separate entries on reload). No deduplication beyond consecutive
duplicates.

### 13. Missing graceful degradation

If `tui.Start()` fails (e.g., not a TTY), there's no fallback to a plain-line
REPL. The doc browser overlay reads `d.tui.Terminal().Rows()` directly when
`ctx.Height` is 0, coupling it to the TUI instance rather than working purely
from render context.

---

## Prioritized Checklist

Ordered by impact × effort. Correctness bugs first, then structural debt, then
polish.

- [x] **Fix `OverlayHandle.SetOptions` data race** — add `tui.mu` around the
      assignment; snapshot overlay entries by value in `doRender` (#2)
- [x] **Retire legacy key constants** — `TextInput.OnKey` now receives `uv.Key`
      directly. Deleted `keys.go` and `KeyToBytes`. REPL and doc browser match
      on `key.Code`/`key.Mod` (#3, #11)
- [x] **Simplify eval input routing** — eval listener now passes `uv.Key`
      straight to `onKey`, no more re-encoding through `KeyToBytes` (#11)
- [ ] **Break up `replComponent`** — extract completion menu controller, history
      manager, and command dispatcher into separate types (#8)
- [x] **Decompose `doRender()`** — extracted snapshotForRender, renderFrame,
      applyFrame, writeFullRedraw, writeTailShrink, writeDiffUpdate, diffLines.
      Named escape constants and cursor helpers. doRender is now 17 lines (#1)
- [x] **Fix `pituiSyncWriter` entry targeting** — pituiSyncWriter now stores
      target `*entryView` directly; set at eval start, cleared in finishEval.
      finishEval receives captured entry instead of re-resolving (#10)
- [ ] **Fix history persistence** — use XDG_DATA_HOME, encode multi-line entries
      (e.g. base64 or `\n` escaping), append instead of rewrite (#12)
- [ ] **Audit `r.mu` / `ev.mu` lock ordering** — document or enforce the
      hierarchy to prevent deadlocks (#8, #10)
- [ ] **Decouple completion positioning from prompt rendering** — have
      `TextInput` expose cursor screen-column so callers don't re-measure the
      prompt (#9)
- [ ] **Skip cursor-only `Update()` in TextInput** — only mark dirty when
      content changes; cursor reposition can use a lighter signal (#4)
- [ ] **Add viewport-aware Container** — skip `renderComponent` for children
      entirely above the viewport, making long sessions O(visible) (#5)
- [ ] **Single-pass overlay layout** — pass max height to first render so
      components aren't rendered twice (#6)
- [ ] **Fallback to line-mode REPL** — detect non-TTY and degrade gracefully
      (#13)
- [ ] **Remove `d.tui.Terminal().Rows()` from doc browser** — use `ctx.Height`
      exclusively (#13)
- [ ] **Terminal capability negotiation** — query and store terminal features,
      gracefully degrade Kitty keyboard protocol (#7)
- [ ] **Per-component debug stats in dashboard** — wire the existing
      `componentStats` collection into a dashboard view (noted in prior TODO)
