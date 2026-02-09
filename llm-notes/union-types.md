# GraphQL Union Type Support

GraphQL union types are supported in Dang, enabling sum-type polymorphism where a value can be one of several concrete types. Unions can be loaded from GraphQL schemas or defined directly in Dang code. Unlike interfaces, union members share no common fields — the concrete type must be discriminated before accessing fields.

## Implementation

### Grammar (pkg/dang/dang.peg)

#### Union Declaration Syntax

Unions are declared using the `union` keyword with `|`-separated members:

```peg
Union <- UnionToken _ name:Symbol _ '=' _ first:Symbol rest:(_ '|' _ s:Symbol { return s, nil })* {
  return &UnionDecl{
    Name:       name.(*Symbol),
    Members:    sliceOfPrepend[*Symbol](first, rest),
    Visibility: PublicVisibility,
    Loc:        c.Loc(),
  }, nil
}
UnionToken <- "union" !WordChar
```

Example:
```dang
union Pet = Cat | Dog
union SearchResult = User | Post | Comment
```

### AST Structure (pkg/dang/slots.go)

#### UnionDecl

```go
type UnionDecl struct {
    InferredTypeHolder
    Name       *Symbol
    Members    []*Symbol        // List of member type names
    Visibility Visibility
    DocString  string
    Loc        *SourceLocation
    Inferred   *Module          // Populated during inference
}
```

### Type Environment (pkg/dang/env.go)

#### 1. Module Kind

Unions are represented as Modules with `UnionKind`:

```go
const (
    ObjectKind ModuleKind = iota
    EnumKind
    ScalarKind
    InterfaceKind
    UnionKind      // For union types
)
```

#### 2. Union Member Tracking

The `Module` struct tracks union membership bidirectionally:

```go
type Module struct {
    // ... existing fields ...
    members []Env  // Member types of this union (for union modules)
    unions  []Env  // Unions this type is a member of
}
```

Helper methods:
- `AddMember(member Env)` - Add a member type to this union (also sets backlink)
- `GetMembers() []Env` - Get all member types
- `HasMember(t Env) bool` - Check if a type is a member of this union
- `GetUnions() []Env` - Get all unions this type belongs to

#### 3. Schema Loading

In `NewEnv()`, unions are loaded in phases:

**Phase 1: Type Creation** — union modules created with `UnionKind` via `ModuleKindFromGraphQLKind`.

**Phase 2: Value Registration** — unions made available as values in root module.

**Phase 3: Member Linking** — after all types exist, link members via `PossibleTypes`:
```go
for _, t := range schema.Types {
    if t.Kind == introspection.TypeKindUnion {
        unionMod.AddMember(memberType)
    }
}
```

#### 4. Subtyping

`Module.Supertypes()` returns both interfaces and unions that a type belongs to. This integrates with the existing `Assignable()` function in `hm/unify.go`, which iterates supertypes. A `User` is assignable to `SearchResult` if `User` is a member of `SearchResult`.

### Introspection (pkg/introspection/introspection.go)

The `Type` struct includes `PossibleTypes` which carries union (and interface) member info:

```go
type Type struct {
    // ... existing fields ...
    PossibleTypes []*Type `json:"possibleTypes,omitempty"`
}
```

The introspection query (`introspection.graphql`) already fetches `possibleTypes`.

### Hoisting & Compilation

#### Form Classification (block.go)

Unions are classified as types:
```go
case *UnionDecl:
    classified.Types = append(classified.Types, f)
```

#### UnionDecl.Hoist

**Pass 0:** Create union module with `UnionKind`, register in type environment.

#### UnionDecl.Infer

Resolves member types and links them. Validates:
- Each member must exist
- Each member must be a `*Module`
- Each member must be `ObjectKind` (not enum, scalar, interface, or another union)

### Evaluation

Union declarations register a `ModuleValue` like interfaces:
```go
func (u *UnionDecl) Eval(ctx context.Context, env EvalEnv) (Value, error) {
    unionModule := NewModuleValue(u.Inferred)
    env.SetWithVisibility(u.Name.Name, unionModule, u.Visibility)
    return unionModule, nil
}
```

### Formatting (format.go)

Unions format as a single line:
```
union SearchResult = User | Post | Comment
```

## Usage

### Defining Unions in Dang

```dang
type Cat {
    pub name: String!
    pub lives: Int! = 9
}

type Dog {
    pub name: String!
    pub tricks: Int! = 0
}

union Pet = Cat | Dog
```

### GraphQL Unions

GraphQL union types are loaded automatically from the schema:

```graphql
union SearchResult = User | Post

type Query {
    search(query: String!): [SearchResult!]!
}
```

## Key Design Decisions

1. **Flat unions only** — no union-of-unions (matches GraphQL spec)
2. **Object members only** — scalars, enums, interfaces cannot be union members
3. **Bidirectional tracking** — both union→members and member→unions
4. **Subtyping via Supertypes** — concrete types list their unions as supertypes, reusing the existing `Assignable` mechanism
5. **No shared fields** — unlike interfaces, unions have no fields of their own

## Related Files

- `pkg/dang/dang.peg` — Grammar for `union` syntax
- `pkg/dang/slots.go` — `UnionDecl` AST node
- `pkg/dang/block.go` — Form classification
- `pkg/dang/env.go` — `UnionKind`, member tracking, schema loading
- `pkg/dang/eval.go` — Runtime union value availability
- `pkg/dang/format.go` — `formatUnionDecl`
- `pkg/introspection/introspection.go` — `PossibleTypes` field
- `tests/gqlserver/schema.graphqls` — Test schema with `SearchResult` union
- `tests/test_union_dang.dang` — Dang union definition tests
- `tests/errors/union_non_object_member.dang` — Validation error tests
- `tests/errors/union_undefined_member.dang` — Undefined member error tests
- `editors/vscode/syntaxes/dang.tmLanguage.json` — VSCode keyword
- `treesitter/queries/highlights.scm` — Tree-sitter highlights
