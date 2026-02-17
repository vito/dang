# pitui TODO

## ✅ Replace `outputLog` with per-entry components — DONE

Each `replEntry` is now wrapped in an `entryView` component, appended as
a child of an `entryContainer` (`pitui.Container`). Finalized entries
return `Dirty: false` and are skipped by Container's caching.

## ✅ Clean up Dirty / Invalidate — DONE

- `Invalidate()` removed from the `Component` interface. Components that
  need internal cache busting use their own methods (e.g. `markDirty()`).
- `RenderResult.Dirty` is now used by `Container.Render` to skip work
  for clean children (reuses cached lines from `prevChildMap`).
- `Container` propagates `Dirty: false` when all children are clean and
  width hasn't changed, allowing `doRender`'s diff to find no changes.

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
