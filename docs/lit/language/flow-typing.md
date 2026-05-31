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

## Type narrowing via `case`

```dang
case (animal) {
  c: Cat => c.purr     # c is Cat!
  d: Dog => d.bark     # d is Dog!
}
```

- branch body sees the pattern-matched type

## Compound conditions

```dang
if (x == null or y == null) { raise "missing" }
# both x and y are T! after
```

## Limitations

- narrowing is intra-procedural — calling a function doesn't carry narrowed types across
- narrowing on object fields is conservative (cleared on any intervening call) — see `errors/flow_narrowing_field_no_narrow.dang`

> Meta: the field-narrowing limitation is the most surprising one in practice. Either document the workaround (re-bind to a local) or be candid that it's a known gap.
