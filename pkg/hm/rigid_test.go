package hm

import "testing"

// A rigid variable may stand for a non-null type, so nothing possibly null
// is one of its values, and unification never binds it to make it nullable.
func TestRigidVariablesStayNonNullable(t *testing.T) {
	a := Rigid(TypeVariable('a'))
	b := Rigid(TypeVariable('b'))
	null := NullableTypeVariable{TypeVariable: FreshVar(0)}
	flex := FreshVar(1)

	refused := []struct {
		name       string
		have, want Type
	}{
		{"null as a", null, a},
		{"a? as a", NullableTypeVariable{TypeVariable: a}, a},
		{"a? as b?", NullableTypeVariable{TypeVariable: a}, NullableTypeVariable{TypeVariable: b}},
		{"Int as a?", simpleType("Int"), NullableTypeVariable{TypeVariable: a}},
	}
	for _, c := range refused {
		if subs, err := Assignable(c.have, c.want); err == nil {
			t.Errorf("%s: want an error, got %v", c.name, subs)
		}
	}

	accepted := []struct {
		name       string
		have, want Type
	}{
		{"a as a?", a, NullableTypeVariable{TypeVariable: a}},
		{"a as null's variable", a, null},
		{"a? as null's variable", NullableTypeVariable{TypeVariable: a}, null},
		{"a! as a", NonNullType{Type: a}, a},
		{"a as a flexible variable", a, flex},
	}
	for _, c := range accepted {
		subs, err := Assignable(c.have, c.want)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		for tv := range subs {
			if tv.IsRigid() {
				t.Errorf("%s: bound rigid %s in %v", c.name, tv, subs)
			}
		}
	}
}
