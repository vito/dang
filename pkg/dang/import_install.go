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

// Install aliases to the existing symbols, rather than copying or renaming their
// definitions: GraphQL functions must still query the original field and type.
func aliasImportTypes(scope TypeScope, schema *introspection.Schema, aliases map[string]string) {
	// Resolve every target before installing any aliases. An alias must name
	// an original schema symbol, never an alias installed earlier in this pass.
	var types []Keyed[TypeScope]
	var values []Keyed[*hm.Scheme]
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		target := aliases[alias]
		if typ, ok := scope.LocalNamedType(target); ok {
			if _, exists := scope.LocalNamedType(alias); !exists {
				types = append(types, Keyed[TypeScope]{Key: alias, Value: typ})
			}
		}
		scheme, ok := scope.LocalSchemeOf(target)
		if !ok {
			if query, found := scope.LocalNamedType(schema.QueryType.Name); found {
				scheme, ok = query.LocalSchemeOf(target)
			}
		}
		if ok {
			// Runtime schema scopes also contain the builtins. Include the
			// lexical fallback here so both phases preserve those bindings.
			_, exists := scope.SchemeOf(alias)
			if !exists {
				values = append(values, Keyed[*hm.Scheme]{Key: alias, Value: scheme})
			}
		}
	}
	for _, alias := range types {
		scope.AddObject(alias.Key, alias.Value)
	}
	for _, alias := range values {
		scope.Add(alias.Key, alias.Value)
		scope.SetVisibility(alias.Key, PublicVisibility)
	}
}

func aliasImportValues(scope ValueScope, schema *introspection.Schema, aliases map[string]string) {
	if len(aliases) == 0 {
		return
	}
	// Runtime scopes also contain builtins, which aren't schema targets.
	targets := make(map[string]bool)
	for _, typ := range schema.Types {
		targets[typ.Name] = true
		if typ.Name == schema.QueryType.Name {
			for _, field := range typ.Fields {
				targets[field.Name] = true
			}
		}
	}
	var values []Keyed[Value]
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		target := aliases[alias]
		if !targets[target] {
			continue
		}
		if val, ok := scope.LookupLocal(target); ok {
			if _, exists := scope.LookupLocal(alias); !exists {
				values = append(values, Keyed[Value]{Key: alias, Value: val})
			}
		}
	}
	for _, alias := range values {
		scope.Bind(alias.Key, alias.Value, PublicVisibility)
	}
}
