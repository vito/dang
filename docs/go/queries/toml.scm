; TOML highlight query, adapted from tree-sitter-toml queries/highlights.scm
; at the version pinned in go.mod. Upstream captures bare keys as @type and
; whole pairs as @property, which would paint keys in the type color; keys
; here are @property (the conventional capture), matching how the site has
; always colored TOML keys.

(bare_key) @property
(quoted_key) @string

(boolean) @constant

(comment) @comment

(string) @string

[
  (integer)
  (float)
] @number

[
  (offset_date_time)
  (local_date_time)
  (local_date)
  (local_time)
] @string.special

[
  "."
  ","
] @punctuation.delimiter

"=" @operator

[
  "["
  "]"
  "[["
  "]]"
  "{"
  "}"
] @punctuation.bracket
