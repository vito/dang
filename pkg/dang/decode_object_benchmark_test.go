package dang

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// decodeBenchTypes is shaped like the objects a Dagger module receives from a
// collection: a list of selections, each holding a nested collection that holds
// a large module object, which holds a tool object. Every one of them is
// rebuilt from stored data and has its class body evaluated, as the Dagger SDK
// does when it decodes a function's receiver.
func decodeBenchTypes() string {
	var b strings.Builder
	b.WriteString("type Tool {\n  version: String\n  warnings: [String!]! = []\n  patterns: [String!]! = [\"**\"]\n  mountPath: String\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "  let helper%d(x: String!): String! { x + \"%d\" }\n", i, i)
	}
	b.WriteString("}\n\ntype Module {\n  let tool: Tool!\n  path: String!\n  let warnings: [String!]!\n  mountPath: String!\n")
	b.WriteString("  let lib: Tool! { Tool(version: path) }\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "  let m%d(ws: String!, n: Int! = 1): String! { tool.helper%d(path + ws) }\n", i, i%30)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "  p%d(ws: String!): [String!]! { [path, ws, mountPath] }\n", i)
	}
	b.WriteString("}\n\ntype Tests {\n  names: [String!]!\n  narrowed: Boolean! { names.length > 0 }\n  let directory: String!\n  let module: Module!\n")
	b.WriteString("  function(name: String!): String! { module.m0(directory + name) }\n}\n\n")
	b.WriteString("type Selection {\n  directory: String!\n  tests: Tests!\n}\n")
	return b.String()
}

type decodeBench struct {
	ctx   context.Context
	env   ValueScope
	types map[string]TypeScope
	data  []map[string]any
}

func newDecodeBench(tb testing.TB, entries int) *decodeBench {
	tb.Helper()
	src := decodeBenchTypes()
	parsed, err := ParseWithRecovery("decode_bench", []byte(src))
	require.NoError(tb, err, "parse")
	file := parsed.(*FileBlock)

	ctx := context.Background()
	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferTopLevel(ctx, file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(tb, err, "infer")
	_, err = evaluateForms(WithEvalContext(ctx, NewEvalContext("decode_bench", src)), file.Forms, valueScope, evalModule)
	require.NoError(tb, err, "eval")

	// The SDK decodes under the engine's request context, which carries no
	// Dang frame and many value layers of its own.
	type engineKey int
	for i := range 30 {
		ctx = context.WithValue(ctx, engineKey(i), i)
	}

	types := map[string]TypeScope{}
	for _, name := range []string{"Tool", "Module", "Tests", "Selection"} {
		t, found := typeScope.NamedType(name)
		require.True(tb, found, name)
		types[name] = t
	}

	module := map[string]any{
		"tool":      map[string]any{"version": "1.27", "warnings": []any{}, "patterns": []any{"**"}, "mountPath": "/src"},
		"path":      ".",
		"warnings":  []any{},
		"mountPath": "/src",
	}
	data := make([]map[string]any, entries)
	for i := range data {
		dir := fmt.Sprintf("pkg/p%d", i)
		data[i] = map[string]any{
			"directory": dir,
			"tests": map[string]any{
				"names":     []any{"TestA", "TestB", "TestC"},
				"directory": dir,
				"module":    module,
			},
		}
	}
	return &decodeBench{ctx: ctx, env: valueScope, types: types, data: data}
}

// decode converts one stored object the way the Dagger SDK's convertObject
// does: bind the stored fields, then evaluate the type's class body.
func (d *decodeBench) decode(tb testing.TB, typeName string, vals map[string]any) *Object {
	mod := d.types[typeName]
	obj := NewObject(mod)
	for name, val := range vals {
		var v Value
		switch x := val.(type) {
		case string:
			v = StringValue{Val: x}
		case []any:
			list := ListValue{ElemType: hm.NonNullType{Type: StringType}}
			for _, e := range x {
				list.Elements = append(list.Elements, StringValue{Val: e.(string)})
			}
			v = list
		case map[string]any:
			nested := map[string]string{"tool": "Tool", "module": "Module", "tests": "Tests"}[name]
			v = d.decode(tb, nested, x)
		}
		obj.Bind(name, v, PrivateVisibility)
	}

	ctor, found, err := d.env.Lookup(d.ctx, typeName)
	require.NoError(tb, err)
	require.True(tb, found)
	bodyEnv := CreateOverlayValueScope(obj, d.env)
	bodyEnv.EnterSelf(obj)
	_, err = EvaluateFormsWithPhases(d.ctx, ctor.(*ConstructorFunction).ObjectBodyForms, bodyEnv)
	require.NoError(tb, err)
	return obj
}

func BenchmarkDecodeNestedObjects(b *testing.B) {
	d := newDecodeBench(b, 608)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, entry := range d.data {
			d.decode(b, "Selection", entry)
		}
	}
}
