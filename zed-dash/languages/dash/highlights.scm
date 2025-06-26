;; Keywords
[
  (pvt_token)
  (pub_token)
] @keyword
[
  (cls_token)
  (if_token)
  (else_token)
  (let_token)
  (in_token)
  (match_token)
  (with_token)
] @keyword.control

;; Literals
(string) @string
(int) @constant.numeric
(boolean) @constant.builtin.boolean
(null) @constant.builtin

;; Comments
(comment_token) @comment.line
(upper_token) @type

;; Operators and punctuation
[
  "="
  (interro_token)
  (bang_token)
] @operator
["{" "}" "[" "]" "(" ")"] @punctuation.bracket
[
  (comma_token)
  (dot_token)
] @punctuation.delimiter

;; Identifiers - using more generic patterns
; (symbol_or_call) @variable

;; Special highlighting for built-in functions
; ((symbol_or_call) @function.builtin
;   (#match? @function.builtin "^(print)$"))

;; Key-value pairs
(key_value
  (word_token) @property)

;; Slot definitions
(type_and_block_slot
  (id) @function.method)
(type_and_args_and_block_slot
  (id) @function.method)
(type_and_value_slot
  (id) @function.method)
(value_only_slot
  (id) @function.method)
(type_only_slot
  (id) @function.method)

(arg_with_block_default
  (id) @variable.parameter)
(arg_with_type
  (id) @variable.parameter)
(arg_with_default
  (id) @variable.parameter)

;; Class definitions
(class
  (id) @type)

;; Special highlighting for print function
(symbol) @variable
(call
  (id) @function.method)

((symbol_or_call
  (id) @function.builtin)
  (#match? @function.builtin "^(print)$"))

;; Field selections
(select_or_call
  (id) @function.method)

;; Error highlighting
(ERROR) @error
