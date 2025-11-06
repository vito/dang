;; Bracket pairs for automatic closing and matching
("(" @open ")" @close)
("[" @open "]" @close)
("{" @open "}" @close)
("\"" @open "\"" @close)

;; Structural bracket pairs
(block
  "{" @open
  "}" @close)

(list
  "[" @open
  "]" @close)

(object
  "{{" @open
  "}}" @close)

(paren_form
  "(" @open
  ")" @close)

(arg_values
  (immediate_paren) @open
  ")" @close)

(arg_types
  (immediate_paren) @open
  ")" @close)

(case
  "{" @open
  "}" @close)
