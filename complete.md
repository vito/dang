# Generics in Dang

A reference for what's implemented. Examples first, mechanics second.

## Syntax

### Generic type declarations

```graphql
type Box[a] {
  pub value: a
}

type Either[a, b] {
  pub left: a
  pub right: b
}

# Multi-letter parameter names
type Pair[first, second] {
  pub a: first
  pub b: second
}
```

### Generic interfaces

```graphql
interface Container[a] {
  pub item: a
}

interface Mapping[k, v] {
  pub key: k
  pub value: v
}
```

### Implementing a generic interface

```graphql
# Forward the class's own param
type Box[a] implements Container[a] {
  pub item: a
}

# Bind the interface's param to a concrete type
type IntBox implements Container[Int!] {
  pub item: Int!
}

# Multi-param interface
type Entry[k, v] implements Mapping[k, v] {
  pub key: k
  pub value: v
}
```

### Applying a generic type

```graphql
pub b: Box[Int!]! = Box(1)
pub e: Either[Int!, String!]! = Either(1, "two")
pub nested: Box[Box[Int!]!]! = Box(Box(7))
```

### Methods with explicit type parameters

```graphql
type Box[a] {
  pub value: a
  # `a` is inherited from the class; `b` is declared as method-local
  pub mapTo[b](&fn(value: a): b): Box[b]! {
    Box(fn(value))
  }
}

# Top-level function with explicit params
pub mapList[a, b](xs: [a]!, &fn(value: a): b): [b]! {
  xs.map { v => fn(v) }
}

# Without explicit params, auto-generalize still works
pub identity(x: a): a { x }
```

### Type parameter scoping

- Type parameters are lowercase identifiers matching `[a-z][a-zA-Z0-9_]*`.
- They live in a separate namespace from fresh inference variables (which use Greek letters internally).
- Class params are in scope throughout the class body, including method signatures.
- Method-local params (`[b]` on `mapTo`) are scoped to that method.

## Type representation

### `*Module`

The nominal type for a named class/interface. Has a `TypeParams []hm.TypeVariable` field — empty for non-generic types.

- `Module.Eq` is pointer identity (nominal equality).
- `Module.FreeTypeVar()` returns `TypeParams` as a set so class params count as "in scope" during method generalization.
- `Module.Supertypes()` returns the interfaces this module declares it implements.

### `*AppliedType`

```go
type AppliedType struct {
    Base *Module
    Args []hm.Type
}
```

Represents `Box[Int!]` etc. Implements both `hm.Type` and `dang.Env`.

- `SchemeOf(name)` looks up the field on `Base`, then applies the substitution `{Base.TypeParams[i] → Args[i]}` to the returned scheme.
- `Eq` compares `Base` pointer + pairwise arg equality.
- `Apply(subs)` substitutes within args.
- `FreeTypeVar` is the union of args' free type vars.
- `Supertypes()` walks `Base.Supertypes()` applying the arg substitution, so `Box[Int!]`'s supertype is `Container[Int!]`, not `Container[a]`.
- `SameTypeConstructor(other)` returns true iff both share the same `Base` pointer (used by unification).
- All mutating `Env` methods panic — `AppliedType` is a read-only view; mutate `Base` instead.

### `hm.TypeVariable`

Now a `string` (was `rune`), so multi-letter names work.

### Fresh variable allocation

Fresh inference vars use Greek letters (`α`, `β`, ...) so they cannot collide with source-level Latin type variables. See `pkg/dang/infer.go` and `pkg/hm/generalize.go`.

## Schemes & instantiation

### Storage

Functions are stored as `hm.Scheme`s with quantified type variables:

- `pub identity(x: a): a` → `forall a. (x: a): a`
- `Box[a].pub mapTo[b](&fn(value: a): b): Box[b]!` → `forall b. (&fn(value: a): b): Box[b]!`
  - `a` stays free; `AppliedType.SchemeOf` substitutes it per receiver.
  - `b` is quantified at the method level.

### Quantification rules at hoist time

`FunDecl.buildScheme`:

1. If `f.TypeParams` is explicitly declared:
   - Reject duplicates.
   - Reject undeclared free vars in the signature (must be in `TypeParams` or `env.FreeTypeVar()`).
   - Reject unused declared params.
   - Store as `hm.NewScheme(f.TypeParams, fnType)`.
2. Otherwise: store as `hm.Generalize(env, fnType)` — quantifies free vars in the signature that are not free in the env.

`Module.FreeTypeVar()` returning class TypeParams is what causes `Generalize` to skip class params when generalizing methods.

### Constructor schemes

A generic class's constructor is stored as `hm.NewScheme(class.TypeParams, constructorFnType)`. The constructor's return type uses the class self-type:

```go
// pkg/dang/slots.go
func classSelfType(class *Module) hm.Type {
    if len(class.TypeParams) == 0 {
        return class
    }
    args := make([]hm.Type, len(class.TypeParams))
    for i, p := range class.TypeParams {
        args[i] = p
    }
    return &AppliedType{Base: class, Args: args}
}
```

So `Box[a]`'s constructor has return type `Box[a]!` (an `AppliedType` referencing its own params). At each call site, the scheme instantiates fresh `α`, unification binds `α := Int!`, return type becomes `Box[Int!]!`.

### Lookup instantiation

`Symbol.Infer` and `Select.Infer` call `hm.Instantiate(fresh, scheme)` before returning the type. Each lookup of a polymorphic scheme generates fresh type variables — the underlying scheme is never mutated.

`SlotDecl.Infer` tolerates polymorphic hoisted schemes: it compares against the underlying type body without requiring monomorphism, and preserves the polymorphic scheme rather than overwriting it with a monomorphic one.

## Unification

### `TypeConstructor` interface

```go
type TypeConstructor interface {
    SameTypeConstructor(Type) bool
}
```

Implemented by `*AppliedType`. The `Assignable` composite branch handles three cases:

1. **Both implement `TypeConstructor`** and `SameTypeConstructor` returns true: unify each arg pair **invariantly** (both `Assignable(a, b)` and `Assignable(b, a)` must succeed) and compose substitutions.
2. **Mismatched constructors** (one TC, the other not, or different bases): skip the reflect-equal composite branch and fall through to the supertype check below — this is how `NonNull(Box[Int!])` reaches `Box[Int!]` via `NonNullType.Supertypes()`.
3. **Same Go composite type** (e.g. both `NonNullType`): unify components covariantly with `Assignable` as before.

### Invariance

Generic type arguments are **invariant**. `Box[Cat!]` is not assignable to `Box[Animal!]` even when `Cat` is a subtype of `Animal`. This includes the supertype path: `Box[Cat!]!` is a supertype of `Container[Cat!]!` (its declared interface), but not `Container[Animal!]!`.

### Supertype propagation

A `Box[Int!]` is assignable to `Container[Int!]` because `AppliedType.Supertypes()` returns `[Container[Int!]]` after substituting `{a → Int!}` into the base's declared supertypes.

## Validation

Errors detected at hoist time:

- Duplicate type parameter: `type Bad[a, a]` or `pub fn[a, a]`.
- Generic type used without arguments in type position: `pub b: Box! = Box(1)` (the bare `Box` in the annotation).
- Non-generic type used with arguments: `pub b: String[Int!]! = "hi"`.
- Wrong arity: `pub b: Box[Int!, String!]! = Box(1)` (`Box` takes one arg).
- Undeclared type variable in explicit-param signature: `pub bad[a](x: a, y: c): a { x }`.
- Unused declared type parameter: `pub unused[a, b](x: a): a { x }`.

Errors detected at interface validation:

- Class missing interface-required field: a class implementing `Container[a]` that omits `item`.
- Wrong interface arity in `implements` clause: `type Box[a] implements Container[a, Int!]`.
- Invariance on supertype path: `pub bad: Container[String!]! = Box(42)` where `Box[Int!]` is the inferred type.

## Constructor evaluation

`ConstructorFunction` still carries `ClassType *Module` at runtime (type erasure). The applied return type is computed by inference but the runtime `ModuleValue.Mod` points to the base module. Generic args are not preserved at runtime — they live only in the type system.

## Format & display

- `type Box[a, b]` — class header prints type params.
- `Name[Arg1, Arg2]` — named type usages print type args.
- `pub map[a, b](...)` — function declarations print explicit type params.
- `Box[Int!]` — applied generic types print with their args.

## Limitations

- **Invariant only.** No `+a` / `-a` variance annotations.
- **No higher-kinded types.** You can't pass `Box` itself as a type argument.
- **No bounded parameters.** No `type Box[a: Comparable]`.
- **Single-letter validation only.** Parser accepts multi-letter names but there's no namespace check beyond the parser regex.
- **Runtime type erasure.** `Box[Int!]` and `Box[String!]` runtime values both report `Box` as their type's `Name`.

## File map

| File | Role |
|------|------|
| `pkg/dang/dang.peg` | Grammar for `TypeParamList`, `TypeArgList`, type params on functions and classes |
| `pkg/dang/types.go` | `NamedTypeNode.Args`, `NamedTypeNode.Infer` arity check |
| `pkg/dang/env.go` | `Module.TypeParams`, `Module.FreeTypeVar` returning params |
| `pkg/dang/applied_type.go` | `AppliedType` (Type + Env), `SameTypeConstructor`, panic-on-mutate |
| `pkg/dang/slots.go` | `ClassDecl.TypeParams`, `classSelfType`, generic constructor scheme, interface implementation validation, `InterfaceDecl.TypeParams` |
| `pkg/dang/ast_declarations.go` | `FunDecl.TypeParams`, `buildScheme` with explicit-params validation |
| `pkg/dang/ast_expressions.go` | `Symbol.Infer` / `Select.Infer` call `hm.Instantiate` |
| `pkg/dang/format.go` | Print `[a, b]` on class headers, function headers, and named type usages |
| `pkg/hm/types.go` | `TypeVariable` as `string` |
| `pkg/hm/unify.go` | `TypeConstructor` interface, three-way composite branch, `unifyInvariant` |
| `pkg/hm/generalize.go` | Greek-letter fresh variable allocation |
| `pkg/hm/scheme.go` | `hm.NewScheme`, `hm.Instantiate`, `hm.Generalize` (unchanged, but now actually used) |
