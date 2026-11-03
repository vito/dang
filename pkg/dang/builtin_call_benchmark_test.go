package dang

import "testing"

// BenchmarkBuiltinMethodCalls evaluates list and string builtins with a
// callback per element, the shape of a module scanning lines of text.
func BenchmarkBuiltinMethodCalls(b *testing.B) {
	ctx, scope, node := prepareScopeChainProgram(b, `
lines: [String!]! = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map { "pkg/a  dep  go" }

lines.map { l => l.split(" ").filter { f => f.trimSpace != "" } }.filter { fs => fs.contains("dep") }.length
`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := EvalNode(ctx, scope, node); err != nil {
			b.Fatal(err)
		}
	}
}
