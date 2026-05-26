# Handoff: idiomatic Haskell-style `Either` in Dang

Goal sketch:

```graphql
union Either[a, b] = Lft[a] | Rgt[b]

pub safeDivide(x: Int!, y: Int!): Either[String!, Int!]! {
  if (y == 0) {
    Lft("divide by zero")
  } else {
    Rgt(x / y)
  }
}

pub describe(r: Either[String!, Int!]!): String! {
  case (r) {
    l: Lft[String!] => "err: " + l.value
    rt: Rgt[Int!] => "ok: " + toString(rt.value)
  }
}
```

Three gaps to close, independent and orderable. Phase 1 and 2 are mechanical and parallel earlier work; phase 3 is a real inference change.

## Phase 1: generic unions

Mirrors what was done for `type` and `interface` in `feat(dang): parameterized type declarations` and `feat(dang): generic interfaces`.

### 1.1 Parser

Update the union grammar in `pkg/dang/dang.peg` to accept optional `[a, b]` after the union name and to parse member references via `NamedType` (not bare `Symbol`):

```peg
Union <- UnionToken _ name:Symbol params:TypeParamList? _ '=' _ first:NamedType rest:(_ '|' _ m:NamedType { return m, nil })* { ... }
```

Run `go generate ./pkg/dang/`.

### 1.2 AST

In `pkg/dang/slots.go`:

```go
type UnionDecl struct {
    ...
    TypeParams []hm.TypeVariable
    Members    []*NamedTypeNode  // was []*Symbol
    ...
}
```

Update `ReferencedSymbols()` to recurse into each member's `ReferencedSymbols()` (since member nodes now carry args that may reference other types).

### 1.3 Hoist pass 0

- Validate duplicate type params via the existing `checkDuplicateTypeParams` helper.
- Set `unionMod.TypeParams = u.TypeParams`.
- Register the union type so other declarations can reference it.

### 1.4 Hoist pass 1 / member resolution

For each member `m *NamedTypeNode`:

- Call `m.Infer(ctx, env, fresh)` to resolve to either a `*Module` (non-generic member) or `*AppliedType` (generic member instantiated with some of the union's type params, e.g. `Lft[a]`).
- Retrieve the underlying `*Module` (call it `memberMod`):
  - For `*Module`: trivial.
  - For `*AppliedType`: take `.Base`.
- Register the union membership: `unionMod.LinkMember(memberMod)` already exists; extend it (or add a sibling method) to also record the **supertype template** — the `*AppliedType` value (or `*Module` if non-generic) keyed by the union it joins.

### 1.5 Supertype template storage

Add a field on `*Module`:

```go
type unionMembership struct {
    Union    *Module
    Template hm.Type  // either *Module (non-generic union) or *AppliedType
}

// in Module:
unionMemberships []unionMembership  // replaces or augments existing `unions []Env`
```

The `Template` is what the member's *Module* "looks like" as a value of the union, with the member's own type params still free. For `Lft[a]` joining `Either[a, b]`:

- `Template = &AppliedType{Base: Either, Args: [a, b]}` where:
  - `a` is `Lft`'s own first TypeParam (the same TypeVariable identity used inside Lft).
  - `b` is `Either`'s second TypeParam, **not bound by Lft**.

### 1.6 `AppliedType.Supertypes` for generic unions

Currently `AppliedType.Supertypes()` walks `Base.Supertypes()` (which today returns the raw interface/union modules) and applies the substitution `{Base.TypeParams[i] → Args[i]}`.

For generic unions, the raw supertype must be the **template**, not the bare `*Module`. Update `Module.Supertypes()` to return templates for union memberships and substituted supertypes for interface memberships. The `AppliedType.Supertypes()` substitution loop already does the right thing:

```go
for i, s := range bases {
    out[i] = s.Apply(subs).(Type)
}
```

This applies `{a → Int!}` (substituting `Lft`'s own param). Union params not bound by the member (`b`) stay as their TypeVariable, which unification then binds locally per call site.

### 1.7 Validation

- A member's arity must match its referenced type's TypeParams.
- A union member's type args must be type variables drawn from the union's `TypeParams`, or concrete types. (Allow `union Either[a, b] = Lft[Int!]` to bind a concrete member arg if needed — that's fine.)
- Reject members referencing the same Base twice with different args (e.g. `Lft[a] | Lft[b]`) — would make case discrimination ambiguous at runtime since runtime types are erased.

### 1.8 Tests

Language tests (`tests/test_generic_union.dang`):

```graphql
type Lft[a] { pub value: a }
type Rgt[b] { pub value: b }
union Either[a, b] = Lft[a] | Rgt[b]

pub e: Either[Int!, String!]! = Lft(42)
pub f: Either[Int!, String!]! = Rgt("ok")
```

Plus a multi-param union, a union with a concrete-bound member, and a use through a function returning the named generic union.

Error tests:

- Duplicate union type param.
- Member arity mismatch: `union Bad[a] = Lft[a, b]`.
- Member binds a type var not in the union's params: `union Bad[a] = Lft[c]` — would currently slip through; needs explicit rejection.
- Same-base duplicates: `union Bad[a, b] = Lft[a] | Lft[b]`.

### 1.9 Formatter

`pkg/dang/format.go`:

- Print `[a, b]` after union name in `formatUnionDecl`.
- Member output already goes through `formatTypeNode` if we hand it a `*NamedTypeNode`; verify and adjust.

## Phase 2: case patterns over applied generic types

### 2.1 Grammar

Change `CaseClause` in `pkg/dang/dang.peg`:

```peg
CaseClause <- ... binding:Symbol _ ColonToken _ pattern:NamedType _ ArrowToken _ expr:Form { ... }
```

(Replace `typeName:Symbol` with `pattern:NamedType`.)

### 2.2 AST

In `pkg/dang/control_flow.go` (or wherever `CaseClause` lives):

```go
type CaseClause struct {
    ...
    TypePattern *NamedTypeNode  // was *Symbol
    ...
}
```

### 2.3 Inference

When inferring a case clause with a type pattern:

- Call `clause.TypePattern.Infer(ctx, env, fresh)` to get either a `*Module` or `*AppliedType`.
- Check that the inferred pattern type is a member of the case's operand type (which must be a union — already validated today).
- Bind `clause.Binding` to the pattern type in the clause's expression scope.

For union-member checking with generics: the operand's type is e.g. `Either[Int!, String!]`. The pattern is `Lft[Int!]`. Check via `hm.Assignable(patternType, operandType)` — this should already work because `AppliedType.Supertypes()` (after Phase 1) returns `Either[Int!, b]` for `Lft[Int!]`, and unification binds `b := String!`.

### 2.4 Runtime matching

Dang's runtime is type-erased — a `ModuleValue` carries a `*Module` (the Base), not the applied args. Type discrimination at runtime can only compare against the Base pointer.

This is sound because the type checker guarantees the value's runtime type satisfies the static pattern (including args). Update the case dispatch in `pkg/dang/control_flow.go` (or wherever case `Eval` lives) to:

- For a pattern that's an `*AppliedType`, compare against the value's `*Module` pointer using `pattern.Base`.
- For a plain `*Module` pattern, compare against the value's `*Module` pointer.

In other words: the runtime match is purely on the Base; the AppliedType args are checked statically and dropped at runtime.

### 2.5 Tests

```graphql
union Either[a, b] = Lft[a] | Rgt[b]

pub describe(r: Either[String!, Int!]!): String! {
  case (r) {
    l: Lft[String!] => "err: " + l.value
    rt: Rgt[Int!] => "ok: " + toString(rt.value)
  }
}
```

Plus:

- Case over a union where the pattern is a `*Module` (non-generic) used inside a generic union.
- Else branch.
- Exhaustiveness check (today's mechanism — does it need updating?).

Error tests:

- Pattern not a union member: `case (r) { x: SomeOther => ... }` where `SomeOther` isn't in the union.
- Pattern with wrong args: `case (r) { l: Lft[Int!] => ... }` against `Either[String!, Int!]`.

## Phase 3: bidirectional union inference in conditionals

Today:

```graphql
pub safeDivide(x: Int!, y: Int!): Lft[String!]! | Rgt[Int!]! {
  if (y == 0) {
    Lft("divide by zero")    # Lft[String!]!
  } else {
    Rgt(x / y)               # Rgt[Int!]!
  }
}
```

Fails because the `if` unifies its two branches against each other before checking against the declared return type. We need to push the expected type *down into each branch*.

### 3.1 Propagation surface

The function's declared return type is known in `FunctionBase.inferFunctionType` (`pkg/dang/ast_declarations.go`). The body inference is `f.Body.Infer(...)`. Add an "expected type" context that the body uses:

```go
// In FunctionBase.inferFunctionType
if explicitRetType != nil {
    bodyCtx = contextWithExpectedType(bodyCtx, definedRet)
}
inferredRet, err := f.Body.Infer(bodyCtx, newEnv, fresh)
```

Then conditionals, blocks, and `return` statements consult this expected type.

### 3.2 Conditional inference

In the if/else inferer (`pkg/dang/control_flow.go` or wherever):

- If there's an expected type from context:
  - Infer each branch with the same expected-type context propagated.
  - Each branch's inferred type is checked against the expected type via `hm.Assignable`.
  - The if-expression's overall type is the expected type (or its actual unified branch types if assignability succeeds without further constraint).
- Otherwise, fall back to current behavior: unify branches against each other.

The key insight: a union return type lets each branch be *any member* of the union. With bidirectional inference, branch 1 can produce `Lft[…]` and branch 2 can produce `Rgt[…]` because each independently satisfies `Lft[…] | Rgt[…]`.

### 3.3 Block return inference

If the function body is a block (`{ ... }`) with a final expression, that expression also gets the expected type. The block's last form's `Infer` should receive the propagated expected type. Same mechanism: context-threaded.

### 3.4 Interaction with early returns

`inferReturnTypeWithEarlyReturns` already checks each `return` against the declared return type. With Phase 3, those checks remain; the new addition is checking the *implicit* trailing-expression return.

### 3.5 Tests

```graphql
union Either[a, b] = Lft[a] | Rgt[b]

# If/else with union return type — both branches return distinct members.
pub safeDivide(x: Int!, y: Int!): Either[String!, Int!]! {
  if (y == 0) {
    Lft("divide by zero")
  } else {
    Rgt(x / y)
  }
}

# Nested conditional in slot annotation context.
pub x: Either[Int!, String!]! = if (true) { Lft(1) } else { Rgt("two") }

# Multi-arm case as the final body expression.
pub classify(n: Int!): Either[String!, Int!]! {
  case (n) {
    0 => Lft("zero")
    else => Rgt(n)
  }
}
```

Error tests:

- A branch that doesn't satisfy any union member: `if (...) { Lft(...) } else { 42 }` against `Either[a, b]`.
- Conditional without an expected type still requires both branches to unify (unchanged behavior): `pub x = if (...) { Lft(1) } else { Rgt("two") }` errors as today.

### 3.6 Risks

- The expected-type context must be cleared at the right boundaries — e.g. lambda bodies don't inherit it from an outer function's return type. Existing block-arg bidirectional inference (in `FunCall.inferBlockArg`) gives a precedent for scoped propagation.
- Don't propagate into expressions whose result is *used*, only into expressions whose result *is* the function/conditional/block return. E.g. `let x = Lft(...)` shouldn't have an expected type forced on it.

## Test plan summary

Run after each phase:

```bash
go test ./pkg/dang/... ./pkg/hm/...
go test -a ./tests/
```

Use `-update` to regenerate goldens. Watch for incidental shifts in fresh-var counters (e.g. additional Greek letters consumed by Instantiate calls on union supertypes).

## Open questions

1. Should `union Foo[+a, -b]` variance annotations be designed alongside or deferred? Recommendation: defer (matches what we did for class generics).
2. Should `case` allow a pattern that's a generic type *without* args, meaning "any application of this base"? E.g. `case (r) { l: Lft => ... }` matching any `Lft[*]`. Probably yes — it falls out naturally from the runtime base-pointer match.
3. What is the precedence of the `|` in union member lists vs. inline union types in expressions? `union Either[a, b] = Lft[a] | Rgt[b]` should be unambiguous because `Lft[a]` is a `NamedType`, not a value expression.

## Suggested phase order

Phases 1 → 2 → 3. Phase 1 alone unlocks named generic unions and inline use; Phase 2 makes case discrimination ergonomic; Phase 3 closes the if/else gap. A user can write Haskell-style Either after Phase 2; Phase 3 is purely about call-site ergonomics.
