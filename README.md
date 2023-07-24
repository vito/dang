# dash

an experimental scripting language for Dagger

## philosophy

- keep it simple, don't pay 1000 homages to 1000 languages
  - don't be afraid to be different
  - don't be afraid to be the same
- take the good parts of Bash
  - it's the "language" tackling the most similar domain
    - almost every language sucks for quickly scripting commands
  - examples:
    - use # for comments
    - write commands $ like this; ["not", "like", "this"]
- favor ergonomics over purity
  - use keywords (e.g. def) to make it easier to walk through the file using tags/etc.
  - make things look different if they are interacted with differently
  - Bass went too far in the "everything is an X" direction
- don't circlejerk language shit
  - Kernel is great but this probably doesn't need operatives
- types, with inference (hindley milner?)
  - no optional typing. don't want fragmentation. it shouldn't be that
    complicated anyway.
- vertically integrated (good marketing speak for "tightly coupled")
- need to be able to work on this as a 20% project
  - example: want tree-sitter, but don't want to maintain two grammars.
  - solution: codegen AST from tree-sitter, use it for actual parsing too.
