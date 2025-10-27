;; Keywords
[
  (let_token)
  (pub_token)
] @keyword
[
  (type_token)
  (if_token)
  (else_token)
  (while_token)
  (for_token)
  (in_token)
  (match_token)
  (with_token)
  (assert_token)
  (directive_token)
  (on_token)
  (import_token)
  (as_token)
] @keyword.control

;; Literals
(string) @string
(string (immediate_escape) @string.escape) @string
(doc_string) @string
(triple_quote_string) @string
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
  (double_interro_token)
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
  (id_symbol) @function.method)
(type_and_args_and_block_slot
  (id_symbol) @function.method)
(type_and_value_slot
  (id_symbol) @function.method)
(value_only_slot
  (id_symbol) @function.method)
(type_only_slot
  (id_symbol) @function.method)

(arg_with_block_default
  (id_symbol) @variable.parameter)
(arg_with_type
  (id_symbol) @variable.parameter)
(arg_with_default
  (id_symbol) @variable.parameter)

;; Class definitions
(class
  (id_symbol) @type)

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

;; Object selection (multi-field selection)
(object_selection) @punctuation.bracket
(field_selection
  (id) @property)

((symbol) @variable.special
  (#match? @variable.special "^(self)$"))

;; Error highlighting
(ERROR) @error
