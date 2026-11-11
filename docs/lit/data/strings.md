\use-plugin{dang}
\literate-fences

# Strings {#strings}

> Meta: this page is mostly a method reference. Group by what you do with it; don't try to be exhaustive — link to [#stdlib] for the full signatures.

> The examples on this page are live: they share one Dang environment, so
> later snippets use earlier definitions. Each result is computed and baked
> in by the docs build — edit a snippet and hit Run ▶ to replay the page in
> your browser. Blocks that show an error are *supposed* to fail: the build
> verifies the failure the same way it verifies the results.

## Construction

Write strings as literals (see [#literals]), join them with `+`, and append
with `+=`. A double-quoted string interpolates `${...}`; a single-quoted one
keeps its text as written:

```dang
let name = "Ada"
let greeting = "hello, " + name
greeting += "!"

[greeting, "${name} wrote the first program", '${name} stays put']
```

Backticks interpolate too, but take every backslash literally, so they're
the form for code, like the regular expressions below.

## Conversion

`toString(value)` passes strings, enum values and custom scalar values
through as their bare text, and writes anything else as the Dang literal it
would be written as, its strings quoted. Interpolation, `List.join` and
`print` show values the same way:

```dang
[toString(42), toString(["one", "two"]), toString({{ a: 1 }}), toString("plain")]
```

`value :: String!` turns a custom scalar back into a string (see
[#nullability]):

```dang
let docs = "docs/../README.md" :: Path!

(docs :: String!).toUpper
```

`.toInt` parses an integer, in base 10 unless you pass a `base` from 2 to 36.
A cast can't do this — `"42" :: Int!` is a type error:

```dang
["42".toInt, "ff".toInt(base: 16), "101".toInt(base: 2)]
```

Anything that isn't an integer in that base raises, so `rescue` supplies a
fallback (see [#errors]):

```dang
"0x1f".toInt(base: 16) rescue -1
```

## Inspection

`.length` counts characters (Unicode code points, not bytes):

```dang
"héllo".length
```

`.isEmpty` asks whether there are none:

```dang
["".isEmpty, greeting.isEmpty]
```

`contains`, `hasPrefix`, and `hasSuffix` look for a substring:

```dang
[greeting.contains("Ada"), greeting.hasPrefix("hello"), greeting.hasSuffix("?")]
```

## Case

```dang
[name.toUpper, name.toLower]
```

## Trim and pad

`trim`, `trimLeft`, and `trimRight` remove any of the given characters from
the ends. `trimSpace` removes whitespace:

```dang
["--draft--".trim("-"), "--draft--".trimLeft("-"), "  draft \n".trimSpace]
```

`trimPrefix` and `trimSuffix` remove one exact prefix or suffix, if it's
there:

```dang
["v1.2.3".trimPrefix("v"), "main.dang".trimSuffix(".dang")]
```

`padLeft`, `padRight`, and `center` add spaces up to a width, counted in
characters like `.length`. `center` puts an odd extra space on the right:

```dang
["7".padLeft(3), "7".padRight(3) + "|", "hi".center(5) + "|"]
```

## Split / replace

`split` cuts at each separator. `limit` caps the number of parts, with the
last part keeping the rest, and an empty separator splits into characters:

```dang
["a,b,c".split(","), "a,b,c".split(",", limit: 2), "abc".split("")]
```

`replace` replaces every match by default. `count` limits how many, and an
empty `old` inserts between characters:

```dang
["a-b-c".replace("-", "+"), "a-b-c".replace("-", "+", count: 1), "abc".replace("", ".")]
```

## Regex

> Regex methods — `containsMatch`, `match`, `matchAll`, `replaceMatches`, `rewriteMatches`, `splitMatches` — take a `Regexp!` pattern. A backtick template string auto-coerces to the `Regexp` scalar, so a pattern is usually written `` `\d+` `` (Go `regexp/syntax`). Full signatures are on [#stdlib].

```dang
let version = "dang v2.1.3"

version.containsMatch(`v\d+`)
```

`match` returns the first match, or `null` when there is none:

```dang
version.match(`v(\d+)\.(\d+)`).captures
```

```dang
version.match(`beta`)
```

`matchAll` returns every match:

```dang
"a1 b22 c333".matchAll(`\d+`).map { _.string }
```

`replaceMatches` fills in `$1`, `$2`, or `$name` from the match's groups.
`${name}` works too, but a double-quoted string would interpolate it, so
write that replacement in single quotes. `rewriteMatches` computes each
replacement with a block:

```dang
[
  "2024-01-15".replaceMatches(`(\d+)-(\d+)-(\d+)`, "$3/$2/$1"),
  "2024-01-15".replaceMatches(`(?P<year>\d+)-\d+-\d+`, '${year}!'),
  "hello world".rewriteMatches(`\w+`) { m => m.string.toUpper },
]
```

`splitMatches` splits at each match:

```dang
"a1b22c".splitMatches(`\d+`)
```

Only a literal converts to a `Regexp!` on its own (see [#enums-scalars]). A
pattern held in a variable needs a cast:

```dang
let digits = `\d+`
```

```dang-failure
version.containsMatch(digits)
```

```dang
version.containsMatch(digits :: Regexp!)
```

### `Match` object

> Returned by `.match`, and as the elements of `.matchAll`; a missing match is `null`. It only arises from these String/Regexp APIs, so it lives here rather than as a top-level stdlib module.

```dang
let release = version.match(`v(?P<major>\d+)\.(?P<minor>\d+)`)!

release.named
```

```dang
[release.start, release.end]
```

\stdlib-methods{Match}
