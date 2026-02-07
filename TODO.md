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
