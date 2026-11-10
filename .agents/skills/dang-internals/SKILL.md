---
name: dang-internals
description: Non-obvious invariants in Dang's compiler — multi-pass hoisting, the Fork/Clone/dynamic-scope-cell model, the effect/escape checks (top-level immutability, closure purity, `{{ }}` purity), `Module.Eq` subtyping, and evaluation cancellation. Use when editing `pkg/dang/` infer/eval/scope code, adding a new type-like declaration, adding a builtin that takes a block, adding a path that evaluates user code or caches its result, or debugging mutation/scoping/purity/cancellation issues.
---

# Dang Internals — Hoisting and Scoping

The non-obvious invariants in `pkg/dang/`: how the compiler resolves forward
references (multi-pass hoisting), how copy-on-write mutation interacts with
closures (shared vs fresh dynamic-scope cells), and how the compile-time
effect checks keep shared writes out of escaping closures and `{{ }}` fields.

## Multi-pass hoisting

Top-level type-like declarations are hoisted before bodies are inferred, so
mutual references and out-of-order declarations work. Hoisting runs in two
passes.

### Form classification

In `pkg/dang/block.go`, `classifyForms()` splits a block's forms into
`Types`, `Functions`, etc. **Anything that introduces a named type must be
classified as a Type**, or downstream code that references it during the
type phase will fail with "not found":

```go
case *ClassDecl, *InterfaceDecl, *UnionDecl, *ScalarDecl:
    classified.Types = append(classified.Types, f)
```

When adding a new type-like declaration kind, this is the first place to
edit. Symptom of missing it: forward references work in isolation but break
when the declaration order shifts.

### Pass 0 vs pass 1

`Hoist(ctx, env, fresh, pass int)` is called twice on each Type form.

- **Pass 0** — register the *name*. Create the module, add it to the type
  environment, register a scheme. **Do not** populate fields, link
  implementations, or validate — those types may not exist yet.
- **Pass 1** — populate body, link interface implementations / union
  members, run validation (interface-implements, union-member-is-object).

`InterfaceDecl.Hoist` and `ClassDecl.Hoist` follow this exact split. New
type-like decls should too.

### The "hoist vs infer timing" gotcha

If forward references break, you're probably accessing a type during pass 0
of *another* declaration before that type's pass 0 has run, or trying to
read interface relationships before pass 1. Move the work to pass 1, or to
`Infer` if it can wait that long.

## Scoping: Clone, Fork, and the dynamic-scope cell

The eval environment has two distinct scoping mechanisms and a separate
dynamic-scope slot for `self`. Mixing them up causes subtle mutation bugs.

### Two env operations

| Op | What it does | When |
|---|---|---|
| `Clone()` | New scope frame, same dynamic-scope cell | Entering a block or function call (lexical scoping). Assignments walk outward via `Reassign()`. |
| `Fork()` | Execution boundary, same dynamic-scope cell | Entering a method body. Reassignments stay local — caller's reference is unaffected. |

### Two dynamic-scope ops

`self` doesn't live in lexical scope — it's a separate cell on the env.

| Op | What it does | When |
|---|---|---|
| `NewDynamicScope(v)` | Creates a **fresh** cell with `v` as `self` | Entering a method (`BoundMethod.Call`) or constructor (`ConstructorFunction.Call`) |
| `SetDynamicScope(v)` | Updates the existing shared cell | `self.field = value` (copy-on-write assignment) |

### Why the cell must be shared within a method

When a closure (block argument) runs inside a method, each invocation clones
the captured env. If those clones each had their own `self` cell, mutations
in one iteration would be invisible to the next:

```dang
type Builder {
  items: [String!]! = []
  addAll(source: [String!]!): Builder! {
    source.each { item => self.items += [item] }   # must accumulate
    self
  }
}
```

`Clone` and `Fork` both share the dynamic-scope cell (a `*DynamicScope`
pointer). Only `NewDynamicScope` creates a fresh one. So everything inside
a single method call — closures, nested `.each`, user blocks, and nested
functions declared in the body (`FunctionValue.Nested`, which run on a derive
of their closure rather than through `BoundMethod.Call`) — sees the same
`self`, while distinct method calls are isolated.

### Whose `self` a function runs against

Always the `self` where it was written, never the caller's:

- Blocks and nested functions close over their scope, `self` cell included.
- A method referenced by bare name (`helper(1)`, `&helper`) or through
  `self` (`&self.helper`) is bound to the referencing scope
  (`FunctionValue.SelfScope`, see `bindLexicalSelf`); calls dispatch on that
  scope's `self`, read at call time. So a ref stored on another object and
  invoked from its methods still runs against the object it came from.
- `&obj.helper` with any other receiver is a `BoundMethod` on that value;
  values are immutable, so binding it early is equivalent.

### Copy-on-write field assignment

`obj.a.b.c = v` clones every object on the path, sets the leaf, and
reassigns the root binding. Sibling fields not on the path are shared, not
deep-copied. When the root is `self`, the new clone replaces the dynamic
scope via `SetDynamicScope` — that's how methods "mutate" the receiver
without aliasing back to the caller.

### Bare vs `self.` field assignment in methods

Both fork `self` and produce the same result:

- `self.field = value` takes the copy-on-write path explicitly.
- Bare `field = value` resolves through the method's outermost frame
  (`newSelfFrame`, `selfParent`), whose effective parent is the *current*
  value of the `self` cell rather than a fixed receiver frame
  (`Object.nextFrame`). So bare reads see `self.x = v` writes, and a bare
  write that reaches a field of `self` forks it (`forkWithField`) and puts
  the fork in the cell. Receiver frames are never written in place — an
  alias of `self` (`let r = self`, a stored or returned `self`) keeps the
  generation it captured.
- A local/arg shadowing the field is found first and rebound, no fork.

Use `self.` when a parameter shadows a field name (`new(x: Int!) { self.x = x }`).

### Builtins live in a parent frame

`NewValueScope` binds builtin functions and the prelude's bindings in a
`builtins` frame, the builtin imports in a frame derived from it (also marked
`builtins`), and returns a derive of that, so a module's own declarations go
in a frame *of their own* and shadow builtins (`let loop = 1`,
`pub toString(...)`) and builtin imports (`type JSON {...}`), in every file of
the module. Never bind builtins into the module frame itself: `FieldDecl.Eval`
skips names its frame already binds, so a same-named declaration would be
silently dropped at eval while inference still used it. Copy-on-write treats
the builtins frames as roots.

The **builtin imports** (`builtin_imports.go`) are the static modules
(`StaticModules`: `JSON`, `YAML`, `TOML`, `Random`, `UUID`), installed by the
import installer as qualified-only imports
(`installBuiltinImportType`/`installQualifiedImportValueScope`) before
a module's explicit imports, wherever those go. Each name is the host as a
type and, as a value, its namespace (`builtinNamespaceType`): a namespace
module such as `Random` is both; a builtin **scalar** host (`JSON`, `YAML`,
`TOML`: the scalar of valid text in that format, validated by a Go-native
`new()` hook, `SetGoScalarHook`) keeps its statics on its meta-type and its
`Statics()` object, as any type's name does, so `JSON.encode` resolves
static-first (`Select.infer`) and `JSON!` is the scalar. A builtin import's
name always resolves to the module's declaration of it if any, else the
explicit (or auto-) import of that name, else the builtin import:

- Single file and REPL (imports go in the module frame):
  `NewPreludeTypeScope` returns `module → builtin imports → prelude`, and
  `NewValueScope` mirrors it.
- Directory modules and `LoadDir` sessions (imports go in a per-file frame
  looked up after the module's declarations): each file's import frame gets
  the builtin imports first (`newFileImportsScope`,
  `installBuiltinImportValues` in `evaluateDirectory` and `NewSession`), and
  the module's own chain has none (`withoutBuiltinImports` in
  `prepareFileScopes`, `newDirectoryValueScope`).

They carry a qualified import origin, are not in `Prelude`, and an import's
namespace (schema or native module) never holds them.

Rules that ride on this:

- **Unqualified imports never shadow builtins** (`isBuiltinValueName` in
  `import_install.go`, for inference *and* eval — they must agree); the
  qualified name still reaches the import.
- **Unqualified imports never override an import's namespace**, in the
  import's frame or one it resolves through (a builtin import, or an import
  around a nested block's): `resolvesToImportNamespace` at inference, from
  value origins, and `bindsImportNamespace` at eval, from the
  `Object.importNamespaces` marks `installQualifiedImportValueScope` sets.
  Types get it from `installUnqualifiedImportTypesFromModule`'s `NamedType`
  check. No name is special-cased.
- **Code the parser synthesizes never names a builtin directly**, since a
  user binding may shadow it: it uses a `$`-alias from `builtinAliases`
  (see the `language-versions` skill, gate checklist step 3).

## `{{ }}` is always parallel

Every `{{ }}` evaluates its fields concurrently, whether it is a standalone
record literal or a chained `recv.{{ … }}` selection, and a selection over a
list fans out across the elements. The shared primitive is `evalParallel`
(eval.go): it runs N tasks concurrently, returns their results in index order,
and **fails fast** — the first error cancels the context handed to the
still-running tasks, with the cause `errSiblingFailed`. That cancellation is
for in-flight I/O (GraphQL queries) only: the interpreter's checkpoint (see
"Cancellation" below) ignores it unless the context the `{{ }}` was evaluated
under is done too (`parallelTask`), so pure Dang code in the other tasks runs
to completion. Every task is awaited, then one error is chosen
deterministically: the lowest-index *genuine* error, preferring it over the
noise our own cancellation may have induced in siblings — errors wrapping
`context.Canceled` or `errSiblingFailed`, and `CancelledError`s (when the whole
evaluation is cancelled, every task reports one) — so the real cause is never
masked by cancellation noise from a lower index.
`n <= 1` runs inline. Callers fork the scope per task (`forkTaskScope`: a sealed
`Derive(true)` plus a **task-private `self` cell** seeded with the enclosing
`self`) so a task's incidental writes — bare or `self.x = v` — stay private
while the receiver and lexical scope are only read. Without the private cell, a
`self.x = v` in a task would write the enclosing method's shared cell (a race,
and a leak past the `{{ }}`). Name resolution from the task frame threads that
private cell (see `nextFrame`), and a sealed frame still forks `self` for a
bare *field* write (`resolvesToSelfField`) while keeping local/arg writes
local, so bare and `self.` forms see each other inside a task exactly as in a
method body. Record-literal fields use `recordFieldScope` (block.go): the record
frame first for siblings, then the private fork, with writes and `self` routed
to the fork. A closure created *outside* the `{{ }}` still writes its own scope
when a task calls it; those writes are unordered across tasks, so the
`DynamicScope` cell is mutex-guarded (leaf lock) and field writes install their
fork with a compare-and-swap. Checked programs never do this: the compiler
rejects shared writes from `{{ }}` fields (see "Effects and escapes" below), so
the locks are a memory-safety backstop, not a semantics.

The selection paths (`ObjectSelection.evalFieldsBySelection`, the `ListValue`
case of `evalSelectionOnValue`, and the inline-fragment-over-list case) are thin
wrappers over `evalParallel`. The GraphQL receiver stays special-cased: one
batched query, parallelism handled server-side.

### Object literals: parallel lazy evaluation

`ObjectLiteral.Eval` does not evaluate fields top-to-bottom. Each field is
installed as a lazy initializer in the new object (`BindLazy`) and all are
forced concurrently (via `evalParallel`). Dependency order is *emergent*:
forcing a field that references a sibling forces that sibling first and shares
the single result, so independent fields run in parallel while dependents wait.
`force` publishes each value, so the object ends up with every field regardless
of completion order.

Multi-field selection differs in one way: its fields are independent reads off
the same receiver, so there is no inter-field dependency graph — `evalParallel`
fans them out directly rather than through `BindLazy`.

Two things to preserve when touching this:

- **`Object` is concurrency-safe by necessity.** `Lookup`/`force`/`Bind` and
  the other map accessors take `Object.mu` only for brief snapshots/commits —
  never across `Init`, which runs arbitrary user code that re-enters the scope
  and would deadlock. A pending initializer runs at most once to completion —
  the first forcer marks it running, later ones wait for its `ready` channel —
  and its result (value *or* error) is memoized; `pendingInit.mu` guards only
  that state, never across `Init` either. The exception is a run **cut short**
  (`cutShort`: the evaluation was cancelled, or a `{{ }}` sibling's failure
  cancelled its I/O): that error says nothing about the binding, so `force`
  abandons the run instead of memoizing it, waking its waiters to retry, and
  the next read runs the initializer again from the start (repeating whatever
  side effects the cut-short run had). A genuine
  error raised while a sibling's cancellation is pending is still memoized.
  Waiters stop waiting when their own evaluation is cancelled. An imported
  module's top-level code (`dangModule.valueScope`) follows the same rules.
  Don't add a path that touches `Values`/`Pending` without `Object.mu`.
- **A field's own name resolves outward.** During a field's evaluation its own
  name is redirected to the enclosing scope, so `users: users.{{...}}` reads
  the outer `users` instead of recursing into itself. Siblings still see the
  field. (The layered predecessor got this for free by not publishing a field
  until after its turn; the lazy version makes it explicit.)

### Two cycle detectors, two jobs

- **Static (inference).** `objectFieldOrder` and `orderVariablesForInference`
  build a `slotDepGraph` and topologically sort it. This is what lets a field
  reference a later-declared sibling, and a failed sort *is* the cycle. The
  sort — not a separate check — is why the graph exists; cycle detection is its
  byproduct. Object-field cycles are always caught here (a function body can't
  reach object fields), so eval never sees one.
- **Dynamic (eval).** `force` threads a `forceChain` through the context and
  errors if the chain re-enters a pending it is already forcing. This catches
  cycles the static graph can't see — module-level cycles routed through an
  auto-called function (`module_lazy_cycle`) — and is what stops a
  self-referential force from waiting for itself. A cycle split across `{{ }}`
  fields (field 1 runs `a` and waits for `b`, field 2 runs `b` and reads `a`)
  is invisible to any one chain, so a force about to wait first walks the
  wait-for graph (`awaitInit`: edges from each initializer on a blocked chain
  to what it waits for) and errors if that leads back to its own chain.
  Goroutines of a `{{ }}` inside an initializer inherit its chain, so they
  count as part of it. Which force detects such a cycle depends on
  scheduling, so the binding the error names — and whether a `rescue` in one
  of the initializers catches it — does too.

## Cancellation

A host can stop an evaluation through its context (a timeout or a cancel).
It is checked at one place, `enterCall` → `checkpoint` (cancel.go): every
evaluation of a user-defined body goes through it (function, method and
constructor calls, every block invocation, so `loop` iterations and
block-taking builtins), and code between two calls is bounded. Keep it that
way — a new path evaluating a user body must go through `enterCall`, not add
checks of its own.

- **Cheap by construction.** The fast path is one non-blocking `ctx.Err()`.
  It stays O(1) at any recursion depth because `frameContext` answers
  `Done`/`Err`/`Deadline` from `src`, the nearest non-frame context below it,
  instead of delegating frame by frame.
- **Cancellation aborts.** It surfaces as a `*CancelledError` (unwrapping to
  the context's error and cause), which is never rescued (`isCancelled`), and
  `RescueExpr` also refuses to rescue anything once `evalCancelled(ctx)` —
  whatever failed (a call or query cut short) failed because of it.
  Otherwise `loop { x rescue null }` would spin forever.
- **`{{ }}` sibling cancellation is not an abort.** See `evalParallel` above:
  `cancellation` ignores a task context done with `errSiblingFailed` unless
  `parallelTask.watch` is done too. Use the task's own context for its cause
  (`parallelTask.ctx`); `context.Cause(ctx)` on a frame context walks every
  frame.
- **Never memoize a cancellation.** Lazy initializers and imported modules
  abandon cut-short runs (see "Object literals" above); anything else that
  caches an evaluation's outcome must do the same (`cutShort`).

## Effects and escapes (compile-time purity)

Three checks, layered so that none needs to track function values through
storage. Files: `purity.go` (layer 1), `effects.go` (layer 2 and the shared
machinery), `fields_purity.go` (layer 3). `hm.FunctionType` carries an opaque
`hm.Effect` that hm only copies through `Apply`/`Normalize` (`Eq` ignores it);
Dang's `*Effect` is either a **closure effect** (a function body: what it
writes outside itself, which outer parameters it calls, any non-local
`return`/`break`) or a **parameter effect variable** (a function-typed param,
with "must be pure" reasons once discovered).

1. **Top-level bindings are immutable from functions.** `checkTopLevelWrite`
   rejects an assignment whose root resolves (`resolveBinding`) to the
   top-level frame (or a frame it extends) when inferring inside a function,
   method, constructor or type body (`withFunctionBody`). Top-level script
   code — top-level statements and blocks inside them, at the REPL prompt and
   in doc snippets too — may still reassign; declarations' initializers may
   not (see below). Statics (`self { }` members) can't be
   written from outside at all (`checkStaticWrite`); a static method's
   `self` writes fork per call like an instance method's. Consequence: **named
   functions and methods never write shared state** — their only writes are
   locals and their own `self` fork (methods/constructors own their receiver
   frame, `methodSelfFrame`). For a type (object or scalar) declared inside a
   function body that takes one more rule: its code (methods, `new()`, field
   defaults, statics)
   is **sealed** (`effFrame.sealed`, `withTypeBodyEffFrame`) — it can't write
   or call anything writing outside the type, and an outer block param it
   calls must be pure — since its values can be stored, returned and used
   concurrently like any other.
   Top-level declarations are lazy fields forced on first read, maybe inside a
   `{{ }}` field, and all run before any top-level statement. Two syntactic
   rules (`toplevel_order.go`) make that safe without following function
   values: a declaration's initializer (blocks and fields written in it
   included) may not write any top-level binding (`checkInitializerWrite`,
   from `absorbWrite`), so initializers forced concurrently share no mutable
   state; and a binding some top-level statement writes (W, collected by
   `noteStatementWrite`; `box.n = v` writes `box`) may be read only by
   statement code — not by an initializer, nor by a function, method or type
   body, which may run during initialization (`noteTopRead` from
   `Symbol.Infer`, checked by the solver once statements are inferred). A
   top-level form is marked a declaration or statement by
   `withTopDecl`/`withTopStatement`. W lives on the solver: one directory
   module, or one REPL/literate entry.
2. **Impure closures can't escape.** A block, nested function, or `&ref` to
   one whose effect writes a binding it doesn't declare may appear only as a
   callee or as an argument for a function-typed parameter. Escape detection
   is source-based: `FunCall` marks the callee and eligible arguments with
   `withAllowedFn`; any other `Symbol`/`FunctionRef` producing a function
   value calls `checkEscape`. A parameter that escapes (or a closure calling
   one) gets a "must be pure" reason. Builtins are assumed to call their
   function args sequentially and never store them (`paramAcceptsFn`;
   `TestBuiltinsNeverStoreFunctions` lists them — extend it when adding one).
   A function's argument defaults count as its own effect (its effect frame
   is entered before they're inferred): they run when it is called.
   **Field defaults** need no escape check of their own, because they are
   scoped like constructor parameter defaults (`field_defaults.go`): while a
   type body's non-method forms are inferred (`withTypeBody`, carrying
   `unavailableInDefaults`; methods leave it via `withoutTypeBody` in
   `inferFunctionType`, and `new()`'s body never enters it), `self`
   (`checkDefaultSelf`) and a bare name resolving to one of the type's methods
   or computed fields, or, with an explicit `new()`, to a field without a
   default (`checkDefaultName`), are compile errors, anywhere inside the
   default, and so is assigning one of the type's fields (`checkDefaultWrite`,
   from `Reassignment.Infer`). `new()`'s parameter defaults get the same
   checks (`withNewParams` in `inferNewConstructor`), and at runtime
   `ConstructorFunction.Call` evaluates them over the instance after its field
   defaults, with earlier params on top. A `self { }` block is marked with
   `withStaticsBody`: only the write check applies there. Code of a type
   declared in such a body (its methods included)
   stays under the outer body's scope, since it is created while the outer
   object is built. A `{{ }}` selection's field names belong to the receiver,
   so `inferFieldType` resolves them outside every type body
   (`outsideTypeBodies`), and infers its arguments in the enclosing scope,
   where they are evaluated. What a default reads
   is then always a value, so closures created there are ordinary closures.
3. **`{{ }}` fields.** Each field is an `effFrame{field: true}`. Inside a field:
   a write to a binding not declared within it is an error
   (`checkFieldWrite`/`declaredWithin`; blocks written directly in a field
   register their scope with it); calling a closure created outside the field
   that writes anything is an error; calling a parameter makes it "must be
   pure" (concurrently).

Effects are computed **in source order** on a stack of `effFrame`s carried in
ctx (`withEffFrame` for bodies, `withFieldFrame` for fields): writes
(`absorbWrite`) and calls (`absorbCall`) land in the innermost body frame,
dropping targets it owns (`effFrame.inside` walks `*Type.Parent`). Nested
functions aren't hoisted, so a closure's effect is complete before anything
can reference it — no fixed point. What can't be decided in place is whether
an argument meets a parameter's "must be pure" requirement, because hoisting
infers callers before callees: `effectArg` records bindings on the
`effectSolver` and `solve()` propagates requirements through forwarded
parameters (a monotone boolean propagation) and reports violations, including
blocks with non-local `return`/`break` that may escape (this replaced the old
runtime "expired frame" checks).

**Every inference entry point must solve.** `Infer`, `InferDirectoryFiles(Focused)`
and `InferFormsWithPhases` install a solver when ctx has none and solve it at
the end; nested calls reuse the outer one. `InferTopLevel` is
`InferFormsWithPhases` plus marking the top-level frame — use it for REPL-like
sessions (REPL, playground, literate docs). Errors found during inference
(escapes, field violations) go through `reportEffectError` so they're
reported with the deferred ones without failing the expression's type.

Gotchas:
- Flow-sensitive narrowing re-adds a name to a clone frame; `Type.narrowed`
  marks those so writes resolve to the real declaration, and
  `Type.narrowings` keeps the refinement itself (its binding, unrefined type
  and log tick). A refinement holds from its null check (the tick
  `Equality`/`Inequality` were inferred at) until something may null its
  binding: the solver's `narrowLog` (narrowlog.go) records, in inference
  order, nullable assignments (`noteAssignment`, including `self.x` for bare
  `x`; a write in a nested function counts only where it's called, via the
  effect's `nullTargets`) and calls of function parameters (which may write
  anything declared outside the parameter's function). `Symbol.Infer` reads
  through `readNarrowed`, which falls back to the unrefined type once
  invalidated, skipping writes in a sibling `if` branch or a branch that
  returns (`skipBranch`). Closures (block args, nested functions)
  record their reads of outer refinements on their `effFrame`;
  `exitNarrowBody` checks them against the body's own writes (a re-run) and
  `solve` against later writes for nested functions (until their block
  scope ends) and escaping blocks.
- `Coerce` wraps field values after inference; look through it
  (`FieldDecl.funDecl`) when you need the `FunDecl`.
- Effects attach to fresh function types only (`attachEffects` on
  `FunTypeNode` results and freshly built signatures); never `SetEffect` a
  shared type — use `WithEffect` for a copy.

## Subtyping via `Supertypes` and `hm.Assignable`

`Type.Eq` is plain equality: named types compare by pointer, anonymous
records structurally — and only against other anonymous records (or a
GraphQL input object, which is plain data). A record of a named type's
shape is *not* that type. Subtyping lives in `hm.Assignable`
(`hm/unify.go`): after `Eq` fails it walks `have.Supertypes()`, which for
an object returns the interfaces it implements and the unions it belongs to
(and for an interface, its parent interfaces). So a `User` passes where a
`Node` is expected, never the reverse. When adding a subtyping
relationship, the *more specific* type lists the more general one among its
`Supertypes()`.

Function types are checked by `assignableFunction`: contravariant in the
argument record and the block, covariant in the result, without value-level
coercions, and every pair rechecked under the composed substitution (a
variable bound on both sides of the arrow must fit both). Parameter names
are part of a function type: records unify component-wise only when their
keys match in order (`hm.Labelled`, `RecordType.SameLabels`), because calls
may name arguments and arguments bind by name (a positional argument binds
by the name inference resolved against the callee's static type). A block
takes on the names of the signature it's passed for (`BlockArg`
`ExpectedParamNames`, bound to its own names through
`FunctionValue.LocalArgs`).

A type's *name* used as a value is typed as its meta-type
(`Type.MetaType()`, `typeNameValueType`/`enumNamespaceType`), never as an
instance: interfaces, unions and custom scalars without `new()` (whose meta
holds their statics), and enums (whose meta holds copies of their values
and `values`; an enum value itself has no members). The meta-type prints as
the type's name, so diagnostics say which is meant; completion/hover look
through it with `InstanceType()`. Create meta-types while the declaring
scope is private to one goroutine; at eval use `namespaceModuleOf`, which
never creates one.

Runtime value equality must **not** route through `Type.Eq`.
`objectsEqual` (`ast.go`, behind `valuesEqual`'s `*Object` case, used by
`==`/`case`/`contains`/`uniq`) instead gates on the module directly: both
anonymous → structural `AsRecord().Eq`; either named → **pointer** comparison
of the `*Type`s. That keeps `==` commutative (a named-vs-anonymous compare can't
flip on operand order) and nominal (distinct `*Type`s, even same-named ones
from different namespaces, never match). It then compares stored data fields via
`lookupValue` **without forcing** — pending initializers and computed `{ }`
members (function-typed in the module) are skipped, so the comparison stays pure
(no `ctx`, no I/O, no error path). See issue #150.

## Scalar bodies and the Dang-source prelude

Scalar declarations may carry a body (`scalar Path { new(...) {...} ... }`,
`ScalarDecl` in `fields.go`). Non-obvious invariants:

- **Receiver is a value, not a scope.** A `ScalarValue` is not a
  `ValueScope`, so scalar methods dispatch through `BoundScalarMethod`
  (eval.go), which `EnterSelf`s the raw value into a **fresh** cell on a
  scope derived from the method's closure. Never `MutateSelf` a scope
  derived from a scalar method's closure — for prelude scalars that closure
  is process-shared. The naked-self-call seam in `FunctionValue.Call` has a
  `ScalarValue` arm that re-dispatches bare sibling calls for this reason.
- **The `new()` hook is deliberately `IsDynamic=false`**: the naked-self
  redirect keys on the *caller's* dynamic scope, so a dynamic hook invoked
  inside an object method would rebind to that object. Methods, by
  contrast, are forced `IsDynamic=true` in `ScalarDecl.Eval` so bare
  sibling calls redirect.
- **Hook runs at materialization** (`materializeStringValue` /
  `materializeDecoded`), stored on the `*Type` (`SetScalarHook`) — written
  once at `ScalarDecl.Eval`, frozen after. A builtin scalar's Go-native hook
  (`SetGoScalarHook`: `Regexp`, `JSON`, `YAML`, `TOML`) runs at the same
  boundaries.
- **Scalar → scalar only in an explicit cast.** `hm.Coercible` accepts
  String ↔ custom scalar, and is used at value handoffs too
  (`assignableForValue`), so it must not accept one custom scalar from
  another. `TypeHint.Infer` alone falls back to `scalarCastable` and marks
  its `Coerce` with `ConvertScalars`, whose `convertScalars` turns each
  `ScalarValue` of another scalar into the target's through the target's hook
  (keeping the text, and `Encoded`, when it has none). A hint may be inferred
  more than once (an unannotated field), so it decides the cast from the
  expression under its own `Coerce` every time.
- **Literals are checked statically, comparisons never raise.**
  `checkLiterals` (call args, typed slots, `::`, `==`) rejects a string
  literal an enum doesn't name, or one a builtin scalar's Go-native hook
  rejects (a pure function of the text; a Dang `new()` is never run at
  inference). A template compared with a scalar or enum is a `Coerce` marked
  `Compared`: text the target rejects stays a String and compares as text.

The **Dang-source prelude** (`pkg/dang/prelude/*.dang`, loader in
`prelude.go`) parses/infers/evals once via `sync.Once`, lazily on first
scope construction — NOT at `init()`. Its `FileBlock` must be `Inline`
(declarations land in the chain scope, not a clone). Everything it produces
is frozen and shared process-wide like `Prelude`; tests that stub the
builtin registry must `loadPrelude()` first or the prelude will infer
against an empty registry. Prelude-raised errors render embedded source via
`preludeSource` in `uncaught.go`.

## When adding a new type-like declaration

Checklist:

1. AST node in `pkg/dang/slots.go` (or sibling), with `InferredTypeHolder`.
2. Grammar in `pkg/dang/dang.peg`; run `go generate ./pkg/dang/`. Names go
   through `Id` / `ReservedName` (see "Names" in the `testing` skill).
3. `classifyForms()` case in `pkg/dang/block.go` → `Types`.
4. `Hoist` with the pass 0 / pass 1 split.
5. `ModuleKind` constant in `pkg/dang/env.go` if it's a distinct kind.
6. Formatter case in `pkg/dang/format.go`, writing names with `f.ident` /
   `f.fieldName`, never raw.
7. Editor syntaxes — see the `editor-syntaxes` skill for the three files
   that need keyword updates, and the `language-versions` skill's "adding a
   keyword" checklist for the grammar side.

If a grammar change removes, respells or changes the meaning of existing
syntax, old code must keep parsing behind a version gate — see the
`language-versions` skill.
