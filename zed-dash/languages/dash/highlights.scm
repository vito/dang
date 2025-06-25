;; Keywords
["pub" "pvt"] @keyword
["cls" "if" "else" "let" "in" "match" "with"] @keyword.control

;; Literals
(string) @string
(number) @constant.numeric
(boolean) @constant.builtin.boolean
(null) @constant.builtin

;; Comments
(comment) @comment.line

;; Operators and punctuation
["=" ":" "->"] @operator
["{" "}" "[" "]" "(" ")"] @punctuation.bracket
["," "."] @punctuation.delimiter

;; Identifiers - using more generic patterns
(symbol) @variable

;; Special highlighting for Dagger-related operations
((symbol) @function.builtin
  (#match? @function.builtin "^(container|directory|file|secret|service|cache|git)$"))

;; Error highlighting
(ERROR) @error