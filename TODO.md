# pitui / REPL cleanup

## pitui

- [x] Remove `TUI.HideOverlay()` — unused, only `OverlayHandle.Hide()` is called.
- [x] Rename `RequestRender(force bool)` arg to `repaint` for clarity.
- [x] Remove dead `tui` field from `Spinner` — `Update()` propagation through `Compo.parent` handles render requests; the `*TUI` parameter to `NewSpinner` is unused.

## REPL

- [x] Guard `close(r.quit)` with `sync.Once` — prevent double-close panics from Ctrl+C/Ctrl+D/:exit races.
- [x] Batch `pituiSyncWriter` writes — coalescing flush goroutine reduces lock contention and render churn from high-frequency Dagger progress output.
