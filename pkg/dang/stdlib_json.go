package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// JSONModule hosts the JSON encode/decode static methods.
//
// JSON is installed two ways that share these definitions:
//
//   - As a default value namespace in Prelude (see env.go init), so
//     `JSON.encode` / `JSON.decode` resolve in any script.
//   - Grafted onto an in-scope `JSON` *scalar* — user-declared or imported from
//     a schema such as Dagger's — by the format-codec merge below, so the
//     scalar doubles as the codec namespace: `:: JSON` resolves the scalar
//     type while `JSON.encode` resolves the grafted method, instead of the
//     scalar shadowing the namespace. See issue #105.
var JSONModule = NewType("JSON", ObjectKind)

// formatCodecs maps a scalar type name to the module whose static methods are
// grafted onto a same-named scalar in scope. Adding a format here (YAML, TOML,
// ...) makes its codec merge with an imported or declared scalar of that name.
var formatCodecs = map[string]*Type{}

func registerJSON() {
	JSONModule.SetTypeDocString("functions for encoding and decoding JSON")

	// JSON.encode(value: a) -> String!
	//
	// Encode takes an arbitrary value, so it cannot live on the String type
	// (there is no universal receiver); it belongs to the JSON namespace.
	StaticMethod(JSONModule, "encode").
		Doc("serializes a value to a JSON string").
		Example(`JSON.encode([1, 2, 3])`).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("JSON.encode: %w", err)
			}
			return ToValue(string(jsonBytes))
		})

	// JSON.decode(data: String!) -> a
	//
	// Returns an opaque value that is materialized against the expected type
	// supplied via a `:: T` hint at the call boundary.
	StaticMethod(JSONModule, "decode").
		Doc("parses a JSON string into an opaque value that is materialized by an expected type").
		Example(`JSON.decode("[1, 2, 3]") :: [Int!]!`).
		Params("data", NonNull(StringType)).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			data := args.GetString("data")
			decoder := json.NewDecoder(strings.NewReader(data))
			decoder.UseNumber()

			var raw any
			if err := decoder.Decode(&raw); err != nil {
				return nil, fmt.Errorf("JSON.decode: invalid JSON: %w", err)
			}

			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				if err != nil {
					return nil, fmt.Errorf("JSON.decode: invalid JSON: %w", err)
				}
				return nil, fmt.Errorf("JSON.decode: invalid JSON: trailing data")
			}

			return DeferredValue{Raw: raw}, nil
		})

	formatCodecs["JSON"] = JSONModule
}

// graftCodecSchemes adds a registered codec's static-method type schemes onto a
// scalar type of the same name, so `Name.encode` / `Name.decode` type-check
// when `Name` resolves to the scalar. No-op for non-scalars and unregistered
// names; idempotent.
func graftCodecSchemes(scalarType *Type) {
	if scalarType == nil || scalarType.Kind != ScalarKind {
		return
	}
	host, ok := formatCodecs[scalarType.Named]
	if !ok {
		return
	}
	ForEachStaticMethod(host, func(def BuiltinDef) {
		if _, exists := scalarType.LocalSchemeOf(def.Name); exists {
			return
		}
		scalarType.Add(def.Name, hm.NewScheme(nil, createFunctionTypeFromDef(def)))
		scalarType.SetVisibility(def.Name, PublicVisibility)
		if def.Doc != "" {
			scalarType.SetDocString(def.Name, def.Doc)
		}
	})
}

// graftCodecMethods binds a registered codec's static-method implementations
// onto a scalar's runtime object, so `Name.encode` / `Name.decode` resolve at
// eval. No-op for non-scalars and unregistered names.
func graftCodecMethods(obj *Object, scalarType TypeScope) {
	mod, ok := scalarType.(*Type)
	if !ok || mod.Kind != ScalarKind {
		return
	}
	host, found := formatCodecs[mod.Named]
	if !found {
		return
	}
	ForEachStaticMethod(host, func(def BuiltinDef) {
		obj.Bind(def.Name, BuiltinFunction{
			Name:         def.Name,
			FnType:       createFunctionTypeFromDef(def),
			AllDefaulted: allParamsDefaulted(def),
			CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
				return def.Impl(ctx, nil, Args{Values: applyDefaults(args, def)})
			},
		}, PublicVisibility)
	})
}
