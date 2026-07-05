# Design: native Dang module imports

Status: **Phase 1 DONE — local path imports shipped** (see §7.1, all steps
landed). `[imports.X] path = "./dir"` now binds X to a native Dang module
compiled from source, with its own `pub`/`let` API. The cross-USER-module `let`
visibility boundary is finally reachable and tested end to end
(`tests/importlib/` + `tests/test_import_dang_module.dang` positive;
`tests/errors/import_dang_private_member.dang` negative golden). Phases 2–3 (VCS
refs, fetch/cache/lock, MVS, transitive deps) remain future work. Decisions
locked: run all module code on import (no library/program split), local path
imports first.

Implementation surfaced one latent bug, now fixed: `ObjectDecl.Hoist`
(fields.go) added a `type`'s constructor scheme to the type env **without**
setting its visibility, so it defaulted to private and never re-exported across
an import boundary — invisible until a user type was first imported. The eval
side already bound the constructor with `c.Visibility`; the infer side now
matches. Everything else reused existing machinery verbatim.

Modeled on **Go modules** (decentralized, VCS-backed, semver + MVS, no central
registry) and **Dagger module refs** (`host/user/repo[/subdir]@version`,
piggybacking on VCS metadata). **Non-goal: hosting a package registry.** Refs
resolve directly to VCS; fetching is `git`.

### Why not just lean on Dagger for packaging + versioning?

In an ideal world we wouldn't build this at all — Dagger already solves
decentralized, VCS-backed module distribution and versioning, and Dang already
speaks to Dagger. But a Dagger module exposes a **GraphQL API**, and GraphQL
can't express Dang-native features — most notably **block arguments**
(function-typed / closure params like `&block(x: Int!): Int!`), but also the
rest of Dang's value semantics that don't survive an introspection round-trip.
Importing a Dang library *through* Dagger would silently flatten it to its
GraphQL projection and drop exactly the features that make it Dang. So native
Dang imports are needed to carry Dang semantics across the module boundary. We
still borrow Dagger's *distribution* model (VCS refs, no registry); we just load
Dang source directly instead of introspecting a schema.

---

## 0. The model in one paragraph

A **module** is a directory of `.dang` files with a `dang.toml` manifest. Its
`pub` top-level declarations are its API; its `let`s are private to it (this is
what the module-level visibility system already enforces — see the
`member-visibility-module-level` memory). Code imports by a **short local name**
(`import Semver`), and the consumer's `dang.toml` maps that name to a **ref**
(local path or VCS ref + version). This mirrors the *existing* GraphQL-import
model (`import Dagger`, configured in `dang.toml`) — we extend `[imports.X]`
with new source kinds rather than invent a parallel mechanism. Refs live in
`dang.toml`, never in code, so imports stay portable and aliasable.

---

## 1. `dang.toml`: one `[imports]` table, several source kinds

Today `ImportSource` (project.go) has GraphQL kinds: `dagger`, `endpoint`,
`service`, `schema`. Add Dang-source kinds; the kind is inferred from which
fields are set:

```toml
# GraphQL (today, unchanged)
[imports.Dagger]
dagger = true
service = ["dagger-dev", "session"]

# Dang module, local path (Phase 1) — relative to this dang.toml
[imports.Helpers]
path = "./lib/helpers"

# Dang module, VCS ref (Phase 2)
[imports.Semver]
git     = "github.com/vito/dang-semver"
version = "v1.4.0"        # semver tag, or a commit / branch
subdir  = "semver"         # optional, for monorepos (Dagger-style)
```

From the consumer's view all of these are "a namespace of types + values reached
via `X.member`"; it does not care whether X is a GraphQL schema or a Dang
library. The short name is **local to the module that declares it** (imports are
already file/module-scoped), so no global alias namespace and no collision
across modules.

A library module's OWN `dang.toml` carries its own `[imports.*]` (its transitive
deps) plus a `[module]` header giving its canonical path (needed for versioning
and cycle identity, like `module github.com/...` in go.mod):

```toml
[module]
path = "github.com/vito/dang-semver"
```

---

## 2. What a module exposes, and the library/program split

**Exposes:** its `pub` top-level types, functions, and values — exactly
`Bindings(PublicVisibility)` + public `NamedTypes` + public directives. The
existing `installUnqualifiedImportSymbols` / `installImportedTypeScope` /
`installImportedValueScope` machinery already does this for schema modules; we
reuse it verbatim. The module's `let`s stay private — and because the imported
module is compiled with its *own* `moduleScope`, a consumer touching one of its
`let`s is rejected by the visibility check already in `Select.Infer`. **That is
the feature validating itself.**

**The "main" question — DECIDED: run all the code (no library/program split).**
An imported module simply runs top-to-bottom, like a Python module without an
`if __name__ == "__main__"` guard; its `pub` bindings are then exposed. No new
syntax, no `main { }`, no export list. This makes the loader trivially the
existing full load path (`InferDirectoryFiles` + `evaluateDirectoryFiles`, i.e.
`RunDir` returning its scopes) rather than a declaration-only variant. A
side-effecting top-level statement in a module runs when the module is imported;
authors who don't want that simply don't write top-level side effects. If a
guard is ever wanted, it can be added later without reworking this.

---

## 3. Tough question 1 — where do we find the code?

Resolution is layered, most-specific first:

1. **Local path** (`path = "./x"`): resolve relative to the declaring
   `dang.toml`; no fetch. This is the whole Phase 1.
2. **VCS ref** (`git = "host/user/repo"`, optional `subdir`, `version`):
   - **Known hosts** (github.com, gitlab.com, …) map directly to a git remote
     by well-known pattern — the Dagger approach.
   - **Vanity paths** resolve via the **`go-import` meta-tag protocol**: GET
     `https://<path>?go-get=1`, parse `<meta name="go-import" content="…">`.
     This is *protocol, not toolchain* — maximally low-maintenance and already
     emitted by heaps of existing infra. No dependency on the `go` binary.
   - **Explicit override**: a `git-url = "ssh://…"` field for anything exotic.
3. The import **name** in code is a local alias; the canonical identity for
   caching/versioning/MVS is the resolved **module path** (`host/user/repo`),
   never the alias.

## 4. Tough question 2 — remote deps & fetching

- **Fetch** with `git` (shallow fetch of the exact tag/commit) into a
  content-addressed **module cache** (`$DANG_MODCACHE`, default under the XDG
  cache dir), keyed by `module-path@resolved-commit`. Immutable once written; a
  cache hit needs no network.
- **Reproducibility**: `dang.toml` states *intent* (version constraints);
  `dang.lock` records the *resolved* commit SHA + content hash per module (Go's
  go.mod/go.sum split). Present lock ⇒ deterministic, offline-capable builds.
- **Transitive deps**: a fetched module's own `dang.toml` `[imports.*]` are
  recursed into, building the dependency graph.
- **Version selection**: **Minimal Version Selection** (Go). Each module lists
  direct deps with minimum versions; the build takes the max of the minimums per
  module path — one version per path, deterministic, no SAT solver, no
  lockfile-as-solver-output. Cycles are detected via the module cache's
  in-progress marks and errored (like Go).
- **No central registry**: everything is VCS + git + an HTTP meta GET. An
  optional GOPROXY-style read-through cache could be added later without
  changing the model.

---

## 5. Integration with the existing import path (grounded in code)

- `ImportDecl.Infer` (ast_declarations.go) currently: resolve name →
  `TypeScopeFromSchema` (or shared cache) → `installImportedTypeScope`. Add a
  branch: if the resolved `ImportConfig` is a **Dang source**, produce the
  module's compiled `TypeScope` by **loading + inferring the module dir with its
  own `moduleScope`** (a library variant of `InferDirectoryFiles` /
  `declareDirectoryFiles`), then install it the same way.
- `ImportDecl.Eval`: currently `ValueScopeFromSchema` → `installImportedValueScope`.
  Add a branch producing the Dang module's **value scope** (its `pub` bindings,
  lazily evaluated), installed the same way.
- **Identity/unification**: reuse `sharedImportModule`/`cacheImportModule`, but
  key Dang modules by resolved **module-path@version** (not the local alias), so
  every importer of the same module@version gets one `*Type` identity and types
  unify across the graph — the same guarantee the schema cache gives.
- **Compilation happens once per module@version per build.** The loaded module
  is frozen and shared, exactly like the prelude.

## 6. Why visibility falls out for free

Load module A with `moduleScope{path@version A}`; its declared types get
`homeModule = A`. The consuming module B is compiled with its own `moduleScope`.
When B does `aValue.letMember`, `Select.Infer` sees `home(A) != cur(B)` → reject;
`aValue.pubMember` → allowed. **No new visibility code** — the Phase-1 loader
is sufficient to finally test cross-user-module `let` privacy end to end. One
thing to verify when it lands: an imported Dang type must keep *its own*
`homeModule` (A), i.e. loading must not re-stamp it with the importer's scope
(type-statics.md §8 flagged exactly this).

---

## 7. Phasing

1. **Phase 1 — local path imports (DONE; see §7.1).**
   `[imports.X] path = "…"`; load the dir as a module (run all its code), own
   `moduleScope`, reuse the install machinery. Network-free, versioning-free,
   low-risk. **Unblocked and shipped the cross-module visibility test.**
2. **Phase 2 — VCS refs + fetch + cache + lock.** Known hosts + git fetch +
   `dang.lock` + `$DANG_MODCACHE`. Single-level (no transitive) first.
3. **Phase 3 — transitive deps + MVS + vanity (`go-import`) resolution.** The
   full Go-style graph.

## 7.1 Phase 1 implementation plan (DONE)

All steps below landed. Net new code: `pkg/dang/import_dang.go` (loader, cache,
`dangModule`, isolated module import context) plus small branches in
`project.go`, `ast_declarations.go`, and the one-line visibility fix in
`fields.go` (see status header). Deviations from the plan below:
- The loader is `loadDangModule` returning a `*dangModule` (dir + typeScope +
  blocks + lazily-evaluated valueScope), not the sketched
  `loadDangModuleTypes`. Eval reuses the same `blocks` via `i.dangMod` on the
  node — the two-phase carry — so cached import-node state (not re-resolved
  configs) drives the module's own imports at eval; `moduleScope` is only read
  during infer, so eval needs no sub-context.
- Modules compile under an **isolated** import context (`moduleImportContext`):
  the importer's project/schema imports are stripped and the module resolves its
  OWN `dang.toml` (at its root, no walk-up), so a Dagger auto-import the consumer
  configured never leaks into a module that didn't ask for it.
- Test vehicle: the integration harness pre-loads `tests/dang.toml` and injects
  its configs into every test's ctx, so a nested `dang.toml` under a `test_*/`
  dir is NOT auto-resolved by `RunDir` (its `ensureProjectImports` early-returns
  on the already-present configs). So `[imports.Lib] path` lives in the
  top-level `tests/dang.toml` (dormant until imported); the error harness, which
  hardcodes its configs, got a `Lib` `DangModuleDir` config added directly.

Original ordered steps, with exact files/functions from the tree, retained for
reference:

**Step 1 — dang.toml surface (`project.go`).**
- Add `Path string \`toml:"path,omitempty"\`` to `ImportSource`.
- In `resolveImportSource` (project.go:200), add a branch *before* the GraphQL
  ones: if `source.Path != ""`, resolve it against `configDir` to an absolute
  dir, set `ic.DangModuleDir = <abs>`, and `return ic, nil` early — skipping the
  client/schema logic and the final "must specify one of dagger/schema/…" error
  (project.go:285). Error if `path` is combined with any GraphQL field.

**Step 2 — carry it on the config (`ast_declarations.go`).**
- Add `DangModuleDir string` to `ImportConfig` (ast_declarations.go:1000).
  Non-empty ⇒ native Dang module, not a schema.

**Step 3 — the loader (new file, e.g. `import_dang.go`).**
- `loadDangModuleTypes(ctx, dir) (TypeScope, []*FileBlock, error)`:
  `parseDirBlocks(ctx, dir)` → `typeScope := NewPreludeTypeScope(name)` →
  **`subCtx := withModuleScope(ctx, &moduleScope{label: "module:"+dir})`** (THE
  line that makes visibility work) → `InferDirectoryFiles(subCtx, blocks,
  typeScope, fresh)`. Return typeScope + blocks (blocks are needed at Eval).
- A ctx-scoped Dang-module cache keyed by **resolved abs dir**, mirroring
  `WithSchemaModuleCache`/`sharedImportModule`/`cacheImportModule` (a second
  `sync.Map`), storing the typeScope, the blocks, and (lazily) the evaluated
  valueScope. Same-dir imports MUST return the same typeScope so types unify and
  `homeModule` is identical across importers.

**Step 4 — `ImportDecl.Infer` branch (ast_declarations.go:1082).**
- After `loadImportConfig`, if `config.DangModuleDir != ""`: consult/populate the
  Dang-module cache (load on miss via Step 3); set `i.inferred = typeScope`;
  stash `dir`/`blocks` on the `ImportDecl` (add fields, alongside
  `client`/`schema`) for Eval; `installImportedTypeScope(env, i.Name.Name,
  i.inferred)`; `return NonNull(i.inferred)`. Else: existing schema path.

**Step 5 — `ImportDecl.Eval` branch (ast_declarations.go:1124).**
- If Dang module: get-or-eval the module's valueScope **once** (cache):
  `vs := NewValueScope(typeScope); evaluateDirectoryFiles(ctx, blocks, vs)` — runs
  all the module's code — then `installImportedValueScope(scope, i.Name.Name, vs)`.
  Else: existing `ValueScopeFromSchema` path.

**Step 6 — verify (the payoff; see testing skill for error goldens).**
- Fixture module A (a dir) with a `pub type` exposing a `pub` member and a `let`
  member. A consumer with `dang.toml` `[imports.Lib] path = "../lib"` (or
  similar) importing it.
- Positive: `Lib.Thing().pubMember` works. Negative (error golden):
  `Lib.Thing().letMember` → "…is a private member of Thing and cannot be accessed
  from another module" — the cross-user-module case that was unreachable
  (type-statics.md §8). Confirm A's type kept `homeModule = A` (not re-stamped).
- Harness: `tests/` runs `test_*.dang` and `test_*/` dirs via RunFile/RunDir with
  dang.toml imports resolved (integration_test.go). A `test_import_*/` directory
  fixture with its own `dang.toml` is the vehicle — but first CONFIRM `RunDir`
  resolves a nested dang.toml's `[imports.X] path` relative to that dir (it calls
  `ensureProjectImports`/`FindProjectConfig`; check the search root).

**Risks / things to check while implementing.**
- *Two-phase carry*: schema imports stash `i.inferred`/`i.client`/`i.schema` on
  the node and reuse across passes; do the same (dir + blocks) so the LSP's
  repeated passes reuse the cache (`WithSchemaModuleCache` semantics).
- *Don't re-stamp*: `InferDirectoryFiles` for A MUST run under A's `moduleScope`,
  never the importer's — otherwise `homeModule` collapses and the negative test
  fails open. This is the one subtle correctness point.
- *Eval order*: A's code runs at the importer's Eval of `import A`; imports are
  partitioned and evaluated first per file (`evaluateDirectoryFiles`), so A's
  values exist before the importer uses them.
- *Cycles*: A imports B imports A. Phase 1 can punt (mark in-progress in the
  cache and error), but at least don't infinite-loop.
- *Prelude*: `NewPreludeTypeScope` re-triggers cached `loadPrelude`; A and the
  importer share the one prelude — fine.

## 8. Decisions

- **A. Library/program split** — DECIDED: none. Run all the module's code on
  import (§2). Simplest; revisit only if side effects bite.
- **B. Module self-identity** (`[module] path` in a library's `dang.toml`) —
  open, but only matters at Phase 3 (MVS/cycles). Phase 1 identifies a module by
  its resolved local path.
- **C. How far now** — DECIDED: **Phase 1 only** (local path imports), to unblock
  the visibility test, then reassess.
