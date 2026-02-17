# pitui TODO

## Replace `outputLog` with per-entry components

The REPL currently funnels all history through a single `outputLog`
component that re-renders every entry from scratch whenever anything
changes. Replace it with one component per `replEntry`, appended as
children of a container that sits above the input slot.

### Current architecture

```
TUI Container
├── outputLog  ← single component, re-renders ALL entries on markDirty()
└── inputSlot  ← text input or spinner
```

`outputLog.Render()` locks `repl.mu`, snapshots every entry, iterates
them all, splits strings, expands tabs, and truncates. The internal
`dirty`/`cachedLines` fields exist solely to avoid repeating this on
frames where nothing changed. `markDirty()` is called from 10+ sites.

### Target architecture

```
TUI Container
├── entryView (welcome)      ← rendered once, immutable
├── entryView (dang> 1+1)    ← finalized after eval, immutable
├── entryView (dang> foo)    ← active: logs streaming in, re-renders
└── inputSlot
```

### Steps

1. **Create `entryView` component.** Wraps a single `replEntry`. On
   `Render`, it reads `entry.input`, `entry.logs`, `entry.result` and
   produces lines. It keeps its own `cachedLines` and a `dirty` flag.
   When the entry is finalized (result written, eval done), it renders
   one last time and never re-renders again.

2. **Add a `finalized` flag to `replEntry`.** Once set, the `entryView`
   returns cached lines with `Dirty: false` on every subsequent render.
   `finishEval` sets this after writing results. Command handlers
   (`:help`, `:env`, etc.) set it after writing their output.

3. **Replace `outputLog` with an `entryContainer`.** This is just a
   `pitui.Container` (or a thin wrapper) that the REPL appends
   `entryView` children to. It replaces `r.output` and the `r.entries`
   slice. The TUI structure becomes:

   ```go
   r.tui.AddChild(r.entryContainer)
   r.tui.AddChild(r.inputSlot)
   ```

4. **Update `onSubmit`.** Instead of appending to `r.entries` and calling
   `markDirty()`, create a new `entryView`, add it to the container.

5. **Update `pituiSyncWriter`.** Instead of `r.lastEntry().writeLog()`
   + `r.output.markDirty()`, get the active `entryView` and mark *it*
   dirty. Only that component re-renders.

6. **Update `finishEval`.** Write results to the active `entryView`,
   set `finalized = true`, create the next blank entry if needed.

7. **Update `:clear`.** Remove all children from the entry container.
   This replaces `r.entries = nil; r.output.markDirty()`.

8. **Fix completion menu positioning.** The menu currently reads
   `len(r.output.cachedLines)` to determine how many content lines are
   above the input. Replace with a method on the entry container that
   sums child line counts (each `entryView` knows its own line count
   from its cached render). Or, since the menu uses
   `ContentRelative: true` with `OffsetY: -1`, just count the
   container's total rendered lines. This value is stable because past
   entries don't change height once finalized.

9. **Remove `outputLog` entirely.** Delete the type and all
   `markDirty()` call sites.

### Mutex cleanup

`outputLog.Render()` currently locks `repl.mu` to snapshot entries.
With per-entry components, each `entryView` owns its own data. The
active entry still needs synchronization (streaming writes from
`pituiSyncWriter` on a different goroutine), but finalized entries
need no locking at all. Move the mutex into `entryView` (or just
the active one) instead of a single global lock on `replComponent`.

---

## Clean up Dirty / Invalidate

The `Dirty` flag on `RenderResult` and the `Invalidate()` method on
`Component` are underused and inconsistent. After the `outputLog`
refactor above, clean them up.

### Current state

- `RenderResult.Dirty` exists but `doRender` **never checks it**. It
  is only recorded in debug stats. The real optimization is the
  line-level string diff which works regardless of Dirty.

- `Invalidate()` is on the `Component` interface but every component
  except `outputLog` and `Slot` implements it as a no-op. `TUI.
  Invalidate()` is defined but **never called from any call site**.

- `Slot` uses an internal `dirty` field to signal that its child was
  swapped, which is genuinely useful (it tells `Container.Render` the
  output changed). But this is conflated with the component-level
  `Dirty` flag.

- Most components return `Dirty: true` unconditionally: `TextInput`,
  `Spinner`, `completionOverlay`, `detailBubble`, `docBrowserOverlay`.
  This means `Container` always propagates `Dirty: true`, so the
  framework-level flag is always true and useless.

### Plan

The goal: after the `outputLog` split, finalized `entryView`s return
`Dirty: false` and the framework uses that to **skip diffing their
line ranges entirely**. This is the big win — 200 finalized entries
producing 200+ lines can be skipped in O(1) instead of O(n) string
comparisons.

1. **Remove `Invalidate()` from the `Component` interface.** Nothing
   calls it externally. Delete `TUI.Invalidate()`, delete the no-op
   implementations everywhere. If a component needs internal cache
   busting (like the new `entryView`), it can use its own method
   (e.g. `markDirty()`) — that's a component-private concern, not a
   framework interface.

2. **Keep `Dirty` on `RenderResult` but make the framework use it.**
   Add a fast path in `Container.Render`: track each child's line
   offset and how many lines it produced. When a child returns
   `Dirty: false`, copy its previous lines into the result without
   re-examining them. This means the Container skips string building
   for clean children.

   Then in `doRender`: when the overall `Dirty` is false (no child
   changed) and there are no overlays and width hasn't changed, skip
   the line diff entirely. Just reposition the cursor and return.

3. **Fix components to report `Dirty` honestly.**
   - `entryView` (new): returns `Dirty: true` when content changed,
     `Dirty: false` when finalized and cached. This is the main win.
   - `TextInput`: returns `Dirty: true` always. It's 1 line, so the
     diff cost is negligible. Leave it.
   - `Spinner`: returns `Dirty: true` always (animating). Fine.
   - Overlay components: always `Dirty: true`. Fine — they're small
     and composited separately anyway.

4. **Simplify `Slot.dirty`.** `Slot` has its own `dirty` flag to
   handle child swaps. This is fine — keep it. It correctly ORs with
   the child's dirty flag.

5. **Wire into debug stats.** The existing `ComponentDirty` stat
   already records whether the tree reported dirty. After this change
   it becomes meaningful: `false` means we actually skipped work.

### What this achieves

On a typical idle REPL frame (cursor blinking, no eval running):
- 200 finalized `entryView`s each return `Dirty: false` instantly
- `Container.Render` copies their cached lines in O(1) each
- `TextInput` returns `Dirty: true` (1 line)
- The framework diffs only the cursor line
- Total work: O(1) per finalized entry + O(1) for the input line

On a streaming eval frame (logs arriving):
- Only the active `entryView` returns `Dirty: true`
- The framework diffs only its line range
- Finalized entries above are skipped entirely

---

## Per-component debug stats

After the above refactors are done, add per-component render metrics
to the JSONL debug output so the dashboard can show which components
are dirty, how long each takes, and flag components that never cache.

1. **Add `componentStats` collector to `RenderContext`.** When the
   debug writer is set, `doRender` passes a collector. `Container.
   Render` records each child's name (via `reflect.TypeOf` or a
   `Name() string` interface), render duration, line count, and dirty
   flag.

2. **Add `components` array to the JSONL output.** Each entry:
   `{"name": "entryView", "render_us": 5, "lines": 3, "dirty": false}`

3. **Add a per-component table to the dashboard.** Columns: name,
   render count, dirty rate (%), avg/max render time, avg lines,
   status flag ("always dirty" / "caching" / "N% dirty"). Sort by
   total time descending. This is the tool that catches components
   that wastefully return `Dirty: true` or take too long to render.

---

## Stretch: Container child-line-range tracking

To make the `Dirty: false` skip work at the diff level (not just the
render level), `Container` needs to know which line ranges belong to
which children. This is a prerequisite for step 2 of the Dirty cleanup.

1. **Track `childOffsets []int` in Container.** After rendering,
   `childOffsets[i]` is the starting line index of child `i`. This is
   trivially computed during the render loop.

2. **When a child returns `Dirty: false`, reuse its previous lines.**
   Instead of appending `r.Lines` (which the child had to rebuild or
   return from cache), the Container can copy the corresponding slice
   from its own `previousLines`. This means a finalized `entryView`
   doesn't even need to *return* its cached lines — the Container
   already has them.

3. **Expose dirty ranges to `doRender`.** Instead of diffing all
   lines, `doRender` could skip ranges known to be clean. This is a
   bigger change to the diff algorithm and may not be worth it given
   string comparison is fast. Measure first.
