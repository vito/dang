---
name: language-versions
description: How Dang's per-project language versions (`dang = "X.Y"` in dang.toml) work, and the checklist for changing the language without breaking old code — version gates in the grammar, desugaring old forms to the current AST, `dang migrate`, migration fixtures, and bumping CurrentVersion. Use whenever a change removes, respells or changes the meaning of existing syntax, reserves a keyword, or when cutting a minor/major release.
---

# Language Versions

A project's `dang.toml` declares the Dang version its source is written for —
the major.minor of a Dang release:

```toml
dang = "2.1"
```

Newer Dang keeps parsing older versions' syntax. That works because of one
rule, which every language change must follow:

> **Every version parses into the same AST.** When a release removes or
> respells a form, the old form stays in the grammar behind a *version gate*,
> and the gated rule's action builds the AST of the current equivalent.

Consequences you get for free, as long as the rule holds:

- the interpreter, type checker and evaluator never branch on the version
- modules of different versions import each other freely
- `dang migrate` needs no per-change code: it parses as the old version and
  formats as the new one (`MigrateSource`)

Code lives in `pkg/dang/version.go` (the `Version` type, gates, desugarings),
`pkg/dang/migrate.go` (`MigrateSource`, `PlanMigrations`, `InitProject`),
`pkg/dang/dang.peg` (gated rules), `pkg/dang/treesitter.go` (keeps tree-sitter
on the current version), `cmd/dang/{migrate,init}.go`.

## First: classify the change

| Change | What to do |
|---|---|
| **Pure addition** (new syntax that didn't parse before, new builtin) | Nothing version-related. |
| **Removes or respells a form** (`for` loops, `.{ }` → `.{{ }}`, comma separators) | Gate it: the old spelling parses before version V and builds the new form's AST. If both spellings must print (a respelling), the formatter prints per version. |
| **Changes meaning of existing code, expressibly** (unmatched `case` raised → now null) | Gate it: before V, the parser *desugars* to explicit current code that keeps the old meaning (adds `else => raise ...`). |
| **Strictness only** (code that compiled now errors; nothing changes meaning) | **No gate.** Old code stops compiling after migrating and is fixed by hand. Say so in the release notes. |
| **Reserves a keyword** (`rescue`) | Old code using it as a name can't be migrated automatically (renaming needs scope info). Either gate the keyword (and every construct using it) or accept the break and document it. Prefer not reserving words. |
| **Changes meaning, not expressible in current syntax** | Stop and rethink: this is the case the design can't absorb. It would need an interpreter branch — don't add one without discussing it. |

If you're unsure which release changed something, check the tags rather than
guessing: `mount` `https://github.com/vito/dang#vX.Y.Z` and read its
`pkg/dang/dang.peg`. (Getting this wrong bakes the wrong meaning into every
dang.toml that declares that version.)

## Checklist: adding a version gate

Let **V** be the major.minor that main will release as (the version that
*changes* the form). Old forms parse when `v.Before(V)`.

1. **Version constant.** If V is newer than `CurrentVersion`, add a
   `VersionX_Y` var in `version.go` *and bump `CurrentVersion` to it in the
   same commit*. A gate on a version newer than `CurrentVersion` can never be
   enabled: `ParseVersion` refuses versions newer than the binary. Never touch
   `DefaultVersion` (see invariants).

2. **Feature method** in `version.go`, named for the feature, never for the
   version:

   ```go
   // forLoops reports whether `for (cond) { ... }` is a loop, as before 2.1.
   func (v Version) forLoops() bool {
   	return v.Before(Version2_1)
   }
   ```

   Never compare versions any other way (`==`, names): always `Before`.

3. **Grammar** in `dang.peg`: a predicate rule plus the gated alternative,
   whose action builds the *current* AST (put the builder in `version.go`):

   ```peg
   ForLoops <- &{ return versionFromStore(c.globalStore).forLoops(), nil }
   ForLoop <- ForLoops ForToken _ '(' _ cond:Form _ ')' _ body:Block {
     return desugarForLoop(cond.(Node), body.(*Block), c.Loc()), nil
   }
   ```

   For a respelling, wrap both spellings in helper rules (see
   `SelectionOpen <- SingleBraceSelection '{' / !SingleBraceSelection "{{"`),
   and use `!Pred` for rules that only exist in the new version (`DotBlockArg`).

4. **The built AST must equal the hand-written current form, field for
   field** (locations aside). `MigrateSource` checks this; a mismatch shows up
   as "migrating to Dang V would change the meaning of line N". Give
   synthesized nodes sensible `Loc`s: the formatter places comments by line,
   so e.g. the synthetic `else` takes the last clause's `Loc`.

5. **Formatter** (only for respellings): print per version from `f.version`,
   through a method like `selectionBraces()` / `fragmentBraces()`. Desugared
   forms need nothing: they're current AST and print as current code.

6. **Tree-sitter** in `treesitter.go`: tree-sitter only knows
   `CurrentVersion`. Add every new predicate rule to `tsPredicates` and every
   helper/old-form-only rule to `tsInlinedRules`. Then regenerate
   (`go generate ./pkg/dang/ ./treesitter/`, or the `generate` generators):
   `treesitter/src/grammar.json` must stay **byte-identical** unless the
   current syntax itself changed. An "Undefined symbol" error from
   `tree-sitter generate` means a predicate isn't registered.

7. **Unit tests** in `pkg/dang/version_test.go`: parse in each version
   (`parseSingleForm(t, src, VersionX_Y)`), check the new version rejects or
   reinterprets the old form, and pin the migration text (`assertMigratesTo`,
   or `MigrateSource` directly).

8. **Migration fixture**: add a script to
   `tests/testdata/migrations/<prev>-to-<V>/before/<name>.dang`, written the
   old way, with `assert`s pinning its behaviour. For a new path, create
   `before/dang.toml` (`dang = "<prev>"`) and `after/dang.toml`
   (`dang = "<V>"`). Regenerate the goldens (`go test ./tests/ -run
   TestDang/TestMigrations -update`, or the `update` Dagger function) and read the
   `after/` file: it's the spec of the migration. `TestMigrations` runs both
   halves, so before and after must behave the same. `before/` dirs are
   excluded from `dang fmt` (`.dagger/main.dang`); don't format them.

9. **Docs** describe current syntax only (`docs/lit/`, the `dang-language`
   skill). Note the change and its version in the release notes, including
   anything strictness-only that old code may trip.

Run `go/packages/tests/test`, `test/treesitter`, `go/packages/generate/stale`
and `fmt/stale` before committing.

## Releasing

- **Patch releases must not change the language** — no gates on patches. A
  version means the syntax of its minor's latest release. (Before versions
  were declared, 2.1.x patches did change syntax; that's why 2.1 means
  2.1.4's syntax and `rescue`-as-a-name has no gate.)
- **Minor/major releases**: `CurrentVersion` must equal the release's
  major.minor by the time it's tagged (it may already, if a gate bumped it).
- Grep `version.go` for gates on the new version and list them in the
  release notes, with `dang migrate` as the upgrade path.

## Invariants (don't break these)

- **`DefaultVersion` is pinned forever** (2.1). It's what a dang.toml without
  the key means (absent an embedder's `ContextWithVersion`); moving it would
  silently change existing projects.
- **The version comes only from the source's path**: nearest `dang.toml`
  walking up, stopping at `.git`; no dang.toml means `CurrentVersion` (or an
  embedder's `ContextWithVersion`). Never from the importer, and never from
  the project config that happens to be on the context. Use `VersionForDir`.
- **`ContextWithVersion` never overrides a version a dang.toml declares**,
  but does apply under a dang.toml without the key (it may predate the key,
  e.g. a pinned Dagger module's import-only dang.toml). It is bound to that
  dang.toml (`bindContextVersion`): it carries into imported modules the
  same dang.toml governs, and is cleared for any other import
  (`importContextVersion`).
- **Running code never writes files**: only `dang init` and `dang migrate`
  stamp `dang = "..."`.
- **The zero `Version` means `CurrentVersion`**, everywhere (parser option,
  formatter, `Before`). `ParseVersion` rejects `0.0` so it can't sneak in.

## Known limits (expected, not bugs)

- `dang fmt` on an old-version file prints desugared forms in current
  spelling (`loop`, the added `else`): formatting works on the shared AST.
- Parse errors in old-version files are positioned by the current tree-sitter
  grammar.
- `MigrateSource` leaves a file untouched when nothing in it is spelled
  differently in the two versions, so most files in a version bump don't
  change.
