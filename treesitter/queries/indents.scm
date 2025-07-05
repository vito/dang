;; Indent on structural elements
[
  (block)
  (list)
  (class)
  (conditional)
  (else_clause)
  (lambda)
  (match)
  (object)
  (paren_form)
  (arg_values)
  (arg_types)
  (lambda_args)
  (pattern_args)
  (message_args)
] @indent

;; Indent on opening brackets
[
  "{"
  "["
  "("
] @indent

;; Dedent on closing brackets
[
  "}"
  "]"
  ")"
] @outdent

;; Branch pairs for balanced indentation
[ "(" ")" ] @branch
[ "{" "}" ] @branch
[ "[" "]" ] @branch

;; Auto-indent for errors and comments
[
  (ERROR)
  (comment_token)
] @auto
