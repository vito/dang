# Handoff: parameterized `type` declarations

Goal sketch:

```dang
type Box[a] {
  pub value: a
}

type Either[a, b] {
  pub left: a
  pub right: b
}

type List[a] {
  pub map(&fn(item: a, index: Int!): b): [b]!
}
```

## Feasibility summary

This is feasible, but it is not just a parser change. The current system already has ad-hoc generic functions via free `hm.TypeVariable`s, but named object types are nominal `*Module`s with pointer equality. Parameterized object types need a new notion of "applied nominal type" (`Either[Int!, String!]`) plus some HM hygiene.

The most treacherous parts are:

1. `Module` is both a type and an environment. A generic instance must still answer `SchemeOf("field")`, but with class type parameters substituted.
2. `Module.Eq` is pointer identity for named types. `Box[Int!]` cannot just be a cloned module unless equality is changed/canonicalized.
3. `hm.Assignable` currently uses `reflect.TypeOf` + `Types()` for composite types. A generic `AppliedType` would need constructor-aware unification; otherwise `Box[Int!]` and `Either[Int!]` could accidentally unify if they are the same Go type with the same number of args.
4. `hm.Scheme` exists, but lookups usually ignore `forall`/instantiation. Generic constructors need real scheme instantiation, especially inside generic methods.
5. Fresh type variables currently use the same Latin runes as source-level variables (`a`, `b`, ...), so instantiating `forall a` can collide with an in-scope source `a` unless we reserve a separate fresh namespace.
6. Variance: arbitrary user type parameters should be invariant by default. List is covariant today, but user object type params can appear in method argument positions, so covariance is unsound without variance analysis/annotations.

I would implement this in phases and keep the first cut conservative: generic `type` only, single-letter params, no generic interfaces/unions/implements, no higher-kinded or partial application.

## Current relevant code

- Parser: `pkg/dang/dang.peg`
- Type AST/runtime types: `pkg/dang/types.go`
  - `NamedTypeNode`, `ListTypeNode`, `VariableTypeNode`
  - `ListType` is a special `hm.Type`, not a `Module`
- Nominal types/env: `pkg/dang/env.go`
  - `Module` implements both `Env` and `hm.Type`
  - named module equality is pointer identity in `Module.Eq`
- Type declarations: `pkg/dang/slots.go`
  - `ClassDecl.Hoist` pass 0 registers the name; pass 1 populates constructor/body
  - constructor value currently has type `(...): Class!`
- Function lookup/calls: `pkg/dang/ast_expressions.go`
  - `Symbol.Infer` and `Select.Infer` call `scheme.Type()` directly
  - `FunCall.inferFunctionType` collects substitutions from args and applies them to return/block types
- HM schemes: `pkg/hm/scheme.go`, `pkg/hm/generalize.go`
  - `hm.Instantiate` exists but is not wired into normal symbol/member lookup

## Proposed user-facing syntax

Declaration:

```dang
type Name[a, b] { ... }
```

Type application:

```dang
Name[Int!, String]
```

Keep `[T]` as sugar for the current builtin `ListType{T}`. Later, optionally make `List[T]` parse to the same type.

Initial restrictions:

- Type params are single lowercase letters, matching current `hm.TypeVariable rune` / `VariableTypeNode`.
- A generic type name used without args is an error: `Box` in type position should say `generic type Box requires 1 type argument`.
- A non-generic type used with args is an error.
- Generic `implements` is out of scope for the first pass.
- Type params are invariant unless/until variance is designed.

## Phase 0: HM hygiene needed for generic constructors

This can be done before the parser/type work.

### 0.1 Reserve fresh vars away from source vars

Current source type variables are Latin runes (`a`, `b`, ...), and `inferer.Fresh()` also starts at `a`. That makes scheme instantiation unsafe.

Change fresh generation to use a namespace not produced by source syntax, e.g. Greek/private-use runes from the start. Update both:

- `pkg/dang/infer.go` (`inferer.Fresh`)
- `pkg/hm/generalize.go` (`hm.SimpleFresher.Fresh`), if used in tests/tooling

This may alter some golden error output if fresh vars are printed.

### 0.2 Instantiate schemes on value/member lookup

Add a helper like:

```go
func instantiateScheme(fresh hm.Fresher, scheme *hm.Scheme) hm.Type {
  return hm.Instantiate(fresh, scheme)
}
```

Use it in:

- `Symbol.Infer`
- `Select.Infer` for normal Env member lookup
- `Select.Infer` when receiver is nil

Do **not** reject non-monomorphic schemes in `Select.Infer`; after instantiation the result is usable. Leave record argument field schemes monomorphic for now.

### 0.3 Be careful generalizing members

Do not blindly wrap every class field scheme as `forall a`. For generic class members, class params must remain free so an applied receiver can substitute them.

Example storage for `type Box[a] { pub value: a }`:

- template field scheme: `value: a` with no bound tvs
- constructor scheme in outer env: `forall a. (value: a): Box[a]!`

Later, method-local variables can be quantified by generalizing relative to an env whose `FreeTypeVar()` includes the class params. For example `map` on `List[a]` would store `forall b. ...`, not `forall a b. ...`. To do that, `Module.FreeTypeVar()` must stop returning nil for generic templates, and `SlotDecl.Infer`'s current "TODO: type is not monomorphic" redefinition check will need to tolerate previously-hoisted polymorphic function schemes.

## Phase 1: type AST + parser

### 1.1 Extend `ClassDecl`

In `pkg/dang/slots.go`:

```go
type ClassDecl struct {
  ...
  TypeParams []byte // or []hm.TypeVariable if importing hm in parser actions is OK
}
```

Validate duplicate params early in `ClassDecl.Hoist` and attach errors to the declaration/header.

### 1.2 Extend `NamedTypeNode`

In `pkg/dang/types.go`:

```go
type NamedTypeNode struct {
  Base *NamedTypeNode
  Name string
  Args []TypeNode
  Loc  *SourceLocation
}
```

Update:

- `ReferencedSymbols()` to include arg referenced symbols
- `Infer()` to infer/apply args and check arity
- `formatTypeNode()` in `pkg/dang/format.go`

### 1.3 Parser changes

In `pkg/dang/dang.peg`:

- `Class` header accepts optional type params after the name:

```peg
Class <- ... TypeToken _ name:Symbol params:TypeParamList? implements:... block:Block { ... }
TypeParamList <- '[' _ first:TypeParam rest:(_ CommaToken _ p:TypeParam { return p, nil })* _ ']'
TypeParam <- [a-z]
```

- `NamedType` accepts optional type args:

```peg
NamedType <- qual:(n:NamedType DotToken { return n, nil })? name:UpperId args:TypeArgList? { ... }
TypeArgList <- '[' _ first:Type rest:(_ CommaToken _ t:Type { return t, nil })* _ ']'
```

Then run:

```bash
go generate ./pkg/dang/
```

## Phase 2: represent applied generic object types

Add something like `AppliedType` (probably in `pkg/dang/types.go` or `pkg/dang/env.go`):

```go
type AppliedType struct {
  Base *Module
  Args []hm.Type
}
```

It should implement both `hm.Type` and `dang.Env`, just like `Module` does.

Key behavior:

- `Name/String`: `Box[Int!]`, `Either[Int!, String]`
- `Eq`: same `Base` pointer and pairwise equal args
- `Apply`: apply substitutions to args
- `FreeTypeVar`: union of args' free vars
- `SchemeOf`/`LocalSchemeOf`/`Bindings`: delegate to `Base`, but apply `{Base.TypeParams[i] => Args[i]}` to returned schemes
- `Supertypes`: initially none, or substituted base supertypes only if/when generic interfaces are supported

Add `TypeParams []hm.TypeVariable` (or `[]byte`) to `Module`.

`NamedTypeNode.Infer` should return:

- plain `*Module` for non-generic no-arg types
- error for generic no-arg types
- `*AppliedType` for generic applied types
- error for wrong arity or args on non-generic types

## Phase 3: constructor/self typing for generic classes

In `ClassDecl.Hoist` pass 0:

- call `declareLocalType` as today
- set `class.TypeParams = c.TypeParams`
- do not resolve body types yet

In pass 1 and `Infer`:

- compute the self type:

```go
func classSelfType(class *Module) hm.Type {
  if len(class.TypeParams) == 0 { return class }
  args := make([]hm.Type, len(class.TypeParams))
  for i, p := range class.TypeParams { args[i] = hm.TypeVariable(p) }
  return &AppliedType{Base: class, Args: args}
}
```

- set dynamic scope to `NonNull(classSelfType(class))`, not always `NonNull(class)`
- change `buildConstructorType` to take an `hm.Type` return type, not just `*Module`
- generic constructor type should be `(fields...): Box[a]!`
- add the constructor value scheme as `hm.NewScheme(class.TypeParams, constructorType)` so `Box` is instantiated at each call

Runtime `ConstructorFunction` can still keep `ClassType *Module` as the template for the first cut. If we want runtime `Value.Type()` to preserve args, teach `FunCall.Eval` to pass its inferred applied return type into constructor calls and set the returned `ModuleValue.Mod` to that applied type.

## Phase 4: constructor-aware invariant unification

Do not let `AppliedType.Types()` participate in the current composite unification without a same-constructor check.

Recommended HM addition in `pkg/hm/unify.go`:

```go
type TypeConstructor interface {
  SameTypeConstructor(Type) bool
}
```

Then the composite branch can require either:

- same concrete Go type via `reflect.TypeOf`, as today, and not a generic constructed type, or
- `have.SameTypeConstructor(want)` for constructed types

For user-defined generic object types, default to **invariant** args. That probably needs a symmetric unification helper rather than using assignability/subtyping for each arg:

- `Box[a]` should accept `Box[Int!]` and bind `a := Int!`
- `Box[Int!]` should equal `Box[Int!]`
- `Box[Cat]` should **not** automatically be assignable to `Box[Animal]` unless we later add variance

This is more conservative than `ListType`, which is currently covariant via `Supertypes()`.

## Phase 5: validation rules

Add validation during generic class hoist/infer:

- duplicate type params: error
- generic class with `implements`: either error in MVP or implement substituted interface validation deliberately
- non-method fields may only mention class type params, not arbitrary free vars
  - good: `pub value: a`
  - bad: `pub value: c` inside `type Box[a]`
- method signatures may mention class params plus method-local free vars
  - good: `pub map(&fn(value: a): b): Box[b]!`

This prevents accidental existential-ish fields that the current type system cannot represent soundly.

## Phase 6: formatter, completion, syntax

Update:

- `pkg/dang/format.go`
  - class header prints `[a, b]`
  - named type prints type args
- editor syntaxes if the grammar/syntax files classify type declarations deeply enough
- completions display `Box[a]` or `Box[_]` for generic types if desired

## Test plan

Language tests under `tests/test_*.dang`:

```dang
type Box[a] {
  pub value: a
}

pub intBox = Box(1)
pub strBox: Box[String!]! = Box("x")
assert { intBox.value == 1 }
assert { strBox.value == "x" }
```

```dang
type Either[a, b] {
  pub left: a
  pub right: b
}

pub e = Either(1, "one")
assert { e.left == 1 }
assert { e.right == "one" }
```

Generic method / constructor instantiation regression:

```dang
type Box[a] {
  pub value: a
  pub map(&fn(value: a): b): Box[b]! {
    Box(fn(value))
  }
}

pub mapped = Box(1).map { n => toString(n) + "!" }
assert { mapped.value == "1!" }
```

Error tests under `tests/errors/`:

- `Box` used without type args in type position
- `Box[Int!, String!]` wrong arity
- `String[Int!]` args on non-generic type
- duplicate params: `type Bad[a, a]`
- non-method field with unbound type var: `type Bad[a] { pub x: b }`
- invariant mismatch: `pub x: Box[String!]! = Box(1)`

Run:

```bash
go test ./tests/ -v
go test ./pkg/dang/... ./pkg/hm/... -v
```

Use `-update` for changed error goldens.

## Open questions

1. Are single-letter type params acceptable for the first pass, or should we first upgrade `hm.TypeVariable`/`VariableTypeNode` to support names like `elem`?
2. Should `Box` in expression position remain the constructor function, while `Box[...]` is only type syntax? I recommend yes; no value-level type application in the first pass.
3. Do we want runtime values to preserve applied type args (`Box[Int!]`) immediately, or is type erasure acceptable for MVP?
4. Should generic interfaces be designed now, or explicitly rejected until object generics are stable?
5. Should `List[T]` become accepted immediately as sugar for `[T]`, or wait until user-defined generics land?
