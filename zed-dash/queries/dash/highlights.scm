;; Keywords
(visibility) @keyword
["pub" "pvt"] @keyword

["cls" "if" "else" "let" "in" "match" "with"] @keyword.control

;; Types and type annotations
(type_annotation) @type
(named_type) @type
(list_type) @type
(non_null_type) @type

;; Function definitions and calls
(fun_decl name: (identifier) @function)
(fun_call fun: (identifier) @function.call)

;; Lambda expressions
(lambda args: (lambda_args) @parameter) @function
(lambda_args (identifier) @parameter)

;; Variables and identifiers
(slot_decl name: (identifier) @variable)
(let name: (identifier) @variable)
(symbol) @variable

;; Literals
(string) @string
(int) @constant.numeric.integer
(boolean) @constant.builtin.boolean
(null) @constant.builtin

;; Comments
(comment) @comment.line

;; Operators and punctuation
["=" ":" "->"] @operator
["{" "}" "[" "]" "(" ")"] @punctuation.bracket
["," "."] @punctuation.delimiter

;; Special forms
(conditional 
  condition: (_) @expression
  then: (_) @expression
  else: (_)? @expression)

(match 
  expr: (_) @expression
  cases: (_) @expression)

;; Pattern matching
(pattern_wildcard) @constant.builtin
(pattern_literal) @constant
(pattern_constructor) @constructor
(pattern_variable) @variable

;; Lists and records
(list) @punctuation.bracket
(record) @punctuation.bracket
(record_field key: (identifier) @property)

;; Class definitions  
(class_decl name: (identifier) @type.class)

;; Special highlighting for Dagger-related operations
((identifier) @function.builtin
  (#match? @function.builtin "^(container|directory|file|secret|service|cache|git)$"))

;; Error highlighting for undefined symbols
(ERROR) @error