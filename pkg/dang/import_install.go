package dang

import "github.com/vito/dang/pkg/hm"

const importedBindingVisibility = PrivateVisibility

func installImportedTypeEnvironment(parentEnv TypeScope, importName string, schemaModule TypeScope) {
	qualifiedOrigin := ImportedBindingOrigin(importName, true)

	parentEnv.AddObject(importName, schemaModule)
	parentEnv.SetTypeOrigin(importName, qualifiedOrigin)
	parentEnv.Add(importName, hm.NewScheme(nil, NonNull(schemaModule)))
	parentEnv.SetVisibility(importName, importedBindingVisibility)
	parentEnv.SetValueOrigin(importName, qualifiedOrigin)

	installUnqualifiedImportSymbols(parentEnv, schemaModule, importName)
}

func installUnqualifiedImportSymbols(parentEnv TypeScope, schemaModule TypeScope, importName string) {
	installUnqualifiedImportValuesForInference(parentEnv, schemaModule, importName)

	if mod, ok := schemaModule.(*OverlayTypeScope); ok {
		if primaryMod, ok := mod.primary.(*TypeDef); ok {
			installUnqualifiedImportTypesFromModule(parentEnv, primaryMod, importName)
			installUnqualifiedImportDirectivesFromModule(parentEnv, primaryMod, importName)
		}
		return
	}
	if mod, ok := schemaModule.(*TypeDef); ok {
		installUnqualifiedImportTypesFromModule(parentEnv, mod, importName)
		installUnqualifiedImportDirectivesFromModule(parentEnv, mod, importName)
	}
}

func installUnqualifiedImportValuesForInference(parentEnv TypeScope, schemaModule TypeScope, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for name, scheme := range schemaModule.Bindings(PublicVisibility) {
		if name == importName {
			continue
		}

		if _, exists := parentEnv.LocalSchemeOf(name); exists {
			if localValueBindingIsUnqualifiedImport(parentEnv, name) {
				addValueImportProvider(parentEnv, name, importName)
			}
			continue
		}

		parentEnv.Add(name, scheme)
		parentEnv.SetVisibility(name, importedBindingVisibility)
		parentEnv.SetValueOrigin(name, origin)
	}
}

func installUnqualifiedImportTypesFromModule(parentEnv TypeScope, mod *TypeDef, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for name, object := range mod.objects {
		if name == importName {
			continue
		}

		if _, exists := parentEnv.NamedType(name); exists {
			if localTypeBindingIsUnqualifiedImport(parentEnv, name) {
				addTypeImportProvider(parentEnv, name, importName)
			}
			continue
		}

		parentEnv.AddObject(name, object)
		parentEnv.SetTypeOrigin(name, origin)

		if enumMod, ok := object.(*TypeDef); ok && enumMod.Kind == EnumKind {
			installUnqualifiedImportEnumValuesForInference(parentEnv, enumMod, importName)
		}
	}
}

func installUnqualifiedImportEnumValuesForInference(parentEnv TypeScope, enumMod *TypeDef, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for enumValName, enumValScheme := range enumMod.Bindings(PublicVisibility) {
		if _, exists := parentEnv.LocalSchemeOf(enumValName); exists {
			if localValueBindingIsUnqualifiedImport(parentEnv, enumValName) {
				addValueImportProvider(parentEnv, enumValName, importName)
			}
			continue
		}

		parentEnv.Add(enumValName, enumValScheme)
		parentEnv.SetVisibility(enumValName, importedBindingVisibility)
		parentEnv.SetValueOrigin(enumValName, origin)
	}
}

func installUnqualifiedImportDirectivesFromModule(parentEnv TypeScope, mod *TypeDef, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for directiveName, directive := range mod.directives {
		if _, exists := parentEnv.GetDirective(directiveName); exists {
			if localDirectiveBindingIsUnqualifiedImport(parentEnv, directiveName) {
				addDirectiveImportProvider(parentEnv, directiveName, importName)
			}
			continue
		}

		parentEnv.AddDirective(directiveName, directive)
		parentEnv.SetDirectiveOrigin(directiveName, origin)
	}
}

func installImportedEvalEnvironment(parentEnv ValueScope, importName string, moduleEnv ValueScope) {
	// Binding origins live on the type environment and are established during
	// inference. Evaluation only populates runtime values; mutating origins here
	// can clobber local declarations and races with shared/static type modules.
	parentEnv.Bind(importName, moduleEnv, importedBindingVisibility)

	installUnqualifiedImportValues(parentEnv, moduleEnv, importName)
}

func installUnqualifiedImportValues(parentEnv ValueScope, moduleEnv ValueScope, importName string) {
	for _, binding := range moduleEnv.Bindings(PublicVisibility) {
		name := binding.Key
		value := binding.Value
		if name == importName {
			continue
		}

		if _, exists := parentEnv.LookupLocal(name); exists {
			continue
		}

		parentEnv.Bind(name, value, importedBindingVisibility)

		if enumModuleVal, ok := value.(*Object); ok {
			if mod, ok := enumModuleVal.Mod.(*TypeDef); ok && mod.Kind == EnumKind {
				installUnqualifiedImportEnumValues(parentEnv, enumModuleVal)
			}
		}
	}
}

func installUnqualifiedImportEnumValues(parentEnv ValueScope, enumModuleVal *Object) {
	for _, enumBinding := range enumModuleVal.Bindings(PublicVisibility) {
		enumValName := enumBinding.Key
		enumVal := enumBinding.Value

		if _, exists := parentEnv.LookupLocal(enumValName); exists {
			continue
		}

		parentEnv.Bind(enumValName, enumVal, importedBindingVisibility)
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
