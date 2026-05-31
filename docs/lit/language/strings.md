\use-plugin{dang}

# Strings {#strings}

> Meta: this page is mostly a method reference. Group by what you do with it; don't try to be exhaustive — link to `reference/stdlib.md` for the full signatures.

## Construction

- literals: see [literals](./literals.md)
- concat: `+`
- compound update: `s += "..."`
- template interpolation: `` `hello #{name}` ``

## Conversion

- `toString(value)` — JSON-encodes non-strings; passes strings through
- `value :: String!` — explicit cast where types align

## Inspection

- `s.length` — `Int!`
- `s.isEmpty` — `Boolean!`
- `s.contains(sub)`
- `s.hasPrefix(p)`
- `s.hasSuffix(p)`

## Case

- `s.toUpper`
- `s.toLower`

## Trim and pad

- `s.trim(charset)`, `s.trimLeft(charset)`, `s.trimRight(charset)`, `s.trimSpace`
- `s.trimPrefix(p)`, `s.trimSuffix(p)`
- `s.padLeft(width)`, `s.padRight(width)`, `s.center(width)`

## Split / replace

- `s.split(sep)`, `s.split(sep, limit: n)`
- `s.replace(old, new)`, `s.replace(old, new, count: n)`

## Future: regex

> Meta: see `regexp.md` for the planned `Regexp` scalar, `Match` object, and `containsMatch/match/matchAll/replaceMatches/rewriteMatches/splitMatches` family. Add a section here when it lands.
