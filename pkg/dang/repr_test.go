package dang

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepr(t *testing.T) {
	str := StringValue{Val: "hi"}
	list := ListValue{Elements: []Value{IntValue{1}, StringValue{Val: "a"}, BoolValue{true}}}
	mapv := MapValue{
		Keys:    []string{"name"},
		Entries: map[string]Value{"name": StringValue{Val: "Bob"}},
	}

	tests := []struct {
		name string
		val  Value
		want string
	}{
		{"string is quoted", str, `"hi"`},
		{"string escapes", StringValue{Val: "a\"b\n"}, `"a\"b\n"`},
		{"int unchanged", IntValue{42}, "42"},
		{"bool unchanged", BoolValue{true}, "true"},
		{"null unchanged", NullValue{}, "null"},
		{"list quotes nested strings", list, `[1, "a", true]`},
		{"empty list", ListValue{}, "[]"},
		{"map quotes keys and values", mapv, `["name": "Bob"]`},
		{"empty map", MapValue{}, "[:]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Repr(tt.val))
		})
	}
}

// Repr must not change String(): print and interpolation still emit raw text.
func TestReprLeavesStringUnchanged(t *testing.T) {
	s := StringValue{Val: "hi"}
	assert.Equal(t, "hi", s.String())
	assert.Equal(t, `"hi"`, Repr(s))
}
