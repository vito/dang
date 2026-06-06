\use-plugin{dang}

# Flow-sensitive typing {#flow-typing}

> Meta: short page, but pays for itself — the alternative is writing `!` casts everywhere. Make sure the examples include both narrowing inside a branch and narrowing after a guard clause.

## Null narrowing

### After a guard

```dang
if (x == null) { return "no value" }
# x is now T! here
print(x.length)
```

### Inside a branch

```dang
if (x != null) {
  # x is T! here
  print("got " + x)
}
```

### Else branch

```dang
if (x == null) { ... } else {
  # x is T! here
}
```

### Loop conditions

```dang
for (x != null) {
  # x is T! in the body
  x = x.next
}
```

## Diverging constructs are narrowing-aware

- `return`, `raise`, `break`, `continue` all count as diverging
- code after them in the same scope sees the narrowed type
- a guard whose then-branch diverges narrows the rest of the enclosing scope
- `else if` chains: the parser wraps `else if` in a Block, so the outer guard's falsy facts still apply afterward
- sequential guards accumulate: each narrows independently as forms are processed in order
- a loop *condition* narrows the loop body (`for (x != null) { … }` → `x` is `T!` inside)

## Type narrowing via `case`

```dang
case (animal) {
  c: Cat => c.purr     # c is Cat!
  d: Dog => d.bark     # d is Dog!
}
```

- `binding: TypeName => …` clauses bind the operand narrowed to the pattern type
- `try`/`catch` clauses reuse the same `CaseClause` form, so typed catch clauses narrow the bound error the same way
- this is how you recover a concrete value from a widened conditional: an `if`/`else` over divergent branches infers as a **union**, which a `case` then narrows

## Conditional result inference (related)

- an `if`/`else` where one branch is `null` infers a **nullable** type, not non-null
- divergent concrete branches widen to their common interface/supertype, or to a union when unrelated
- a discarded divergent conditional is fine; only *using* the result forces the union/narrowing

## Compound conditions

```dang
if (x == null or y == null) { raise "missing" }
# both x and y are T! after the diverging guard
```

- guard with `or`: entering the diverging branch means *both* checks failed, so **both** narrow afterward
- compound `and` *inside a then-branch* narrows both operands in that branch:

```dang
if (maybe != null and other != null) {
  maybe + other   # both T! here
}
```

## Limitations

- narrowing is intra-procedural — calling a function doesn't carry narrowed types across
- **`and`-guard does NOT narrow**: `if (x == null and y == null) { raise … }` tells us only that *at least one* is non-null afterward, so neither narrows individually
- **field accesses don't narrow**: a null check on `h.val` does not narrow later `h.val` accesses, because each `.field` access could return a different value. Workaround: bind to a local first — `let v = h.val; if (v == null) { … }`
- in an `else` branch where the guard checked `== null`, the variable is known *null* (not narrowed to `T!`) — using it as non-null there errors
- narrowing applies to bare symbols (locals, and bare `self`-field references inside methods, which parse as plain `Symbol`s)

When narrowing can't reach the value — a field or call result, or a spot where the checker just can't follow your reasoning — the postfix `!` operator is the explicit escape hatch: `expr!` narrows `T` to `T!` and raises at runtime if the value turns out to be null. See [#operators].

See also [#errors] (`raise`/`try`/`catch` divergence) and [#control-flow] (guards, loops, `case`).

> Meta: field-narrowing and the `and`-guard non-narrowing are the two most surprising gaps in practice. Both are now documented with the re-bind-to-a-local workaround.
