# pitui TODO

## ✅ Replace `outputLog` with per-entry components — DONE

Each `replEntry` is now wrapped in an `entryView` component, appended as
a child of an `entryContainer` (`pitui.Container`). Finalized entries
cache their rendered lines and return them instantly on subsequent renders.

## ✅ Simplify rendering model — DONE

Removed the `Dirty` flag from `RenderResult` entirely. Components just
return lines; the framework's existing line-level string diff handles all
change detection. Components that want to be fast (like `entryView`)
cache internally and return the cached slice — the diff sees identical
strings and skips them.

Also removed `Invalidate()` from the `Component` interface (was already
a no-op everywhere) and the `prevChildMap` caching from `Container`
(no longer needed without Dirty tracking). `Slot` no longer tracks its
own dirty flag either.

## Per-component debug stats

Add per-component render metrics to the JSONL debug output so the
dashboard can show which components are slow.

1. **Add `componentStats` collector to `RenderContext`.** When the
   debug writer is set, `doRender` passes a collector. `Container.
   Render` records each child's name (via `reflect.TypeOf` or a
   `Name() string` interface), render duration, and line count.

2. **Add `components` array to the JSONL output.** Each entry:
   `{"name": "entryView", "render_us": 5, "lines": 3}`

3. **Add a per-component table to the dashboard.** Columns: name,
   render count, avg/max render time, avg lines. Sort by total time
   descending.
