package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// TestEmptyCollectionsCarryTheirInferredType checks that an empty list or map
// holds, at runtime, the element type inference settled on for it, wherever
// it was settled: around the literal (`xs ?? []`, an if branch, a reduce
// accumulator) or, in a generic body, by the call's type arguments.
func TestEmptyCollectionsCarryTheirInferredType(t *testing.T) {
	typeScope := NewPreludeTypeScope("runtime-types")
	valueScope := NewValueScope(typeScope)
	src := `
let floats: [Float!] = null
let viaDefault = floats ?? []
let flag = true
let viaBranch = if (flag) { [] } else { [1.5] }
let viaReduce = [1.5, 2.5].reduce([]) { acc, x => if (x > 9.0) { acc + [x] } else { acc } }
let someFloats: [Float!]! = [1.0]
let viaConcat = [] + someFloats.filter { x => x > 9.0 }

doubled[a: Numeric](xs: [a!]!): [a!]! { xs.map { x => x * 2 } }
emptyLike[a](x: a!): [a!]! { [] }
collect[a](x: a!): [a!]! {
  let out = []
  if (false) { out += [x] }
  out
}

let noFloats: [Float!]! = []
let viaGenericMap = doubled(noFloats)
let viaGenericLiteral = emptyLike("s")
let viaGenericLocal = collect(true)

firstOf[a](x: [a!]!, y: [a!]!): [a!]! { x }
let viaGenericArg = firstOf([], [1.5])

# A method made during a generic call doesn't see that call's variables.
type Box {
  empties(x: a!): [a!]! { [] }
}
mkBox(x: a!): Box! { Box }
let viaMethod = mkBox(1.5).empties(1)

let names: Map[String!] = null
let viaMapDefault = names ?? [:]
`
	parsed, err := ParseWithRecovery("runtime-types", []byte(src))
	require.NoError(t, err)
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	ctx := withTopLevelFrame(context.Background(), typeScope)
	_, err = InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err)
	_, err = EvaluateFormsWithPhases(context.Background(), file.Forms, valueScope)
	require.NoError(t, err)

	floatT := hm.NonNullType{Type: FloatType}
	for name, want := range map[string]hm.Type{
		"viaDefault":        floatT,
		"viaBranch":         floatT,
		"viaReduce":         floatT,
		"viaConcat":         floatT,
		"viaGenericMap":     floatT,
		"viaGenericLiteral": hm.NonNullType{Type: StringType},
		"viaGenericLocal":   hm.NonNullType{Type: BooleanType},
		"viaGenericArg":     floatT,
		"viaMethod":         hm.NonNullType{Type: IntType},
	} {
		val, found, err := valueScope.Lookup(context.Background(), name)
		require.NoError(t, err, name)
		require.True(t, found, name)
		list, ok := val.(ListValue)
		require.True(t, ok, "%s: got %T", name, val)
		require.True(t, list.Elements == nil || len(list.Elements) == 0, "%s: not empty", name)
		require.NotNil(t, list.ElemType, name)
		require.True(t, list.ElemType.Eq(want), "%s: runtime element type %s, want %s", name, list.ElemType, want)
	}

	val, found, err := valueScope.Lookup(context.Background(), "viaMapDefault")
	require.NoError(t, err)
	require.True(t, found)
	m, ok := val.(MapValue)
	require.True(t, ok, "got %T", val)
	require.True(t, m.ValType.Eq(hm.NonNullType{Type: StringType}), "runtime value type %s", m.ValType)
}
