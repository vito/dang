;; Scope definitions
[
  (block)
  (fun_decl)
  (class_decl)
  (let)
  (lambda)
] @local.scope

;; Variable definitions
(slot_decl name: (identifier) @local.definition)
(fun_decl name: (identifier) @local.definition)
(class_decl name: (identifier) @local.definition)
(let name: (identifier) @local.definition)
(lambda_args (identifier) @local.definition)

;; Variable references
(symbol) @local.reference