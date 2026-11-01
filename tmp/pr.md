Soundness fixes for subtyping, objects, interfaces, records, enums and
variance: programs the checker accepted but that went wrong at runtime
(wrong-typed values, missing fields, null in a non-null slot, an
"exhaustive" `case` that didn't match). Almost everything here only makes
the checker stricter; the exceptions are listed under release notes.

Overview of the whole soundness effort: https://gistpreview.github.io/?7466a76346390a8516a0d9f03a430aff

## Bugs fixed

Each was accepted before and failed at runtime; now it's a compile error,
unless noted.

**Function parameters weren't contravariant.** Function types now check
arguments and the block contravariantly and the result covariantly. They are
also rechecked under the combined substitution, so a generic function like
`ident(d: a): a` can't stand in for `(d: Animal!): Dog!`.
```dang
type Host { hook(a: Animal!): String! { a.name }; fire: String! { hook(Cat("c")) } }
dogOnly(a: Dog!): String! { a.bark }
h.hook = &dogOnly   # before: runtime `module Cat does not have a field "bark"`
```

**Parameter names weren't part of function types**, though arguments bind by name.
```dang
myFun(&block(x: Int!): Int!): Int! { block(x: 42) }
myFun { y => y + 1 }   # before: y was null → "addition not supported for NullValue"; now 43
```
- A function with differently named or swapped parameters could be stored in a function-typed field, crossing its arguments. Now rejected.
- A block now takes on the parameter names of the signature it's passed for.
- A positional call now binds by the name resolved against the callee's static type.

**Interface conformance missed block parameters and private members.**
- An implementation could call its block with a supertype of what the interface promised the block. It must now accept the interface's block type and name its parameters the same.
- A `let` member satisfied a public interface field, and a child interface could redeclare a parent's public field as `let`. Both now rejected.

**Records were taken for named types.**
```dang
let d: Dog! = {{name: "x"}}   # Dog has just `name`
let p: Pet! = d
case (p) { x: Dog => x.name; c: Cat => c.meow }   # before: "exhaustive case did not match the value"
```

**Type names were typed as instances of the type.** Interface, union, enum and scalar names, builtin `JSON`/`YAML`/`TOML` included, are now typed as their namespace (meta-type).
- `Named` passed for a `Named!` and crashed on `.name`.
- `Color` passed for a `Color!`.
- `Color.RED.GREEN` and `Email.domain` type-checked and crashed.
- `describe(JSON)` type-checked and crashed.
- A `case` arm `n: Named` matched the namespace value at runtime.

Enum values now have no members, and private statics stay private when the name is held in a variable.

**Invalid enum literals failed only at runtime.**
- `let c: Color! = "PURPLE"` is now a compile error, as are argument, list, map, cast and `case` literals: `"PURPLE" is not a value of Color; expected one of RED, GREEN`.
- Casting a non-literal String is still checked at runtime.

**Equality between unrelated types always answered false.** It's now a compile error; see release notes.

**Smaller fixes:**
- A method declared before a field was taken for a constructor parameter at runtime, so `Impl(3)` failed with "required field v has no value".
- `dang fmt` printed an interface method's block parameter as `f: (a: Int!): String!`, which doesn't parse.

## Release notes (behaviour changes)

- **Equality is type-checked.** `==`/`!=` only accept operands that could be equal:
  - allowed: the same type up to nullability, `Int`/`Float` (they compare numerically), a subtype against its supertype or interface, a union against its members, and `null`;
  - rejected: `1 == "1"`;
  - an enum vs `String` (`c == "GREEN"`, always false before) is rejected with the hint `c == Color.GREEN`;
  - a custom scalar or `ID` vs `String` now needs a cast: `node.id == ("User:1" :: ID!)`. This breaks comparisons that worked before, with the hint `cast the literal to compare: ("User:1" :: ID!)`. In this repo, 33 such comparisons in tests were rewritten (43 comparisons across 17 test cases broke in all; the other 10 were genuinely mismatched).
- **Block parameter names:** a block parameter named like a *different* parameter of its signature is rejected (`{ b, a => }` for `(a, b)`), rather than silently binding by position.
- **Interface positional order (runtime change):** when an implementation orders same-typed parameters differently from its interface, a positional call through the interface now follows the interface's order. Before, it followed the implementation's order, which disagreed with named calls.
- **Records and GraphQL input objects:** a record literal can still be passed for a GraphQL input object of exactly its shape. That's the one exception to records no longer standing in for named types.
- Interfaces: `let` members don't implement public fields; implementations' block parameters must match the interface's names and accept its block type.
- Calling a value that may be one of several functions with different signatures (`if (c) { &f } else { &g }`) gets an explanatory error.

## Known gaps (filed)

- #224 records don't widen to a supertype's record (`{{a: Dog!}}` as `{{a: Animal!}}`)
- #225 records can't fill input objects with optional fields (exact field types required)
- #226 `[Dog] + [Cat]` doesn't widen to `[Animal!]!`
- #227 exhaustive enum/Boolean `case` is still nullable
- #228 `Email! | String!` isn't accepted as `String!`

Function values must name their parameters identically and in the same
order, unlike interface conformance (which matches by name in any order).
This is intended.

## Testing

Regression tests for each fix live under `tests/` (should-pass) and
`tests/errors/` (with goldens). `pkg/dang`, `pkg/hm`, `pkg/lsp`, `cmd/dang`,
`docs/go` and `tests` pass; docs and the language/internals skills are
updated.
