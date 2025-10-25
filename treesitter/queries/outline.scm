;; Type declarations
(class
  (id) @name) @item

;; Directive declarations
(directive_decl
  (directive_name) @name) @item

;; Slot declarations (functions/methods/fields)
;; Function slots with arguments and body
(type_and_args_and_block_slot
  (id) @name) @item

;; Function slots with body (zero-arity functions)
(type_and_block_slot
  (id) @name) @item

;; Field slots with value
(type_and_value_slot
  (id) @name) @item

;; Field slots with just value (type inferred)
(value_only_slot
  (id) @name) @item

;; Field slots with just type (no value)
(type_only_slot
  (id) @name) @item
