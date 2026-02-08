* [x] Cache GraphQL schema introspection (but not for `dagger`)
* [x] Multi-select with args: `viewer.{repositories(first:100).{nodes.{name}}}`
* [x] Figure out pagination patterns (for e.g. GitHub style)
* [ ] Add boolean operators (&&, ||, !)
* [ ] Add negation support

## Formatter bugs

* [x] Parameter docstrings are mangled - docstrings for function parameters get smashed onto the same line as parameter names
* [x] Extra blank lines at start of blocks - blank lines inserted after opening braces of function bodies
* [x] Comments inside list literals get moved outside the list
* [x] Trailing whitespace in docstrings - empty lines within docstrings get tabs added
* [x] Double blank lines between functions - two blank lines instead of one between function definitions
* [x] Long `assert` lines split into multi-line blocks instead of staying on one line
* [x] Multi-select `.{field1, field2}` flattened to a single line instead of allowing multi-line
* [x] Blank line before `self` return in method bodies stripped
* [x] Triple-quoted string content dedented to column 0; inline `"""..."""` expanded to multi-line
* [x] Inline enum declaration expanded to one-value-per-line
* [x] Single-line block expressions expanded to multi-line; comma separators become newlines
* [x] Long method chains wrapped so `.method` starts on a new line
* [x] Multiple prefix directives on separate lines collapsed onto one line
* [x] Commas removed from object literal field separators in `{{...}}`
* [x] TODO and explanatory comments stripped from code
