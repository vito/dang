;; Code folding for blocks
[
  (block)
  (class_decl)
  (fun_decl)
] @fold

;; Fold conditional expressions
(conditional) @fold

;; Fold match expressions
(match) @fold

;; Fold let expressions
(let) @fold

;; Fold lambda expressions
(lambda) @fold

;; Fold lists and records
[
  (list)
  (record)
] @fold