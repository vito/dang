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
  "(" @open
  ")" @close)

(arg_types
  "(" @open
  ")" @close)

(block_params
  "(" @open
  ")" @close)

(match
  "{" @open
  "}" @close)
