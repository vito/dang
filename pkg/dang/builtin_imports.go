package dang

import (
	"context"

	"github.com/vito/dang/v2/pkg/hm"
)

// The builtin imports are the imports Dang provides every module without an
// import statement: its builtin namespaces, the modules holding static
// builtins (JSON, YAML, TOML, Random, UUID; see StaticModules). They are
// installed by the import installer, reached qualified only (`JSON.encode`,
// never a bare `encode`), before the module's explicit imports, so that:
//
//   - they are imports, with an import's origin, wherever a module's names
//     are listed or looked up;
//   - the module's own declarations shadow them, as they shadow the prelude;
//   - an explicit import of the same name replaces one, as it replaces an
//     earlier import of that name;
//   - an unqualified name of one of the module's explicit imports never
//     overrides them, as it never overrides any import's namespace (see
//     resolvesToImportNamespace);
//   - they stay the module's own: an import's namespace, a schema's or a
//     native module's, holds only what the import itself defines.
//
// Where they go follows where a module's explicit imports go. A single-file
// program (and a REPL) imports into its own frame, so they sit in a frame
// below it, between it and the prelude (NewPreludeTypeScope, NewValueScope).
// A directory module imports per file, into a frame looked up after the
// module's declarations, so each file's import frame gets them first
// (newFileImportsScope, installBuiltinImportValues), and the module's own
// chain goes without them (withoutBuiltinImports, newDirectoryValueScope).

// builtinImportsTypeScope returns a frame holding the builtin imports, over
// the prelude.
func builtinImportsTypeScope() TypeScope {
	imports := newFileImportsScope()
	imports.builtinImports = true
	return &OverlayTypeScope{primary: imports, lexical: preludeChain}
}

// newFileImportsScope returns a frame holding the builtin imports, for a
// directory module's file to install its explicit imports in after them.
func newFileImportsScope() *Type {
	imports := NewType("", ObjectKind)
	for _, mod := range StaticModules() {
		installQualifiedImportTypeScope(imports, mod.Named, mod)
	}
	return imports
}

// withoutBuiltinImports returns a module's type scope (a NewPreludeTypeScope)
// without the frame of builtin imports below it, for a directory module, whose
// files import them along with their explicit imports. Any other scope is
// returned as it is.
func withoutBuiltinImports(scope TypeScope) TypeScope {
	module, ok := scope.(*OverlayTypeScope)
	if !ok {
		return scope
	}
	below, ok := module.lexical.(*OverlayTypeScope)
	if !ok {
		return scope
	}
	if frame, ok := below.primary.(*Type); !ok || !frame.builtinImports {
		return scope
	}
	return &OverlayTypeScope{primary: module.primary, lexical: below.lexical}
}

// installBuiltinImportValues binds the builtin imports' namespaces in frame.
func installBuiltinImportValues(frame ValueScope) {
	for _, mod := range StaticModules() {
		installQualifiedImportValueScope(frame, mod.Named, builtinImportValue(mod))
	}
}

// builtinImport returns the builtin import named name.
func builtinImport(name string) (*Type, bool) {
	for _, mod := range StaticModules() {
		if mod.Named == name {
			return mod, true
		}
	}
	return nil, false
}

// builtinImportValue builds the namespace of a builtin import: its static
// methods, and the values of the enums nested in it.
func builtinImportValue(hostModule *Type) *Object {
	modValue := NewObject(hostModule)
	ForEachStaticMethod(hostModule, func(def BuiltinDef) {
		fnType := createFunctionTypeFromDef(def)
		builtinFn := BuiltinFunction{
			Name:         def.Name,
			FnType:       fnType,
			AllDefaulted: allParamsDefaulted(def),
			Deprecated:   def.Deprecated,
			CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
				argsWithDefaults := applyDefaults(args, def)
				return def.Impl(ctx, nil, Args{Values: argsWithDefaults})
			},
		}
		modValue.Bind(def.Name, builtinFn, PublicVisibility)
	})

	// Populate nested enum types with their values
	for name, subScope := range hostModule.NamedTypes() {
		subType, ok := subScope.(*Type)
		if !ok || subType.Kind != EnumKind {
			continue
		}
		enumModValue := NewObject(subType)
		var enumValues []Value
		for varName, scheme := range subType.Bindings(PublicVisibility) {
			// Only include actual enum members (whose type is the
			// enum itself), not accessors like values().
			t, _ := scheme.Type()
			if nn, ok := t.(hm.NonNullType); !ok || nn.Type != hm.Type(subType) {
				continue
			}
			ev := EnumValue{Val: varName, EnumType: subType}
			enumModValue.Bind(varName, ev, PublicVisibility)
			enumValues = append(enumValues, ev)
		}
		enumModValue.Bind("values", ListValue{
			Elements: enumValues,
			ElemType: NonNull(subType),
		}, PublicVisibility)
		modValue.Bind(name, enumModValue, PublicVisibility)
	}

	return modValue
}
