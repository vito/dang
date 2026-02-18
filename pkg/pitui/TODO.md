# pitui TODO

Items remaining after the `tui-redux` cleanup. Ordered roughly by value.

---

## Structural

- [ ] **Break up `replComponent`** — extract completion menu controller, history
      manager, and command dispatcher into separate types. The god object has
      ~1450 lines and 30+ fields behind a single `sync.Mutex`. (#8)

- [ ] **Audit `r.mu` / `ev.mu` lock ordering** — document or enforce the
      two-level lock hierarchy (`r.mu` → `ev.mu`) to prevent deadlocks.
      Best addressed alongside the `replComponent` breakup. (#8, #10)

## Robustness

- [ ] **Fallback to line-mode REPL** — detect non-TTY and degrade gracefully
      instead of failing on `tui.Start()`. (#13)

- [ ] **Terminal capability negotiation** — query and store terminal features
      so Kitty keyboard protocol is only enabled when supported, instead of
      sending the enable sequence unconditionally and ignoring the response. (#7)

## Performance

- [ ] **Single-pass overlay layout** — pass max height to the first render so
      overlay components aren't rendered twice per frame. (#6)

---

## Done

- [x] Fix `OverlayHandle.SetOptions` data race (#2)
- [x] Decouple overlays from focus management (#2)
- [x] Retire legacy key constants; delete `keys.go` and `KeyToBytes` (#3)
- [x] Centralize input decoding in TUI; `HandleKeyPress(uv.KeyPressEvent)` (#11)
- [x] Simplify eval input routing (#11)
- [x] Decompose `doRender()` into named methods (#1)
- [x] Fix `pituiSyncWriter` entry targeting (#10)
- [x] Fix history persistence — XDG_DATA_HOME, multi-line entries, append-only (#12)
- [x] Decouple completion positioning — `TextInput.CursorScreenCol()` (#9)
- [x] Remove `Terminal().Rows()` from doc browser (#13)
