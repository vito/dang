; Keywords
[
  "pub"
  "let"
  "type"
  "directive"
  "on"
  "if" 
  "else"
  "match"
  "with"
  "assert"
] @keyword

; Directive keywords
(directive_token) @keyword.directive
(on_token) @keyword.directive

; Directive names and applications
(directive_name) @function.macro
(directive_application name: (id) @function.macro)

; Directive locations
(directive_location name: (upper_id) @constant.builtin)

; Types
(named_type name: (upper_id) @type)
(type_variable name: (_) @type.parameter)

; Literals
(string) @string
(int) @constant.numeric.integer
(boolean) @constant.builtin.boolean
(null) @constant.builtin

; Operators
[
  "="
  "+="
  "+"
  "-"
  "*"
  "/"
  "%"
  "=="
  "!="
  "<"
  "<="
  ">"
  ">="
  "?"
  "::"
] @operator

; Punctuation
[
  "("
  ")"
  "["
  "]"
  "{"
  "}"
  "{{"
  "}}"
] @punctuation.bracket

[
  ","
  ":"
  ";"
  "."
  "|"
  "!"
  "@"
] @punctuation.delimiter

; Arrow operator
"->" @punctuation.special

; Identifiers
(id) @variable

; Function definitions
(fun_decl name: (id) @function)

; Class definitions  
(class name: (id) @type.definition)

; Slot declarations
(slot_decl name: (id) @variable.member)

; Comments
(comment_token) @comment

; Error highlighting for invalid syntax
(ERROR) @error