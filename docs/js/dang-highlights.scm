;; Keywords
[
  (let_token)
  (pub_token)
] @keyword
[
  (type_token)
  (interface_token)
  (union_token)
  (implements_token)
  (enum_token)
  (scalar_token)
  (if_token)
  (else_token)
  (for_token)
  (break_token)
  (continue_token)
  (case_token)
  (directive_token)
  (on_token)
  (import_token)
  (new_token)
  (try_token)
  (catch_token)
  (raise_token)
  (return_token)

  (and_token)
  (or_token)
] @keyword.control

(self_keyword) @variable.special

;; Literals
(string) @string
(string (immediate_escape) @string.escape) @string
(doc_string) @string
(triple_quote_string) @string
(single_template "`" @string)
(single_template
  (single_template_part !e) @string)
(multi_template
  (multi_template_open_token) @string)
(multi_template
  (multi_template_close_token) @string)
(multi_template
  (multi_template_part !e) @string)
(multi_template
  (lang_tag_part (lang_tag_name) @label))
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
  (arrow_token)
  (ampersand_token)
] @operator
["{{" "}}" "{" "}" "[" "]" "(" ")"] @punctuation.bracket
[
  (comma_token)
  (dot_token)
] @punctuation.delimiter
["@" "|"] @punctuation.special

;; Whitespace before `${` is parsed as part of the interpolation token because
;; whitespace is normally an extra; keep that prefix highlighted as string.
((single_template_part
  "${" @string
  e: (_)
  "}")
  (#offset! @string 0 0 0 -2))
((multi_template_part
  "${" @string
  e: (_)
  "}")
  (#offset! @string 0 0 0 -2))

;; Template interpolation delimiters. Keep these after the generic bracket
;; captures so the closing `}` is highlighted as interpolation punctuation.
((single_template_part
  "${" @punctuation.special
  e: (_)
  "}")
  (#trim! @punctuation.special 0 1 0 0))
((single_template_part
  "${"
  e: (_)
  "}" @punctuation.special)
  (#trim! @punctuation.special 0 1 0 0))
((multi_template_part
  "${" @punctuation.special
  e: (_)
  "}")
  (#trim! @punctuation.special 0 1 0 0))
((multi_template_part
  "${"
  e: (_)
  "}" @punctuation.special)
  (#trim! @punctuation.special 0 1 0 0))

;; Identifiers - using more generic patterns
(symbol) @variable
(call (symbol) @function.method)

;; Key-value pairs
(key_value
  (word_token) @property)

;; Special highlighting for built-in functions
((call
  (symbol) @function.builtin)
  (#match? @function.builtin "^(assert|print|toJSON|toString)$"))
((symbol_block
  (symbol) @function.builtin)
  (#match? @function.builtin "^(assert|print|toJSON|toString)$"))

;; Field selections
(select_or_call
  (field_id) @function.method)

;; Object selection (multi-field selection)
(object_selection) @punctuation.bracket
(field_selection
  (id) @property)

;; Error highlighting
(ERROR) @error

;; Field definitions
(type_and_block_field
  (symbol) @function.method)
(type_and_args_and_block_field
  (symbol) @function.method)
(type_and_value_field
  (symbol) @function.method)
(value_only_field
  (symbol) @function.method)
(type_only_field
  (symbol) @function.method)
(type_only_fun_field
  (symbol) @function.method)

(arg_with_block_default
  (symbol) @variable.parameter)
(arg_with_type
  (symbol) @variable.parameter)
(arg_with_default
  (symbol) @variable.parameter)

;; Object definitions
(object_decl (symbol) @type)
(implements (symbol) @type)
(interface_decl (symbol) @type)
(enum_decl (symbol) @type)
(enum_decl (caps_symbol) @property)
(scalar_decl (symbol) @type)
