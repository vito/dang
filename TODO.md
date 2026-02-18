# pitui / REPL cleanup

## pitui

- [x] Remove `TUI.HideOverlay()` — unused, only `OverlayHandle.Hide()` is called.
- [x] Rename `RequestRender(force bool)` arg to `repaint` for clarity.
- [x] Remove dead `tui` field from `Spinner` — `Update()` propagation through `Compo.parent` handles render requests; the `*TUI` parameter to `NewSpinner` is unused.
- [x] Add cursor-relative overlay positioning — overlays can be anchored to the base content's cursor position with automatic above/below flipping.
- [x] Remove `Container.LineCount()` — no longer needed now that completion overlays use cursor-relative positioning.

## REPL

- [x] Guard `close(r.quit)` with `sync.Once` — prevent double-close panics from Ctrl+C/Ctrl+D/:exit races.
- [x] Batch `pituiSyncWriter` writes — coalescing flush goroutine reduces lock contention and render churn from high-frequency Dagger progress output.
- [x] Migrate completion menu and detail bubble to cursor-relative overlays — eliminates manual LineCount-based positioning logic.
