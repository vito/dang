;; Indent on opening brackets
[
  "{"
  "["
  "("
] @indent

;; Indent after certain keywords
[
  "if"
  "else" 
  "let"
  "match"
  "cls"
  "pub"
  "pvt"
] @indent

;; Dedent on closing brackets
[
  "}"
  "]"
  ")"
] @outdent

;; Dedent else clauses
"else" @outdent

;; Indent function bodies
(fun_decl body: (_) @indent)

;; Indent conditional branches
(conditional 
  then: (_) @indent
  else: (_)? @indent)

;; Indent let expressions
(let expr: (_) @indent)

;; Indent match cases
(match cases: (_) @indent)

;; Indent class bodies
(class_decl value: (_) @indent)

;; Indent lambda bodies
(lambda expr: (_) @indent)