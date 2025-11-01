# Extending Hindley-Milner with Supertypes

## Goal

Extend the `hm.Type` interface to support subtyping natively by adding a `Supertypes() []hm.Type` method. This will unify the current ad-hoc subtyping implementations (null covariance and interface subtyping) into a single, coherent system.

## Current State Analysis

### Current Subtyping Mechanisms

1. **Null Subtyping**: Implemented directly in `unify()` (pkg/hm/unify.go:32-47)
   - `NonNullType` has special handling: `T!` is assignable to `T`
   - Direction matters: `String!` → `String` ✓, but `String` → `String!` ✗
   - Implemented as hardcoded logic in the unification algorithm

2. **Interface Subtyping**: Implemented in `Module.Eq()` (pkg/dang/env.go:534-553)
   - `Module.Eq()` is intentionally asymmetric to support subtyping
   - `Cat.Eq(Animal)` returns true when Cat implements Animal
   - Uses `ImplementsInterface()` to check relationships
   - Works because `unify()` falls back to `Type.Eq()` for atomic types

### Current Helper Functions

- `isSubtypeOf()` (pkg/dang/env.go:690-725): Checks covariance for return types
- `isSupertypeOf()` (pkg/dang/env.go:729-767): Checks contravariance for arguments
- `findCommonSupertype()` (pkg/dang/env.go:772-912): Finds LUB for heterogeneous lists

### Problems with Current Approach

1. **Fragmented**: Subtyping logic is scattered across multiple files and functions
2. **Non-Uniform**: Different types handle subtyping differently (unify vs Eq vs helpers)
3. **Limited Composability**: Hard to combine subtyping rules (e.g., `[Cat!]` as `[Animal]`)
4. **Asymmetric Eq**: `Type.Eq()` violates normal equality semantics, which is confusing
5. **Manual Propagation**: Each type system extension needs to manually handle subtyping

## Proposed Solution

### 1. Extend `hm.Type` Interface

Add a new method to the core `hm.Type` interface:

```go
// pkg/hm/types.go
type Type interface {
    Substitutable
    Name() string
    Normalize(TypeVarSet, TypeVarSet) (Type, error)
    Types() Types
    Eq(Type) bool  // Return to being symmetric equality
    Supertypes() []Type  // NEW: Return direct supertypes
    fmt.Stringer
}
```

### 2. Implement Supertypes() for All Types

#### Core HM Types (pkg/hm/types.go)

- **TypeVariable**: Return `nil` (no supertypes)
- **FunctionType**: Return `nil` (functions don't have supertypes)
- **NonNullType**: Return `[]Type{t.Type}` (the inner nullable type is a supertype)

#### Dang Types (pkg/dang/types.go)

- **ListType**: Return `nil` (or potentially handle element type covariance)
- **GraphQLListType**: Return `nil`
- **RecordType**: Return `nil` (structural types don't have nominal supertypes)

#### Module Types (pkg/dang/env.go)

- **Module**: Return the list of implemented interfaces
  ```go
  func (m *Module) Supertypes() []hm.Type {
      if m.Kind != ObjectKind {
          return nil
      }
      result := make([]hm.Type, len(m.interfaces))
      for i, iface := range m.interfaces {
          result[i] = iface.(hm.Type)
      }
      return result
  }
  ```

### 3. Update Type.Eq() to Be Symmetric

Restore `Type.Eq()` to be a true symmetric equality check:

```go
// pkg/dang/env.go (Module.Eq)
func (t *Module) Eq(other Type) bool {
    otherMod, ok := other.(*Module)
    if !ok {
        return false
    }
    if t.Named != "" {
        // Only exact equality (pointer comparison)
        return t == otherMod
    }
    return t.AsRecord().Eq(otherMod.AsRecord())
}
```

Remove the asymmetric subtyping logic from `Module.Eq()`.

### 4. Update Assignable() to Use Supertypes

Modify `hm.Assignable()` to check subtyping transitively:

```go
// pkg/hm/assignable.go (NEW FILE)
func Assignable(have, want Type) (Subs, error) {
    // First try direct unification
    subs, err := unify(have, want)
    if err == nil {
        return subs, nil
    }
    
    // If that fails, try subtyping: check if have is a subtype of want
    if isSubtype(have, want) {
        return NewSubs(), nil
    }
    
    return nil, UnificationError{have, want}
}

// isSubtype checks if sub is a subtype of super (transitively)
func isSubtype(sub, super Type) bool {
    if sub.Eq(super) {
        return true
    }
    
    // Check direct supertypes
    for _, supertype := range sub.Supertypes() {
        if isSubtype(supertype, super) {
            return true
        }
    }
    
    return false
}
```

### 5. Update unify() to Remove Special Cases

Remove the special-case null handling from `unify()`:

```go
// pkg/hm/unify.go
func unify(have, want Type) (Subs, error) {
    // Handle type variables
    if haveTV, ok := have.(TypeVariable); ok {
        return bindVar(haveTV, want)
    }
    if wantTV, ok := want.(TypeVariable); ok {
        return bindVar(wantTV, have)
    }
    
    // NO MORE SPECIAL NonNullType HANDLING HERE
    
    // Handle composite types
    haveTypes := have.Types()
    wantTypes := want.Types()
    
    if haveTypes != nil && wantTypes != nil {
        // ... existing composite unification logic ...
    }
    
    // Atomic type equality (now symmetric)
    if have.Eq(want) {
        return NewSubs(), nil
    }
    
    return nil, UnificationError{have, want}
}
```

### 6. Update Helper Functions

Simplify the helper functions to use the new `Supertypes()` method:

```go
// pkg/dang/subtyping.go (NEW FILE or refactor in env.go)

// isSubtypeOf checks if sub is a subtype of super (covariance)
func isSubtypeOf(sub, super hm.Type) bool {
    return hm.isSubtype(sub, super)  // Delegate to core HM function
}

// isSupertypeOf checks if super is a supertype of sub (contravariance)
func isSupertypeOf(super, sub hm.Type) bool {
    return hm.isSubtype(sub, super)  // Just flip the arguments
}

// findCommonSupertype finds the least upper bound of two types
func findCommonSupertype(t1, t2 hm.Type) hm.Type {
    if t1.Eq(t2) {
        return t1
    }
    
    // Build supertype graphs for both types
    supers1 := buildSupertypeSet(t1)
    supers2 := buildSupertypeSet(t2)
    
    // Find common supertypes
    var common []hm.Type
    for super := range supers1 {
        if supers2[super] {
            common = append(common, super)
        }
    }
    
    // Find the most specific common supertype (closest to leaves)
    // This is the LUB (Least Upper Bound)
    // ... implementation details ...
}

func buildSupertypeSet(t hm.Type) map[hm.Type]bool {
    result := make(map[hm.Type]bool)
    result[t] = true
    
    for _, super := range t.Supertypes() {
        for s := range buildSupertypeSet(super) {
            result[s] = true
        }
    }
    
    return result
}
```

### 7. Handle List Element Covariance

For list element covariance (`[Cat!]` → `[Animal]`), we have two options:

**Option A: Keep special handling in isSubtypeOf()**
```go
func isSubtypeOf(sub, super hm.Type) bool {
    // Use core isSubtype for basic check
    if hm.isSubtype(sub, super) {
        return true
    }
    
    // Special case: List element covariance
    if subList, ok := sub.(ListType); ok {
        if superList, ok := super.(ListType); ok {
            return isSubtypeOf(subList.Type, superList.Type)
        }
    }
    
    // Similar for GraphQLListType
    
    return false
}
```

**Option B: Make ListType.Supertypes() return element-covariant supertypes**
This is more complex and may generate infinite supertypes. Probably not worth it.

**Recommendation**: Use Option A.

### 8. Update Type Variable Binding

Consider whether type variables should be constrained by supertypes:

```go
// pkg/hm/unify.go
func bindVar(tv TypeVariable, t Type) (Subs, error) {
    if tv2, ok := t.(TypeVariable); ok && tv == tv2 {
        return NewSubs(), nil
    }
    
    if occursCheck(tv, t) {
        return nil, fmt.Errorf("Occurs check failed: %s occurs in %s", tv, t)
    }
    
    // NEW: Should we prevent binding if it creates invalid subtyping?
    // For now, no - keep it simple.
    
    subs := NewSubs()
    subs.Add(tv, t)
    return subs, nil
}
```

**Recommendation**: Keep variable binding simple for now.

## Implementation Checklist

### Phase 1: Core Infrastructure
- [ ] Add `Supertypes() []Type` method to `hm.Type` interface (pkg/hm/types.go)
- [ ] Implement `Supertypes()` for `TypeVariable` (return `nil`)
- [ ] Implement `Supertypes()` for `FunctionType` (return `nil`)
- [ ] Implement `Supertypes()` for `NonNullType` (return `[]Type{t.Type}`)
- [ ] Implement `Supertypes()` for `ListType` (return `nil`)
- [ ] Implement `Supertypes()` for `GraphQLListType` (return `nil`)
- [ ] Implement `Supertypes()` for `RecordType` (return `nil`)
- [ ] Implement `Supertypes()` for `Module` (return interface list for ObjectKind)

### Phase 2: Refactor Assignable
- [ ] Create new `isSubtype(sub, super Type) bool` function in pkg/hm/
- [ ] Update `Assignable()` to check subtyping after unification fails
- [ ] Remove special NonNullType handling from `unify()`
- [ ] Restore `Module.Eq()` to symmetric equality (remove interface subtyping logic)

### Phase 3: Update Helper Functions
- [ ] Simplify `isSubtypeOf()` to use `hm.isSubtype()`
- [ ] Simplify `isSupertypeOf()` to use flipped `hm.isSubtype()`
- [ ] Keep list element covariance as special case in `isSubtypeOf()`
- [ ] Update `findCommonSupertype()` to use `Supertypes()` transitively

### Phase 4: Testing
- [ ] Run existing tests to ensure no regressions: `./tests/run_all_tests.sh`
- [ ] Test null subtyping still works: `String!` → `String`
- [ ] Test interface subtyping still works: `Cat` → `Animal`
- [ ] Test transitive subtyping: `Cat` → `Animal` → `Named` (if Cat implements Animal implements Named)
- [ ] Test list covariance: `[Cat!]` → `[Animal]`
- [ ] Test function return covariance still works
- [ ] Test function argument contravariance still works
- [ ] Test findCommonSupertype with interface hierarchies

### Phase 5: Documentation
- [ ] Update llm-notes/interface-types.md to reflect new approach
- [ ] Create llm-notes/subtyping-system.md documenting the Supertypes() design
- [ ] Add comments explaining the subtyping semantics in hm.Type interface

## Edge Cases to Consider

1. **Circular Interface Implementations**: Can interfaces implement other interfaces? If so, need cycle detection in `isSubtype()`.

2. **Multiple Interface Inheritance**: If `Cat implements Animal & Named`, both should be in Supertypes().

3. **Type Variables**: Should `TypeVariable` be able to have subtype constraints? (Not for now.)

4. **List Element Covariance**: `[NonNullType{Module{Cat}}]` → `[Module{Animal}]` requires both unwrapping NonNull and checking interface subtyping.

5. **Empty Supertype Chains**: Types without supertypes should return `nil` or `[]Type{}` consistently.

6. **Performance**: Transitive subtype checking could be O(n²) or worse. May need memoization later.

## Expected Benefits

1. **Uniformity**: All subtyping goes through one mechanism (Supertypes())
2. **Composability**: Easy to add new subtyping relationships
3. **Clarity**: Subtyping is explicit in the type interface, not hidden in unification
4. **Extensibility**: Future type system extensions can easily declare supertypes
5. **Symmetry**: Type.Eq() becomes truly symmetric, reducing confusion
6. **Transitivity**: Multi-level interface hierarchies work automatically

## Migration Strategy

1. Add `Supertypes()` to interface (breaks compilation - good, forces implementation)
2. Implement `Supertypes()` for all existing types (most return `nil`)
3. Add `isSubtype()` function but don't use it yet
4. Update `Assignable()` to call `isSubtype()` after `unify()` fails
5. Run tests - should still pass
6. Remove special NonNullType handling from `unify()`
7. Run tests - should still pass
8. Restore `Module.Eq()` to symmetric equality
9. Run tests - should still pass
10. Refactor helper functions to use new mechanism

## Risks

1. **Breaking Change**: Adding a method to `hm.Type` breaks external implementations (if any).
   - Mitigation: This is an internal package, probably fine.

2. **Performance**: Transitive subtype checking could be slower than direct checks.
   - Mitigation: Measure first, optimize later. Can add memoization if needed.

3. **Subtle Bugs**: Changing unification behavior might break existing code.
   - Mitigation: Comprehensive testing at each step.

4. **List Covariance**: Keeping it as a special case feels inconsistent.
   - Mitigation: Document clearly. Could revisit later with more sophisticated approach.

## Open Questions

1. Should `Supertypes()` return all direct supertypes, or compute transitive closure?
   - **Answer**: Return only direct supertypes. Compute transitive closure in `isSubtype()`.

2. Should we make `Eq()` truly symmetric, or keep it asymmetric for performance?
   - **Answer**: Make it symmetric. Subtyping should be explicit, not hidden in equality.

3. Should list covariance be handled specially, or integrated into Supertypes()?
   - **Answer**: Handle specially for now. Full integration is complex.

4. Do we need contravariance for function arguments in Supertypes()?
   - **Answer**: No. Contravariance is different from subtyping. Keep using `isSupertypeOf()` helper.

5. Should we implement LUB (Least Upper Bound) / Join using Supertypes()?
   - **Answer**: Yes, refactor `findCommonSupertype()` to use Supertypes() transitively.

## Success Criteria

- [ ] All existing tests pass
- [ ] No special cases in `unify()` for NonNullType
- [ ] `Module.Eq()` is symmetric
- [ ] Transitive interface subtyping works (if A → B → C, then A → C)
- [ ] Code is clearer and easier to understand
- [ ] Adding new subtyping relationships is straightforward

## Timeline Estimate

- Phase 1 (Core Infrastructure): 1-2 hours
- Phase 2 (Refactor Assignable): 1 hour
- Phase 3 (Update Helpers): 30 minutes
- Phase 4 (Testing): 1-2 hours
- Phase 5 (Documentation): 30 minutes

**Total**: ~4-6 hours

## Notes

This is a refactoring to make the existing subtyping behavior more explicit and uniform. It should NOT change the semantics of the type system, only improve its implementation.
