\use-plugin{dang}

# Strings {#strings}

> Meta: this page is mostly a method reference. Group by what you do with it; don't try to be exhaustive — link to [#stdlib] for the full signatures.

## Construction

- literals: see [#literals]
- concat: `+`
- compound update: `s += "..."`
- template interpolation: `` `hello ${name}` `` (see [#literals])

## Conversion

- `toString(value)` — JSON-encodes non-strings; passes strings through
- `value :: String!` — explicit cast where types align (see [#types])

## Inspection

> Strings have no `.length` / `.isEmpty` methods — those are list methods ([#collections]). Use the predicates below.

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

- `s.split(separator)`, `s.split(separator, limit: n)` — empty separator splits into characters; `limit` caps the number of parts (last part keeps the remainder)
- `s.replace(old, new)`, `s.replace(old, new, count: n)` — `count` defaults to -1 (replace all); empty `old` inserts between characters

## Future: regex

> Meta: see `regexp.md` for the planned `Regexp` scalar, `Match` object, and `containsMatch/match/matchAll/replaceMatches/rewriteMatches/splitMatches` family. Add a section here when it lands.
