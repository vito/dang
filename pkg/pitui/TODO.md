# pitui TODO

## Performance

- [ ] **Cache terminal dimensions on SIGWINCH**
  `ProcessTerminal.Columns()` and `Rows()` each do an `IoctlGetWinsize`
  syscall. A single `doRender` with the doc browser open does ~8+ ioctls
  (renderer calls both at top, `compositeOverlays` calls
  `resolveOverlayLayout` 3x per overlay, doc browser calls
  `Terminal().Rows()` directly). Cache the values in `ProcessTerminal` and
  update on SIGWINCH + `Start()`. The `onResize` callback is already wired.

- [ ] **Make overlay position mutable (stop recreating completion menu)**
  `showCompletionMenu` calls `menuHandle.Hide()` + `tui.ShowOverlay()`
  every keystroke just to update `OffsetX`. This allocates a new
  `completionOverlay`, `overlayEntry`, and does focus management
  round-trips at typing speed. Add a method on `OverlayHandle` (e.g.
  `SetOptions` or individual setters like `SetOffset(x, y)`) so the REPL
  can reposition without destroy/recreate.

- [ ] **Use dirty flags to skip diffing unchanged line ranges**
  `Dirty bool` is on `RenderResult` and `outputLog` returns `Dirty: false`
  when cached, but `doRender` ignores it — the line-by-line diff always
  runs over the full output. The component-level skip (outputLog doesn't
  re-process) helps, but the framework still does O(total lines) string
  comparisons every frame. Container could track child line offsets and
  only diff dirty children's ranges. Deferring: the string-equality diff
  loop is very fast in practice; the main win (skipping outputLog
  re-processing) is already captured at the component level.

- [ ] **Fix spinner goroutine leak**
  `Spinner.Stop()` sets `stopped = true` and calls `ticker.Stop()`, but
  the goroutine does `for range ticker.C` — after `Stop()` the channel
  isn't closed so the goroutine lingers until the next buffered tick. One
  leaked goroutine per eval. Add a `done chan struct{}` and select on it.

## Abstractions

- [ ] **Consolidate overlay visibility (`hidden` vs `Visible` callback)**
  Two mechanisms: `hidden bool` (set imperatively via `SetHidden`) and
  `Visible func(...)` (polled every render). `isOverlayVisible` checks
  both, but `SetHidden` does focus management while `Visible` returning
  false doesn't. Using both on the same overlay produces confusing focus
  state. Pick one model or make them compose cleanly.

- [ ] **Unify focus/input ownership**
  Focus is managed in `ShowOverlay`, `HideOverlay`, `removeOverlay`, and
  `SetHidden`, each with its own "who gets focus now?" logic (some check
  `topmostVisibleOverlay`, some restore `preFocus`). The REPL adds
  `AddInputListener` as a third mechanism for eval mode (routing input to
  `onKey` while focus is nil). There should be a single model for "who
  owns input right now" instead of three overlapping ones.

- [ ] **Remove doc browser's direct `tui.Terminal().Rows()` dependency**
  `docBrowserOverlay` stores `*pitui.TUI` and calls `Terminal().Rows()`
  in `listHeight()` and as a fallback in `Render()`. This breaks the
  contract that `RenderContext` is sufficient and makes the doc browser
  untestable without a real TUI. Blocked on: overlay system reliably
  passing `Height` in `RenderContext` (it already does, but the doc
  browser doesn't trust it because base container passes 0).

- [ ] **Decouple `outputLog` from `replComponent` internals**
  `outputLog.Render` locks `repl.mu` to snapshot entries. The component
  doesn't own its data — it's a view into shared mutable state. This
  means you can't test it in isolation, and the render path holds a
  user-space mutex that could block if eval is writing logs concurrently.
  Give `outputLog` its own append-only data or have the REPL push
  snapshots to it.

- [ ] **Collapse `resolveOverlayLayout` triple-call**
  `compositeOverlays` calls `resolveOverlayLayout` 3 times per overlay
  per render: once for width (before component render), once for
  maxHeight (after), once for row/col (after clipping). This is because
  layout depends on rendered height which depends on width. Restructure
  into a two-pass (resolve width → render → resolve placement) or pass
  the component's rendered height into a single call.

- [ ] **Add layout negotiation to Container**
  Container just concatenates children. No child can say "I want N lines"
  or "give me remaining space." The doc browser works around this by
  querying terminal dimensions directly (see above). A scrollable
  viewport or fixed-height output pane would need this. Add when a real
  use case appears — don't speculate.
