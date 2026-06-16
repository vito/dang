---
name: dang-internals
description: Non-obvious invariants in Dang's compiler — multi-pass hoisting, the Fork/Clone/dynamic-scope-cell model, and `Module.Eq` subtyping. Use when editing `pkg/dang/` infer/eval/scope code, adding a new type-like declaration, or debugging mutation/scoping issues.
---

# Dang Internals — Hoisting and Scoping

The two non-obvious invariants in `pkg/dang/`: how the compiler resolves
forward references (multi-pass hoisting) and how copy-on-write mutation
interacts with closures (shared vs fresh dynamic-scope cells).

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
a single method call — closures, nested `.each`, user blocks — sees the
same `self`, while distinct method calls are isolated.

### Copy-on-write field assignment

`obj.a.b.c = v` clones every object on the path, sets the leaf, and
reassigns the root binding. Sibling fields not on the path are shared, not
deep-copied. When the root is `self`, the new clone replaces the dynamic
scope via `SetDynamicScope` — that's how methods "mutate" the receiver
without aliasing back to the caller.

### Bare vs `self.` field assignment in methods

Both work and produce the same result:

- Bare `field = value` reassigns through the Fork (the method body's env
  includes the receiver's fields).
- `self.field = value` takes the copy-on-write path explicitly.

Use `self.` when a parameter shadows a field name (`new(x: Int!) { self.x = x }`).

## Object literals: parallel lazy evaluation

`ObjectLiteral.Eval` does not evaluate fields top-to-bottom. Each field is
installed as a lazy initializer in the new object (`BindLazy`) and all are
forced concurrently. Dependency order is *emergent*: forcing a field that
references a sibling forces that sibling first and shares the single result,
so independent fields run in parallel while dependents wait. `force`
publishes each value, so the object ends up with every field regardless of
completion order. The first failure cancels in-flight siblings, but every
field is awaited and the **lowest-source-index** error is returned, so a
failure is reported deterministically.

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
