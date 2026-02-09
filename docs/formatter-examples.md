# Dang Formatter Examples

This document shows before/after examples for `dang fmt`, an opinionated code formatter for Dang.

## Principles

1. **Two spaces for indentation** - consistent, compact indentation
2. **Indent levels over alignment** - don't align to arbitrary columns
3. **Leading dots for chains** - `.method()` at start of line, not trailing
4. **Triple-quoted doc strings** - `"""..."""` everywhere
5. **Multi-line long arguments** - one argument per line when it doesn't fit
6. **No trailing commas** - last element has no comma
7. **Consistent spacing** - single spaces around operators, after colons
8. **No parens for zero-arg calls** - `foo.bar` not `foo.bar()`
9. **80 character line limit** - break when approaching this limit
10. **Blank lines between members** - one blank line between type members
11. **Authoritative formatting** - no escape hatch, like `go fmt`

---

## 1. Indentation (Two Spaces)

### Before
```dang
type Foo {
  pub bar: String! {
    container
      .from("alpine")
      .withExec(["echo", "hello"])
  }
}
```

### After
```dang
type Foo {
  pub bar: String! {
    container
      .from("alpine")
      .withExec(["echo", "hello"])
  }
}
```

---

## 2. Method Chain Splitting

Long method chains should be split with the `.` at the **start** of each continuation line, not at the end of the previous line.

### Before (trailing dots)
```dang
pub build: Container! {
  container.
    from("alpine:latest").
    withDirectory("/src", source).
    withWorkdir("/src").
    withExec(["make", "build"])
}
```

### After (leading dots)
```dang
pub build: Container! {
  container
    .from("alpine:latest")
    .withDirectory("/src", source)
    .withWorkdir("/src")
    .withExec(["make", "build"])
}
```

### Short Chains Stay Inline

```dang
# This is fine - short enough to stay on one line
pub name: String! {
  user.profile.displayName
}
```

---

## 3. Triple-Quoted Doc Strings

All doc strings should use triple quotes, even single-line ones.

### Before
```dang
# Build the container
pub build: Container! {
  container.from("alpine")
}

"This is a description"
pub foo: String! = "bar"
```

### After
```dang
"""
Build the container
"""
pub build: Container! {
  container.from("alpine")
}

"""
This is a description
"""
pub foo: String! = "bar"
```

### Multi-line Doc Strings

```dang
"""
Build and run a `dang` command.

Example usage:

  # Run a Dang script
  dang script.dang

  # Run all .dang files in a directory
  dang ./my-module
"""
pub dang(args: [String!]!): Void {
  base.withExec(["dang"] + args).sync
  null
}
```

---

## 4. Long Function Arguments

When function call arguments exceed the line length, split them one per line.

### Before
```dang
pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: ["Session.vim", "dang", "zed-dang/grammars/", ".env", ".envrc", ".dagger"])
```

### After
```dang
pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
  "Session.vim"
  "dang"
  "zed-dang/grammars/"
  ".env"
  ".envrc"
  ".dagger"
])
```

### Function Calls With Many Arguments

### Before
```dang
container.withExec(["go", "test", "-v", "./..."], experimentalPrivilegedNesting: true, expect: ReturnType.ANY)
```

### After
```dang
container.withExec(
  ["go", "test", "-v", "./..."]
  experimentalPrivilegedNesting: true
  expect: ReturnType.ANY
)
```

### Nested Calls

### Before
```dang
pub result: Container! {
  container.import(container.from("cgr.dev/chainguard/apko").withMountedCache("/apkache/", cacheVolume("apko")).withNewFile("/config.yml", contents: toJSON(config)).withExec(["apko", "build", "--cache-dir", "/apkache/", "/config.yml", "latest", "layout.tar"]).file("layout.tar"))
}
```

### After
```dang
pub result: Container! {
  container.import(
    container
      .from("cgr.dev/chainguard/apko")
      .withMountedCache("/apkache/", cacheVolume("apko"))
      .withNewFile("/config.yml", contents: toJSON(config))
      .withExec([
        "apko"
        "build"
        "--cache-dir"
        "/apkache/"
        "/config.yml"
        "latest"
        "layout.tar"
      ])
      .file("layout.tar")
  )
}
```

---

## 5. No Trailing Commas

The last element in a list or argument list should not have a trailing comma.

### Before
```dang
pub packages = [
  "bash",
  "git",
  "go",
]

pub config = {{
  name: "foo",
  port: 8080,
}}

container.withExec(
  ["echo", "hello"],
  experimentalPrivilegedNesting: true,
)
```

### After
```dang
pub packages = [
  "bash"
  "git"
  "go"
]

pub config = {{
  name: "foo"
  port: 8080
}}

container.withExec(
  ["echo", "hello"]
  experimentalPrivilegedNesting: true
)
```

---

## 6. Type Declarations

### Before
```dang
type Foo   {
pub   bar:String!="hello"
  pub baz  :  Int!
    pub qux(a:String!,b:Int!=42):Container!{
    container.from("alpine")
  }
}
```

### After
```dang
type Foo {
  pub bar: String! = "hello"
  pub baz: Int!
  pub qux(a: String!, b: Int! = 42): Container! {
    container.from("alpine")
  }
}
```

---

## 7. Control Flow

### Conditionals

```dang
# Short conditionals can stay on one line
pub status = if (enabled) { "active" } else { "inactive" }

# Longer conditionals should be split
pub result: String! {
  if (condition) {
    doSomething
  } else if (otherCondition) {
    doSomethingElse
  } else {
    fallback
  }
}
```

### For Loops

```dang
pub process: Container! {
  let ctr = base
  for (pkg in packages) {
    ctr = ctr
      .withExec(["go", "-C", pkg, "get", dependency])
      .withExec(["go", "-C", pkg, "mod", "tidy"])
  }
  ctr
}
```

### Case Expressions

```dang
pub arch = case (defaultPlatform) {
  "linux/amd64" => "x86_64"
  "linux/arm64" => "arm64"
  else => "unknown"
}
```

---

## 8. Directives

### Before
```dang
type Foo @deprecated(reason:"use Bar")@experimental{
pub field:String!@auth(role:"admin")@cache(ttl:60)
}
```

### After
```dang
type Foo @deprecated(reason: "use Bar") @experimental {
  pub field: String! @auth(role: "admin") @cache(ttl: 60)
}
```

### Long Directive Arguments

```dang
pub source: Directory! @defaultPath(path: "/") @ignorePatterns(patterns: [
  "Session.vim"
  "dang"
  ".env"
])
```

---

## 9. Object Literals

### Before
```dang
pub config = {{contents:{{packages:[]::[String!]!,repositories:[]::[String!]!,keyring:[]::[String!]!}},cmd:"",archs:[arch]}}
```

### After
```dang
pub config = {{
  contents: {{
    packages: []::[String!]!
    repositories: []::[String!]!
    keyring: []::[String!]!
  }}
  cmd: ""
  archs: [arch]
}}
```

---

## 10. Block Arguments

### Before
```dang
list.filter { x => x > 0 }.map { y => y * 2 }
```

### After
```dang
list
  .filter { x => x > 0 }
  .map { y => y * 2 }
```

### Multi-line Block Arguments

```dang
pub doubled: [[Int!]!]! = nested.map { inner =>
  inner.map { x => x * 2 }
}
```

### Block Args With Function Calls

```dang
pub result: String! = myFun() { x => toJSON(x) }

pub result: String! = withArg("prefix: ") { x => toJSON(x) }
```

---

## 11. Zero-Argument Calls

Don't use parentheses for zero-argument field/method calls.

### Before
```dang
pub result: Container! {
  base
    .build()
    .sync()
}

let value = foo.getValue()
```

### After
```dang
pub result: Container! {
  base
    .build
    .sync
}

let value = foo.getValue
```

---

## 12. Blank Lines Between Members

One blank line between type members.

### Before
```dang
type Foo {
  pub a: String! = "hello"
  pub b: Int! = 42
  pub c: Container! {
    container.from("alpine")
  }
  let private: String! = "secret"
}
```

### After
```dang
type Foo {
  pub a: String! = "hello"

  pub b: Int! = 42

  pub c: Container! {
    container.from("alpine")
  }

  let private: String! = "secret"
}
```

---

## 13. Comments

Comments stay with the code they describe:

```dang
type Build {
  # Base image for all builds
  let base: Container! {
    container.from("alpine")
  }

  """
  Build the application.
  """
  pub build: File! {
    base
      # Install dependencies first
      .withExec(["apk", "add", "go"])
      # Then build
      .withExec(["go", "build", "-o", "/out/app"])
      .file("/out/app")
  }
}
```

---

## 14. Spacing Rules

### Operators
```dang
# Spaces around binary operators
pub sum = a + b
pub product = x * y
pub check = foo == bar and baz != qux

# No space for unary operators
pub negated = !flag
```

### Colons and Types
```dang
# Space after colon in type annotations
pub name: String!
pub count: Int! = 42

# Space after colon in named arguments
container.from(address: "alpine")
```

### Parentheses
```dang
# No space inside parentheses
if (condition) { ... }
foo(arg1, arg2)

# No space before opening paren in calls
container.withExec(["echo"])
```


