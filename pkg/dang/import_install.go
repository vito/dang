package dang

import (
	"maps"
	"slices"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
)

const importedBindingVisibility = PrivateVisibility

func installImportedTypeScope(parentTypeScope TypeScope, importName string, schemaModule TypeScope) {
	qualifiedOrigin := ImportedBindingOrigin(importName, true)

	parentTypeScope.AddObject(importName, schemaModule)
	parentTypeScope.SetTypeOrigin(importName, qualifiedOrigin)
	parentTypeScope.Add(importName, hm.NewScheme(nil, NonNull(schemaModule)))
	parentTypeScope.SetVisibility(importName, importedBindingVisibility)
	parentTypeScope.SetValueOrigin(importName, qualifiedOrigin)

	installUnqualifiedImportSymbols(parentTypeScope, schemaModule, importName)
}

func installUnqualifiedImportSymbols(parentTypeScope TypeScope, schemaModule TypeScope, importName string) {
	installUnqualifiedImportValuesForInference(parentTypeScope, schemaModule, importName)

	if mod, ok := schemaModule.(*OverlayTypeScope); ok {
		if primaryMod, ok := mod.primary.(*Type); ok {
			installUnqualifiedImportTypesFromModule(parentTypeScope, primaryMod, importName)
			installUnqualifiedImportDirectivesFromModule(parentTypeScope, primaryMod, importName)
		}
		return
	}
	if mod, ok := schemaModule.(*Type); ok {
		installUnqualifiedImportTypesFromModule(parentTypeScope, mod, importName)
		installUnqualifiedImportDirectivesFromModule(parentTypeScope, mod, importName)
	}
}

func installUnqualifiedImportValuesForInference(parentTypeScope TypeScope, schemaModule TypeScope, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for name, scheme := range schemaModule.Bindings(PublicVisibility) {
		if name == importName {
			continue
		}

		if _, exists := parentTypeScope.LocalSchemeOf(name); exists {
			if localValueBindingIsUnqualifiedImport(parentTypeScope, name) {
				addValueImportProvider(parentTypeScope, name, importName)
			}
			continue
		}

		parentTypeScope.Add(name, scheme)
		parentTypeScope.SetVisibility(name, importedBindingVisibility)
		parentTypeScope.SetValueOrigin(name, origin)
	}
}

func installUnqualifiedImportTypesFromModule(parentTypeScope TypeScope, mod *Type, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for name, object := range mod.objects {
		if name == importName {
			continue
		}

		if _, exists := parentTypeScope.NamedType(name); exists {
			if localTypeBindingIsUnqualifiedImport(parentTypeScope, name) {
				addTypeImportProvider(parentTypeScope, name, importName)
			}
			continue
		}

		parentTypeScope.AddObject(name, object)
		parentTypeScope.SetTypeOrigin(name, origin)

		if enumMod, ok := object.(*Type); ok && enumMod.Kind == EnumKind {
			installUnqualifiedImportEnumValuesForInference(parentTypeScope, enumMod, importName)
		}
	}
}

func installUnqualifiedImportEnumValuesForInference(parentTypeScope TypeScope, enumMod *Type, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for enumValName, enumValScheme := range enumMod.Bindings(PublicVisibility) {
		if _, exists := parentTypeScope.LocalSchemeOf(enumValName); exists {
			if localValueBindingIsUnqualifiedImport(parentTypeScope, enumValName) {
				addValueImportProvider(parentTypeScope, enumValName, importName)
			}
			continue
		}

		parentTypeScope.Add(enumValName, enumValScheme)
		parentTypeScope.SetVisibility(enumValName, importedBindingVisibility)
		parentTypeScope.SetValueOrigin(enumValName, origin)
	}
}

func installUnqualifiedImportDirectivesFromModule(parentTypeScope TypeScope, mod *Type, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for directiveName, directive := range mod.directives {
		if _, exists := parentTypeScope.GetDirective(directiveName); exists {
			if localDirectiveBindingIsUnqualifiedImport(parentTypeScope, directiveName) {
				addDirectiveImportProvider(parentTypeScope, directiveName, importName)
			}
			continue
		}

		parentTypeScope.AddDirective(directiveName, directive)
		parentTypeScope.SetDirectiveOrigin(directiveName, origin)
	}
}

func installImportedValueScope(parentScope ValueScope, importName string, importValueScope ValueScope) {
	// Binding origins live on the type environment and are established during
	// inference. Evaluation only populates runtime values; mutating origins here
	// can clobber local declarations and races with shared/static type modules.
	parentScope.Bind(importName, importValueScope, importedBindingVisibility)

	installUnqualifiedImportValues(parentScope, importValueScope, importName)
}

func installUnqualifiedImportValues(parentScope ValueScope, importValueScope ValueScope, importName string) {
	for _, binding := range importValueScope.Bindings(PublicVisibility) {
		name := binding.Key
		value := binding.Value
		if name == importName {
			continue
		}

		if _, exists := parentScope.LookupLocal(name); exists {
			continue
		}

		parentScope.Bind(name, value, importedBindingVisibility)

		if enumModuleVal, ok := value.(*Object); ok {
			if mod, ok := enumModuleVal.Mod.(*Type); ok && mod.Kind == EnumKind {
				installUnqualifiedImportEnumValues(parentScope, enumModuleVal)
			}
		}
	}
}

func installUnqualifiedImportEnumValues(parentScope ValueScope, enumModuleVal *Object) {
	for _, enumBinding := range enumModuleVal.Bindings(PublicVisibility) {
		enumValName := enumBinding.Key
		enumVal := enumBinding.Value

		if _, exists := parentScope.LookupLocal(enumValName); exists {
			continue
		}

		parentScope.Bind(enumValName, enumVal, importedBindingVisibility)
	}
}

func localTypeBindingIsUnqualifiedImport(env TypeScope, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localValueBindingIsUnqualifiedImport(env TypeScope, name string) bool {
	origin, found := env.LocalValueOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localDirectiveBindingIsUnqualifiedImport(env TypeScope, name string) bool {
	origin, found := env.LocalDirectiveOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func addTypeImportProvider(env TypeScope, name, importName string) {
	if origin, found := env.LocalTypeOrigin(name); found {
		env.SetTypeOrigin(name, origin.AddImportProvider(importName))
	}
}

func addValueImportProvider(env TypeScope, name, importName string) {
	if origin, found := env.LocalValueOrigin(name); found {
		env.SetValueOrigin(name, origin.AddImportProvider(importName))
	}
}

func addDirectiveImportProvider(env TypeScope, name, importName string) {
	if origin, found := env.LocalDirectiveOrigin(name); found {
		env.SetDirectiveOrigin(name, origin.AddImportProvider(importName))
	}
}

// Alias original schema bindings without changing their identity or wire names.
// Return the accepted value aliases so evaluation uses the same decisions.
func aliasImportTypes(scope TypeScope, schema *introspection.Schema, aliases map[string]string) map[string]string {
	if len(aliases) == 0 {
		return nil
	}
	// Snapshot the original bindings: aliases cannot target other aliases, and
	// collisions must not depend on map iteration order. Include builtin scalars.
	types := make(map[string]TypeScope, len(schema.Types))
	values := make(map[string]*hm.Scheme)
	if query, ok := scope.LocalNamedType(schema.QueryType.Name); ok {
		for name, scheme := range query.Bindings(PublicVisibility) {
			values[name] = scheme
		}
	}
	for _, typ := range schema.Types {
		types[typ.Name], _ = scope.NamedType(typ.Name)
		if scheme, ok := scope.LocalSchemeOf(typ.Name); ok {
			values[typ.Name] = scheme
		}
	}
	builtins := importBuiltinValueNames()
	accepted := make(map[string]string)
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		target := aliases[alias]
		if typ := types[target]; typ != nil {
			if _, exists := types[alias]; !exists {
				scope.AddObject(alias, typ)
			}
		}
		if scheme := values[target]; scheme != nil && values[alias] == nil && !builtins[alias] {
			scope.Add(alias, scheme)
			scope.SetVisibility(alias, PublicVisibility)
			accepted[alias] = target
		}
	}
	return accepted
}

func aliasImportValues(scope ValueScope, aliases map[string]string) {
	// These aliases were validated against the original schema during inference.
	for alias, target := range aliases {
		if value, ok := scope.LookupLocal(target); ok {
			scope.Bind(alias, value, PublicVisibility)
		}
	}
}

// Preserve the values installed by addBuiltinFunctions and the public prelude. Private source helpers aren't imported values
// and must not prevent an alias from being installed.
func importBuiltinValueNames() map[string]bool {
	names := map[string]bool{"Regexp": true}
	ForEachFunction(func(def BuiltinDef) { names[def.Name] = true })
	for _, receiver := range MethodReceivers() {
		ForEachMethod(receiver, func(def BuiltinDef) {
			names[GetMethodKey(receiver, def.Name)] = true
		})
	}
	for _, module := range StaticModules() {
		names[module.Named] = true
	}
	loadPrelude()
	for _, binding := range preludeBindings {
		names[binding.Key] = true
	}
	return names
}
