\use-plugin{dang}

# Objects (`type`) {#objects}

> Meta: lead with "a `type` declares both a type and its prototype constructor." That's the unusual thing. Cross-link to [#mutation] — don't try to explain CoW here.

## Declaration

```dang
type Person {
  pub name: String!
  pub age: Int! = 0

  pub greet: String! {
    "hi, I'm " + name
  }
}
```

- declares a type `Person` and a constructor function `Person`
- members are fields or methods, indistinguishable in syntax (`pub`/`let` + `name` + optional `: Type`, `= default`, or `{ body }`)

## Public vs. private members

- `pub` — readable from outside the type; default visibility for a `type`
- `let` — readable only inside the type's own methods/defaults (private)
- whether a member is a **constructor parameter** depends on having NO default, NOT on visibility:
  - `pub x: T!` (no default) → required positional param
  - `pub x: T! = d` / `pub x = d` → optional param (default `d`)
  - `let x: T!` (no default) → required positional param too (verified: test_private_required_fields.dang `Foo("public_value", "private_value")`)
  - `let x: T! = d` / `let x = d` → NOT a param; the default is used (test_private_field_defaults.dang)
  - method/computed members (have a `{ body }`) are never constructor params

## Implicit constructor

- one positional parameter per non-default field, in **declaration order** (NOT required-first-then-defaults — a defaulted field may precede a required one; positional args still line up by declaration order)
  - e.g. `Mixed { pub publicWithDefault = "default"; let privateRequired: Int! }` constructs as `Mixed("default", 42)` (test_private_required_fields.dang)
- can also be called with named arguments (`Counter(value: 42)`, test_class_mutation.dang)
- field defaults are evaluated with `self` bound, so a default may reference earlier/sibling fields (test_constructors.dang `combined = prefix + "_" + suffix`)

```dang
Person("Alice", age: 30)
Person(name: "Alice")
```

## Zero-arg auto-construction

- a type whose constructor needs nothing (all fields have defaults / no required params) constructs on bare reference:
  `let p = Person` ≡ `let p = Person()` (test_constructors.dang, test_self.dang `MyClass.val`)
- exception: a constructor that requires a **block argument** is NOT auto-called by a bare reference (test errors/constructor_block_autocall.dang: `pub loop: Loop! = Loop` is an error)

## Explicit constructor: `new`

```dang
type Greeter {
  pub greeting: String!

  new(name: String!) {
    self.greeting = "hello, " + name
    self
  }
}
```

- declared as `new(args) { body }` or `new { body }` (no parens when no args, test_constructor_no_args.dang)
- no `pub` and no return-type annotation — both are errors (errors/constructor_pub_new.dang: "'new' is a constructor, not a method")
- `new` is only valid inside a `type` body (errors/new_outside_class.dang)
- overrides the implicit constructor (the type's fields no longer auto-become params; `new`'s arg list defines the signature)
- constructor args are *local* bindings — distinct from fields, even when same-named:
  - mutable: `foo = foo + 10` / `foo += foo` rebinds the arg, does NOT touch `self.foo` (test_constructor_arg_mutation.dang, test_constructor_param_reassign.dang)
  - shadow same-named fields and outer scope; bare name = arg, field write needs `self.field =` (test_constructor_arg_scope.dang)
  - NOT visible in method bodies — only in `new` (errors/constructor_arg_in_method_body.dang)
- must return the constructed type (`self`, or a method chain that returns it): body's last expression must be `Foo!`
  - returning another type errors: `new() must return Wrong!, got String!` (errors/constructor_wrong_return.dang)
  - returning `null` errors (errors/constructor_return_null_type_mismatch.dang)
- may end by chaining other methods, which propagate their forked `self` (test_constructor_method_chain.dang: `self.withSuffix("!")`)
- self-field mutation inside a loop accumulates into one fork (test_constructor_loop_mutation.dang)
- can accept block args: `new(&condition: Boolean!) { ... }` (test_constructor_block_arg.dang); a block param uses `&name`

## `self`

- bound during constructor and method execution (and during field-default / computed-field evaluation, test_self_class_evaluation.dang)
- bare names inside a method resolve against the current receiver first: bare `name` reads `self.name`, bare `incrBy(1)` calls `self.incrBy(1)` (test_self.dang vs test_self_mirror.dang — same asserts, one uses `self.`, one omits it)
- field **reads** never need `self.`; for **assignment**, both forms work:
  - bare `a += 1` / `value = v` on a field forks `self` and sets the field (test_class_desired_behavior.dang, test_class_immutability.dang, test_explicit_constructor.dang `Vector`)
  - `self.field = ...` is the explicit form; required only to disambiguate from a same-named local/arg
  - bare assignment binds a *local/arg* when the name is one in scope (shadowing); otherwise targets the field
- assigning a field forks the receiver (see [#mutation])
- `self` is the value returned by chainable methods (the final `self` returns the accumulated fork)

## Computed fields

- a member with a type and a body but no arg list is a computed field — a zero-arg function evaluated on `self` each access:

```dang
pub fullName: String! { firstName + " " + lastName }
```

- accessed like a plain field (`obj.fullName`, no call parens); recomputes against the current receiver (test_self.dang `dynamicAccess`)
- a defaulted-value member (`pub computedField = config.name + "_computed"`) is computed once at construction; a `{ body }` computed field is re-evaluated per access

## Implements

```dang
type Person implements Named & Identifiable { ... }
```

- see [#interfaces-unions]

## Forward references

- methods can reference types and fields defined later in the same file (test_class_forward_reference.dang)
- references also resolve across files in a directory module, order-independently (tests/test_dir_order_independent/: `a_consumer.dang` uses `Later` / `LaterInterface` declared in `z_types.dang`)
- defaults can reference sibling fields (evaluated with `self` bound)

> Meta: user-defined `type`s and imported GraphQL **input** types share the same `Type(args)` construction syntax. In tests, schema input types are constructed exactly like local types: `CreateUserInput(name: "Alice", email: "...")` passed as `Mutation.createUser(input: ...)` (test_mutation.dang). See [#graphql]. (verify whether a *local* `type` can be passed where the schema expects an input — not yet exercised by a test.)
