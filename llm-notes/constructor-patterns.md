# Constructor and Initialization Patterns in Sprout

## Type Construction Methods

Sprout uses copy-on-write semantics for object construction and mutation. There are several patterns for creating and initializing objects:

### Direct Type Reference
```sprout
type MyType {
  let value = 42
}

# Simply reference the type to get an instance
let instance = MyType
```

### Builder Pattern with Assignment
```sprout
type Container {
  let name = ""
  let size = 0
}

let container = Container
container.name = "my-container"
container.size = 100
```

### Constructor-like Methods
```sprout
type Compose {
  let dir: Directory!
  let files: [String!]!
  
  pub new(dir: Directory!, files: [String!]! = ["docker-compose.yml"]): Compose! {
    let instance = Compose
    instance.dir = dir
    instance.files = files
    instance
  }
}
```

### Record Literals (NOT for type construction)
Record literals `{{}}` are for creating anonymous records, not for constructing instances of named types:

```sprout
# This creates an anonymous record
let record = {{ name: "test", value: 42 }}

# This is WRONG for type construction
# let instance = MyType {{ field: value }}  # Don't do this
```

## Key Principles

1. **Types are prototypes**: Referencing a type name gives you an instance
2. **Copy-on-write**: Assignments create new objects, preserving immutability
3. **Method chaining**: Methods typically return `self` for fluent APIs
4. **Constructor methods**: Use `new()` or similar methods for complex initialization

## Common Patterns

### Fluent API
```sprout
let daemon = Daemon.
  withVersion("24.0").
  withCache(cacheVolume("docker-cache")).
  service
```

### Conditional Initialization
```sprout
let container = baseContainer
if (needsCache) {
  container = container.withMountedCache("/cache", cache)
}
```

### Factory Methods
```sprout
type Docker {
  pub daemon: Daemon! {
    Daemon  # Return default instance
  }
  
  pub compose(dir: Directory!): Compose! {
    Compose.new(dir)  # Use constructor method
  }
}
```

## Important Notes

- **No explicit constructors**: Types don't have constructor syntax like `new Type()`
- **Assignment is mutation**: `obj.field = value` modifies the object
- **Method returns**: Constructor-like methods should return the modified instance
- **Default values**: Use field initialization in type definition for defaults