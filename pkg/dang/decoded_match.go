package dang

import (
	"context"
	"math"

	"github.com/vito/dang/v2/pkg/hm"
)

// decodedShapeMatches reports whether decoded data has the shape of target,
// for choosing a `case` arm on decoded data. It looks only at the data: its
// kind (number, string, boolean, list, map or object), the structure of lists,
// maps and objects, and that an object holds every key the type requires,
// recursively, with the decode's field naming and strictness. It runs no
// scalar hooks, constructors or validators, nor any other user code: those
// run once, when the matching arm's data is materialized, and their errors
// are the case's errors.
func decodedShapeMatches(ctx context.Context, scope ValueScope, data DeferredValue, target hm.Type) bool {
	m := shapeMatcher{ctx: ctx, scope: scope, naming: data.Naming, strict: data.Strict}
	return m.matches(data.Raw, target)
}

type shapeMatcher struct {
	ctx    context.Context
	scope  ValueScope
	naming FieldNaming
	strict bool
}

func (m shapeMatcher) matches(raw any, target hm.Type) bool {
	if nn, ok := target.(hm.NonNullType); ok {
		if _, decoded := nn.Type.(DecodedType); decoded {
			// Decoded! holds any data, null included.
			return true
		}
		if raw == nil {
			return false
		}
		return m.matches(raw, nn.Type)
	}
	if raw == nil {
		return true
	}
	switch target.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable, DecodedType:
		return true
	}
	switch target {
	case StringType:
		_, ok := raw.(string)
		return ok
	case IntType:
		num, ok := raw.(DataNumber)
		if !ok {
			return false
		}
		_, err := decodedNumberToInt(num)
		return err == nil
	case FloatType:
		num, ok := raw.(DataNumber)
		if !ok {
			return false
		}
		f, err := num.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case BooleanType:
		_, ok := raw.(bool)
		return ok
	}
	if elem, ok := listElementTarget(target); ok {
		items, ok := raw.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if !m.matches(item, elem) {
				return false
			}
		}
		return true
	}
	if val, ok := mapValueTarget(target); ok {
		obj, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		for _, v := range obj {
			if !m.matches(v, val) {
				return false
			}
		}
		return true
	}
	mod, ok := target.(*Type)
	if !ok {
		return false
	}
	switch mod.Kind {
	case EnumKind:
		s, ok := raw.(string)
		return ok && enumHasValue(mod, s)
	case ScalarKind:
		// A custom scalar is a string underneath; a schema's scalar without
		// a hook holds any data. Whether a hook accepts the string is the
		// conversion's business.
		if _, ok := raw.(string); ok {
			return true
		}
		return mod.SourceSchema != nil && !mod.hasScalarHook && mod.goScalarHook == nil
	case ObjectKind, InputKind:
		obj, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		return m.objectMatches(obj, mod)
	}
	return false
}

// objectMatches checks an object against the fields a type reads from it: a
// record's fields, or a named type's constructor parameters, each present
// with a value of its shape unless it may be missing (nullable, or with a
// default).
func (m shapeMatcher) objectMatches(obj map[string]any, mod *Type) bool {
	type param struct {
		name       string
		typ        hm.Type
		hasDefault bool
		// nullDefault: null data means the default (see nullTakesDefault).
		nullDefault bool
	}
	var params []param
	if mod.Named == "" {
		for _, field := range mod.AsRecord().Fields {
			t, mono := field.Value.Type()
			if !mono {
				return false
			}
			params = append(params, param{name: field.Key, typ: t})
		}
	} else {
		callable, err := constructorFor(m.ctx, m.scope, mod)
		if err != nil {
			return false
		}
		types := parameterTypes(callable)
		if ctor, ok := callable.(*ConstructorFunction); ok {
			for _, p := range ctor.Parameters {
				t := types[p.Name.Name]
				if t == nil {
					t = p.GetInferredType()
				}
				params = append(params, param{
					name:        p.Name.Name,
					typ:         t,
					hasDefault:  p.Value != nil,
					nullDefault: nullTakesDefault(p),
				})
			}
		} else {
			for _, name := range callable.ParameterNames() {
				params = append(params, param{name: name, typ: types[name]})
			}
		}
	}

	known := map[string]bool{}
	for _, p := range params {
		// An ignored field reads no key, so it is as good as missing.
		key, ignore := fieldKey(m.naming, mod, p.name)
		var v any
		found := false
		if !ignore {
			known[key] = true
			v, found = obj[key]
		}
		if found && v == nil && p.nullDefault {
			found = false
		}
		if !found {
			if _, nonNull := p.typ.(hm.NonNullType); nonNull && !p.hasDefault {
				return false
			}
			continue
		}
		if p.typ != nil && !m.matches(v, p.typ) {
			return false
		}
	}
	if m.strict {
		// A strict decode reads no key the type doesn't: the same keys
		// strict materialization accepts (see noteUnknownKeys).
		for name, scheme := range mod.Bindings(PrivateVisibility) {
			if isCodecDataField(scheme) {
				if key, ignore := fieldKey(m.naming, mod, name); !ignore {
					known[key] = true
				}
			}
		}
		for k := range obj {
			if !known[k] {
				return false
			}
		}
	}
	return true
}
