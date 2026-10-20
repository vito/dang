---
name: dang-internals
description: Non-obvious invariants in Dang's compiler — multi-pass hoisting, the Fork/Clone/dynamic-scope-cell model, the effect/escape checks (top-level immutability, closure purity, `{{ }}` purity), and `Module.Eq` subtyping. Use when editing `pkg/dang/` infer/eval/scope code, adding a new type-like declaration, adding a builtin that takes a block, or debugging mutation/scoping/purity issues.
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

## `{{ }}` is always parallel

Every `{{ }}` evaluates its fields concurrently, whether it is a standalone
record literal or a chained `recv.{{ … }}` selection, and a selection over a
list fans out across the elements. The shared primitive is `evalParallel`
(eval.go): it runs N tasks concurrently, returns their results in index order,
and **fails fast** — the first error cancels the context handed to the
still-running tasks. Every task is awaited, then one error is chosen
deterministically: the lowest-index *genuine* error, preferring it over the
`context.Canceled` errors our own cancellation may have induced in siblings
(so the real cause is never masked by cancellation noise from a lower index).
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
  and would deadlock. A pending initializer instead runs under its own
  `pendingInit.mu` (held across `Init` to serialize forcing), and its result
  (value *or* error) is memoized. Don't add a path that touches
  `Values`/`Pending` without `Object.mu`.
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
  self-referential force from deadlocking on its own `pendingInit.mu`.

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
   code — top-level forms and blocks inside them, REPL prompts, doc
   snippets — may still reassign. Statics (`self { }` members) can't be
   written from outside at all (`checkStaticWrite`); a static method's
   `self` writes fork per call like an instance method's. Consequence: **named
   functions and methods never write shared state** — their only writes are
   locals and their own `self` fork (methods/constructors own their receiver
   frame, `methodSelfFrame`).
2. **Impure closures can't escape.** A block, nested function, or `&ref` to
   one whose effect writes a binding it doesn't declare may appear only as a
   callee or as an argument for a function-typed parameter. Escape detection
   is source-based: `FunCall` marks the callee and eligible arguments with
   `withAllowedFn`; any other `Symbol`/`FunctionRef` producing a function
   value calls `checkEscape`. A parameter that escapes (or a closure calling
   one) gets a "must be pure" reason. Builtins are assumed to call their
   function args sequentially and never store them (`paramAcceptsFn`;
   `TestBuiltinsNeverStoreFunctions` lists them — extend it when adding one).
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
  marks those so writes resolve to the real declaration.
- `Coerce` wraps field values after inference; look through it
  (`FieldDecl.funDecl`) when you need the `FunDecl`.
- Effects attach to fresh function types only (`attachEffects` on
  `FunTypeNode` results and freshly built signatures); never `SetEffect` a
  shared type — use `WithEffect` for a copy.

## Subtyping via `Module.Eq`

Subtyping is folded into Hindley-Milner unification by making `Module.Eq`
**asymmetric**:

```go
// User.Eq(Node) == true  (User implements Node)
// Node.Eq(User) == false
if otherMod.Kind == InterfaceKind && t.ImplementsInterface(otherMod) {
    return true
}
```

This is intentional — you can pass a `User` where a `Node` is expected
(accessing only `Node` fields is safe), but not the reverse. `Supertypes()`
returns both interfaces and unions for the same reason: it's the hook for
`Assignable` in `hm/unify.go`.

When adding a new subtyping relationship, the asymmetry direction matters:
the *more specific* type's `Eq` should return true against the *more
general* type, not vice versa.

Because `Type.Eq` is asymmetric (and its anonymous branch duck-types
structurally), runtime value equality must **not** route through it.
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
  once at `ScalarDecl.Eval`, frozen after.

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
2. Grammar in `pkg/dang/dang.peg`; run `go generate ./pkg/dang/`.
3. `classifyForms()` case in `pkg/dang/block.go` → `Types`.
4. `Hoist` with the pass 0 / pass 1 split.
5. `ModuleKind` constant in `pkg/dang/env.go` if it's a distinct kind.
6. Formatter case in `pkg/dang/format.go`.
7. Editor syntaxes — see the `editor-syntaxes` skill for the three files
   that need keyword updates.
