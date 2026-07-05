# Design: native Dang module imports

Status: **Phase 1 DONE — local path imports shipped** (see §7.1, all steps
landed). `[imports.X] path = "./dir"` now binds X to a native Dang module
compiled from source, with its own `pub`/`let` API. The cross-USER-module `let`
visibility boundary is finally reachable and tested end to end
(`tests/importlib/` + `tests/test_import_dang_module.dang` positive;
`tests/errors/import_dang_private_member.dang` negative golden). **Instance
identity is now decided AND shipped: per-module** (per-`dang.toml`) — each module
gets its own copy of a dependency, nothing unifies across the module boundary;
transitive local imports and cycle detection landed with it, tested in
conventional style (`tests/test_import_transitive/` positive,
`tests/errors/import_foreign_type_mismatch.dang` negative golden, cycle Go test;
see §9), plus a diagnostics/hardening pass (clearer cross-module mismatch message,
deeper + self import-cycle tests). Commits: `16a7def1` (per-module identity),
`56f43aab` + `2704a260` (mismatch diagnostic), `0b963b0e` (cycle tests).

**Next up is Phase 2 (remote VCS refs) — the plan is in §10**, and the direction
is now locked: adopt Dagger's exact module-ref syntax and **extract Dagger's ref
resolution code** (`engine/vcs` + `core/gitref`) into a local package rather than
reimplement it. **No Go-style MVS** (per-module makes version reconciliation
moot). Decisions locked: run all module code on import (no library/program split),
local path imports first, per-module instance identity, Dagger ref syntax +
extracted resolver.

Implementation surfaced one latent bug, now fixed: `ObjectDecl.Hoist`
(fields.go) added a `type`'s constructor scheme to the type env **without**
setting its visibility, so it defaulted to private and never re-exported across
an import boundary — invisible until a user type was first imported. The eval
side already bound the constructor with `c.Visibility`; the infer side now
matches. Everything else reused existing machinery verbatim.

Modeled on **Go modules** (decentralized, VCS-backed, no central registry) and
**Dagger module refs** (`host/user/repo[/subpath]@version`, piggybacking on VCS
metadata) — and we take Dagger's ref machinery *literally*, extracting its
resolver rather than reimplementing (§3, §10). **No Go-style MVS** — per-module
identity (§9) makes version reconciliation moot. **Non-goal: hosting a package
registry.** Refs resolve directly to VCS; fetching is `git`.

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

# Dang module, VCS ref (Phase 2) — Dagger's exact ref-string syntax,
# host/user/repo[/subpath]@version, parsed by the extracted gitref package (§3).
[imports.Semver]
ref = "github.com/vito/dang-semver/semver@v1.4.0"
```

(The single Dagger-style `ref` string supersedes the earlier split
`git`/`version`/`subdir` sketch: we parse it with Dagger's own parser, so the
input format is whatever that parser accepts — one string, `subpath` and
`@version` inline.)

From the consumer's view all of these are "a namespace of types + values reached
via `X.member`"; it does not care whether X is a GraphQL schema or a Dang
library. The short name is **local to the module that declares it** (imports are
already file/module-scoped), so no global alias namespace and no collision
across modules.

A library module's OWN `dang.toml` carries its own `[imports.*]` (its transitive
deps). A `[module]` header giving its canonical path (like `module github.com/...`
in go.mod) is **probably unnecessary** under the current model — cycle identity
now uses the resolved dir (compile-stack, §9) and there is no MVS/versioning
algorithm that would need it. Left as a minor open item (Decision B, §8); only
add it if a concrete need appears.

```toml
# Likely NOT needed — see Decision B.
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
   `dang.toml`; no fetch. This is the whole Phase 1 (shipped).
2. **VCS ref** (`ref = "host/user/repo[/subpath]@version"`): **DECIDED — do not
   reimplement.** Adopt Dagger's exact ref syntax and **extract Dagger's resolver**
   (see §10 for the extraction steps). Dagger already does the layered resolution
   we sketched — known hosts + dynamic `go-import`/vanity + the git plumbing — in
   two small, largely self-contained packages in the Dagger tree
   (`/home/vito/src/dagger`):
   - **`engine/vcs`** — a fork of Go's `cmd/go/internal/vcs`. Known-host mapping
     plus **dynamic `go-import` meta-tag / vanity** discovery (GET
     `https://<path>?go-get=1`, parse `<meta name="go-import">`). Entry point
     `RepoRootForImportPath(importPath, verbose) (*RepoRoot, error)`;
     `RepoRoot.Repo` is the git clone URL. BSD (Go Authors) — carries its own
     `LICENSE`. Deps: stdlib + `golang.org/x/sys/execabs`.
   - **`core/gitref`** — the thin ref parser/formatter on top:
     `Parse(ctx, refString) (Parsed, error)` and `RefString(...)`. `Parsed` gives
     `RepoRoot.Repo` (git URL), `CloneRef`, `ModVersion`/`HasVersion`, and
     `RepoRootSubdir` (monorepo subdir) — everything the fetch needs.
3. The import **name** in code is a local alias; the canonical identity for the
   cache is the resolved **module path @ commit** (`host/user/repo@<sha>`), never
   the alias. (Under per-module identity this key addresses *bytes on disk*, not a
   shared type instance — see §4/§9.)

## 4. Tough question 2 — remote deps & fetching

- **Fetch** with `git` (shallow fetch of the exact tag/commit) into a
  content-addressed **module cache** (`$DANG_MODCACHE`, default under the XDG
  cache dir), keyed by `module-path@resolved-commit`. Immutable once written; a
  cache hit needs no network.
- **Reproducibility**: `dang.toml` states *intent* (version constraints);
  `dang.lock` records the *resolved* commit SHA + content hash per module (Go's
  go.mod/go.sum split). Present lock ⇒ deterministic, offline-capable builds.
- **Transitive deps**: a fetched module's own `dang.toml` `[imports.*]` are
  recursed into — but this needs **no new code**: once a ref is fetched to a cache
  dir, `moduleImportContext` resolves that module's own `dang.toml` exactly as it
  does for a local module today (§7.1). Cycles are caught by the shipped
  compile-stack (§9), keyed by resolved dir.
- **~~Version selection (MVS)~~ — DROPPED.** Superseded by per-module identity
  (§9): there is **no selection algorithm and no reconciliation**. Each `dang.toml`
  pins exactly what it wants; two modules pinning different versions simply get
  different instances, and pinning the *same* ref still yields separate instances
  (the shared cache dir is bytes on disk, not a shared type). No MVS, no SAT, no
  "one version per module path".
- **No central registry**: everything is VCS + git + (for vanity paths) an HTTP
  `go-get` meta GET, all inherited from the extracted `engine/vcs` (§3). An
  optional GOPROXY-style read-through cache could be added later without changing
  the model.

---

## 5. Integration with the existing import path (grounded in code)

> **Superseded in part by §9 (per-module) and §7.1 (as shipped).** This section
> was written before the identity decision. The bullets on *identity/unification*
> ("key by module-path@version", "types unify across the graph", "compiled once
> per module@version, shared like the prelude") describe the **rejected**
> shared-per-version model. The shipped model is per-module: each importing module
> gets its own instance and foreign types do **not** unify. The `ImportDecl.Infer`
> / `Eval` branch descriptions below are accurate and did land (see §7.1); read
> the identity bullets against §9.

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
   Per-module identity + diagnostics/hardening also DONE (§9).
2. **Phase 2 — VCS refs (NEXT; full plan in §10).** Extract Dagger's `engine/vcs`
   + `core/gitref` (§3) → add a Dagger-style `ref` string to `dang.toml` → resolve
   + shallow-fetch to a content-addressed `$DANG_MODCACHE` dir → hand the dir to
   the **unchanged** `loadDangModule`. Optional follow-up: `dang.lock`.
3. **~~Phase 3 — transitive deps + MVS + vanity~~ — mostly absorbed.** MVS is
   dropped (per-module, §9). Vanity (`go-import`) resolution comes *for free* with
   the extracted `engine/vcs`. Transitive **remote** deps need no new code (a
   fetched module's `dang.toml` resolves like a local one, §4). What may remain:
   `dang.lock`, a GOPROXY-style cache, ref-syntax niceties — all optional polish.

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
  leaning **unnecessary**. It was only for MVS (dropped) and cycle identity (now
  the resolved dir / compile-stack, §9). Modules are identified by resolved local
  path (Phase 1) or `module-path@commit` cache dir (Phase 2). Add only if a
  concrete need surfaces.
- **C. How far now** — DECIDED: **Phase 1 only** (local path imports), to unblock
  the visibility test, then reassess.
- **D. Instance identity** — DECIDED & SHIPPED: **per-module** (per-`dang.toml`).
  See §9.

## 9. Module instance-identity model — DECIDED: per-module (shipped)

Phase 1 shipped native local-path imports (commits `fix(dang): set visibility on
type value bindings` + `feat(dang): native Dang module imports via path`). The
one open question that changed the cache/identity core — **when is an imported
module the *same instance* vs a fresh one?** — is now **decided and
implemented**: the sharing unit is the **module** (a `dang.toml`).

**Settled: no Go-style MVS.** We are NOT doing Minimal Version Selection or
global "one version per module path" reconciliation. This supersedes §4's
version-selection paragraph. Refs will still pin exact versions and `dang.lock`
will still record resolved commits for reproducibility, but there is no selection
algorithm.

**Decision: per-module (per-`dang.toml`) instance identity.** All files of one
module resolve a given import to a single instance, but two *different* modules
each instantiate their own copy of a dependency — even at the same pinned version
/ same directory. Nothing is shared across the module boundary. (The two rejected
alternatives: *shared-per-version* — one instance per `module@version` graph-wide,
types unify across modules; and *strict-per-import* — every `import` statement its
own instance, so even sibling files disagree.)

**Why per-module.** Version reconciliation disappears completely — nothing is
shared across modules, so two modules pinning different versions cannot conflict;
you fetch exactly what each `dang.toml` pins and instantiate it for that module.
Hermetic modules; simplest of the three. Aligns with the intent that behavior is
shared through **interfaces** (loose coupling), not by unifying concrete foreign
types. These import graphs are expected to be small, so the duplication cost is a
non-issue.

**Accepted consequences (all confirmed, not open):**
- *Foreign types don't unify across modules.* App's `Semver` ≠ B's `Semver`,
  same version or not. A module's public API that exposes a dependency's type
  (`bump(v: Semver): Semver`) is used structurally (member access on the returned
  value works) or via a shared **interface**; an explicit cross-instance
  annotation (`let v: Semver = other.bump(...)` where `Semver` is *your* copy)
  will not typecheck. Modules are black boxes with their own vocabulary — as
  intended.
- *No dedup = N copies.* N modules importing one util compile AND run it N times
  (top-level side effects included). Fine at expected scale; opt-in dedup could
  be added later without changing the model.

**Implementation (shipped in this session).** Two changes in
`pkg/dang/import_dang.go`:
1. *Fresh cache per module.* `moduleImportContext` now installs a **fresh**
   Dang-module cache (`WithDangModuleCache`) per compiled module, alongside the
   already-fresh schema cache. So each module resolves its own dependencies into
   its own cache; two modules importing the same directory never share a `*Type`.
   Within-module sharing falls out because all of a module's files infer/eval
   under the one ctx that owns that cache.
2. *Compile-stack cycle detection.* The old in-progress marker lived in the
   shared cache and could not survive the (now per-module) boundary — A→B→A would
   expand a fresh instance every hop, forever. Replaced with a ctx-threaded
   **compile-stack** of module dirs (`compileStackFromContext` / `pushCompileStack`)
   that *does* cross the boundary; `loadDangModule` errors `import cycle detected:
   A -> B -> A` when a dir recurs on the active path. Modules are now cached only
   after a successful compile (no marker), so a failed load leaves nothing to
   retry around.

**Tests — conventional style (`.dang` fixtures, `tests/`).** Dang unification is
**nominal** ("structurally compatible … but not declared as an implementation"),
so per-module identity is fully observable through the type checker — no need to
count side effects. Fixture modules: `tests/importutil/` (leaf `Util`, defines
`Widget` + `double`) and `tests/importcalc/` (`Calc`, imports `Util`, re-exposes
`tripled` and a `Util.Widget`).
- *Positive* — `tests/test_import_transitive/` (language suite). Two files:
  `Calc.tripled` runs `Util.double` across `App -> Calc -> Util` (transitive),
  and a `Util.Widget` produced in one App file is assigned to a `Util.Widget`
  slot in the sibling file — which only typechecks because both files share one
  `Util` instance (within-module sharing; a per-import model would fail nominal
  unification).
- *Negative* — `tests/errors/import_foreign_type_mismatch.dang` + golden. App
  imports `Util` (its own copy) and `Calc` (which imports its own `Util`);
  assigning `Calc.widget` to App's `Util.Widget!` is rejected — the two instances
  of the same directory don't unify across the module boundary. This is the
  cross-module distinctness proof.
- *Cycle* — `TestDangModuleImportCycle` in `tests/errors_test.go`. A directory
  that must **error** fits neither the golden harness (the cycle message embeds
  absolute module dirs) nor the language harness (an error there is a failure),
  so it runs from a temp dir and asserts the message — the same pattern as the
  existing `TestRunDirControlFlowSourceErrors`. A↔B mutual import ⇒ `import cycle
  detected` rather than looping.

**Remaining for Phase 2.** The per-module model is the foundation the remote-ref
phase reuses verbatim: resolve ref → fetch to a content-addressed dir → hand the
dir to the existing `loadDangModule`. Cycle detection already keys on the compile
path (by resolved dir), so it carries over unchanged; when refs land, the stack
key can shift from abs dir to `module-path@commit` if desired. **Full plan: §10.**

---

## 10. Next session — Phase 2 (remote VCS refs): plan

Everything through per-module identity + polish is shipped (status header for
commits). The loader (`loadDangModule`), per-module cache, and compile-stack cycle
detection all key on a **resolved local directory** and do **not** change. Phase 2
only adds a **resolve-ref → fetch → cache-dir** stage in front of them.

**Direction (locked with the user): reuse Dagger's ref machinery verbatim — do
not reimplement.** Adopt Dagger's exact ref *string* syntax
(`host/user/repo[/subpath]@version`) and extract the two Go packages that parse +
resolve it (both in the Dagger tree at `/home/vito/src/dagger`) into local
package(s) under `pkg/` — see §3 for their APIs:
- `engine/vcs` → e.g. `pkg/modref/vcs` (keep its BSD `LICENSE`; deps = stdlib +
  `golang.org/x/sys/execabs`; self-contained).
- `core/gitref` → e.g. `pkg/modref` (deps beyond vcs: `go-git/v5/plumbing/transport`,
  otel `trace`, `dagger/otel-go` telemetry — **`dagger/otel-go` is already a Dang
  dep**; trim the go-git transport use if it turns out cosmetic).
Bring `vcs_test.go` + `gitref_test.go` across too — they pin the ref-syntax and
go-import behavior we're inheriting.

**Ordered steps:**

1. **Extract the packages (self-contained commit, no Dang wiring).** Copy the two
   packages under `pkg/`, rewrite import paths, add `go-git` (and confirm
   `x/sys`) to `go.mod`, and get `Parse` / `RepoRootForImportPath` + their tests
   green in-tree. Verifiable in isolation before touching the compiler.
2. **`dang.toml` ref surface (`project.go`).** Add a Dagger-style `ref` string to
   `ImportSource` (supersedes the split `git`/`version`/`subdir` sketch). In
   `resolveImportSource`, branch beside the `path` one and record the raw ref for
   **lazy** resolution — do NOT fetch here (networked; be lazy like schema
   introspection). Carry it on `ImportConfig` as `DangModuleRef string` (alongside
   `DangModuleDir`).
3. **Resolve + fetch + cache (new file, e.g. `import_dang_fetch.go`).** In
   `ImportDecl.Infer`'s Dang branch, when the config carries a ref and no dir yet:
   `gitref.Parse` → shallow `git` fetch of the resolved commit into
   `$DANG_MODCACHE/<module-path>@<commit>` (XDG cache default; immutable; a hit is
   network-free) → the `RepoRootSubdir` within it is the module dir → pass that
   dir to the **unchanged** `loadDangModule`. Everything downstream (per-module
   cache, cycle stack, visibility) already works from a dir.
4. **`dang.lock` (thin-slice-optional follow-up).** Record resolved commit + a
   content hash per ref (go.mod/go.sum split) for reproducible, offline builds;
   a present lock skips resolution and fetches the pinned commit.

**Per-module consequences for remote (already true, restated):** two modules
pinning the same ref get **separate instances** — bytes shared on disk
(content-addressed), compilation/run per importing module. No MVS, no
reconciliation (§9). Transitive remote deps need no new code (a fetched module's
own `dang.toml` is resolved by `moduleImportContext` like a local one).

**Thin-slice-first.** Steps 1–3 restricted to a known static host (github.com,
which resolves without the go-import HTTP GET) is the fastest working remote
import end-to-end; defer `dang.lock` (4) and lean on the dynamic go-import path
only after static hosts work.
