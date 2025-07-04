# Bind Development Workflow

## Essential Commands
- `./hack/generate` - Regenerate parser and build code (run after grammar changes)
- `./tests/run_all_tests.sh` - Full test suite (Go tests + Dagger integration)
- `go test ./tests/ -count -v -run /test_name` - Run specific test
- `go run ./cmd/bind ./path/to/file.bd` - Execute Bind file
- `go run ./cmd/bind ./tests/test_dir_module` - Special directory module test

## Development Process
1. **Add test first** - Create `.bd` file in `/tests/` with `assert { ... }` statements
2. **Modify grammar** - Update `pkg/bind/bind.peg` for syntax changes
3. **Update AST** - Modify structs in `pkg/bind/*.go` as needed
4. **Implement logic** - Update `Hoist()`, `Infer()`, and `Eval()` methods
5. **Regenerate** - Run `./hack/generate` after grammar changes
6. **Test** - Run specific test, then full suite

## Language Design Principles
- **Simplicity over complexity** - No operator precedence, use parentheses
- **Type safety** - Hindley-Milner type system prevents runtime errors
- **No invalid states** - Constructor functions ensure proper initialization
- **GraphQL integration** - Types and APIs derived from GraphQL schema

## Common Gotchas
- **Grammar changes require regeneration** - Always run `./hack/generate`
- **Hoisting vs Inference timing** - Forward references need types during `Hoist()` phase
- **Module boundaries** - Test cross-file references with directory module test
- **Type inference order** - Constructor args must be available during class method inference

## File Organization
- `pkg/bind/bind.peg` - Grammar definition
- `pkg/bind/slots.go` - Class and slot declarations
- `pkg/bind/ast.go` - Function calls and evaluation
- `pkg/bind/eval.go` - Core evaluation engine
- `tests/*.bd` - Individual test files (auto-discovered by Go tests)
- `tests/test_dir_module/` - Multi-file test (manual execution required)
