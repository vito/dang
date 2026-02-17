# pitui TODO

## ✅ Replace `outputLog` with per-entry components — DONE

Each `replEntry` is now wrapped in an `entryView` component, appended as
a child of an `entryContainer` (`pitui.Container`). Finalized entries
are never `Update()`d again, so the framework skips their Render()
entirely and returns cached output.

## ✅ Compo-based render caching — DONE

Components embed `pitui.Compo` to get automatic render caching. Call
`Update()` when state changes — the framework re-renders on the next
frame. Between `Update()` calls, `Render()` is skipped entirely and
the cached result is reused.

`Update()` propagates upward through parent Containers/Slots, and
the root Compo automatically calls `TUI.RequestRender`. So a single
`component.Update()` call is all that's needed to schedule a repaint.

Components without Compo (legacy or simple) always render — the
framework treats them as always-dirty.

## Per-component debug stats

Add per-component render metrics to the JSONL debug output so the
dashboard can show which components are cached vs rendered and how
long each takes.

1. **`componentStats` in `RenderContext`.** Already wired: Container
   and Slot collect timing data. `renderComponent` now also records
   cache hits with a `Cached: true` flag.

2. **Dashboard.** Add a per-component table: name, render count,
   cache hit rate, avg/max render time, avg lines.
