;; Keywords
[
  (let_token)
  (pub_token)
] @keyword
[
  (type_token)
  (if_token)
  (else_token)
  (match_token)
  (with_token)
  (assert_token)
  (directive_token)
  (on_token)
] @keyword.control

;; Literals
(string) @string
(int) @constant.numeric
(boolean) @constant.builtin.boolean
(null) @constant.builtin

;; Comments
(comment_token) @comment.line
(upper_token) @type

;; Directives
(directive_name) @function.macro
(directive_application
  (id) @function.macro)
(directive_location
  (upper_id) @constant.builtin)

;; Operators and punctuation
[
  (equal_token)
  (plus_equal_token)
  (interro_token)
  (bang_token)
  (lambda_token)
  (arrow_token)
] @operator
["{{" "}}" "{" "}" "[" "]" "(" ")"] @punctuation.bracket
[
  (comma_token)
  (dot_token)
] @punctuation.delimiter
["@" "|"] @punctuation.special

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

((call
  (id) @function.builtin)
  (#match? @function.builtin "^(print)$"))

;; Field selections
(select_or_call
  (id) @function.method)

((symbol) @variable.special
  (#match? @variable.special "^(self)$"))

;; Error highlighting
(ERROR) @error
