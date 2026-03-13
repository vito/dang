# Constructor and Initialization Patterns in Dang

## Type Construction Methods

Dang uses copy-on-write semantics for object construction and mutation. There
are two ways to construct objects: **field-derived constructors** (implicit)
and **explicit `new()` constructors**.

### Field-Derived Constructors (Implicit)

When a type has no `new()` block, its public fields become constructor
parameters in declaration order. Fields with defaults are optional:

```dang
type Config {
  pub host: String!
  pub port: Int! = 8080
  pub debug: Boolean! = false
}

# Positional or named arguments
let c1 = Config("localhost")            # port=8080, debug=false
let c2 = Config("localhost", 9090)
let c3 = Config(host: "localhost", debug: true)
```

Types with all-default or no-parameter fields support **auto-calling** — just
reference the type name:

```dang
type Defaults {
  pub name: String! = "Unknown"
  pub age: Int! = 0
}

let d = Defaults          # auto-called, name="Unknown", age=0
let d2 = Defaults("Alice") # name="Alice", age=0
```

### Explicit `new()` Constructors

For complex initialization logic, define a `new()` block. The constructor
must assign all required fields and return `self` as its last expression:

```dang
type Greeter {
  pub greeting: String!

  new(name: String!) {
    self.greeting = "Hello, " + name + "!"
    self
  }
}

assert { Greeter("World").greeting == "Hello, World!" }
```

Constructor args with defaults support auto-calling:

```dang
type Logger {
  pub prefix: String!

  new(name: String! = "app", level: String! = "INFO") {
    self.prefix = "[" + level + "] " + name
    self
  }
}

assert { Logger.prefix == "[INFO] app" }
```

### Calling Methods from Constructors

Constructors can call methods on `self`. The return value carries the
mutations (copy-on-write):

```dang
type Pipeline {
  pub steps: [String!]! = []

  new(name: String!) {
    self.steps = [name]
    self.addStep("init").addStep("ready")
  }

  pub addStep(step: String!): Pipeline! {
    self.steps = self.steps + [step]
    self
  }
}

assert { Pipeline("start").steps == ["start", "init", "ready"] }
```

### Mutating Self in Loops Inside Constructors

Closures inside constructors (e.g. `.each` blocks) share the constructor's
dynamic scope for `self`. This means mutations to `self.field` accumulate
across loop iterations:

```dang
type Accumulator {
  pub items: [String!]!

  new(source: [String!]!) {
    self.items = []
    source.each { item =>
      self.items += [item]   # Each iteration sees previous mutations
    }
    self
  }
}

assert { Accumulator(["a", "b", "c"]).items == ["a", "b", "c"] }
```

This works because `ConstructorEnv` uses a shared dynamic scope cell.
See `mutability-and-assignment.md` for the full explanation of shared vs
isolated dynamic scope.

### Bare Field Assignment in Constructors

When constructor parameter names don't shadow field names, you can assign
fields without the `self.` prefix:

```dang
type Vector {
  pub x: Int! = 0
  pub y: Int! = 0

  new(vx: Int!, vy: Int!) {
    x = vx
    y = vy
    self
  }
}
```

When parameter names match field names, use `self.` to disambiguate:

```dang
type Point {
  pub x: Int!
  pub y: Int!

  new(x: Int!, y: Int!) {
    self.x = x    # self.x is the field, x is the parameter
    self.y = y
    self
  }
}
```

## Record Literals (NOT for type construction)

Record literals `{{}}` create anonymous records, not instances of named types:

```dang
let record = {{ name: "test", value: 42 }}
```

## Common Patterns

### Fluent API
```dang
let daemon = Daemon.
  withVersion("24.0").
  withCache(cacheVolume("docker-cache")).
  service
```

### Builder Pattern
```dang
type MyClass {
  pub name: String! = "Jeff"

  pub withName(name: String!): MyClass! {
    self.name = name
    self
  }
}
```

### Conditional Initialization
```dang
let container = baseContainer
if (needsCache) {
  container = container.withMountedCache("/cache", cache)
}
```

## Key Principles

1. **Copy-on-write**: Assignments create new objects; originals are unchanged
2. **Method chaining**: Methods return `self` for fluent APIs
3. **Constructor closures share scope**: Blocks inside `new()` see accumulated
   `self` mutations (shared dynamic scope cell)
4. **Method calls are isolated**: Calling a method doesn't mutate the caller's
   reference (fresh dynamic scope cell)
