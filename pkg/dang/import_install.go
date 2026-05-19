package dang

import "github.com/vito/dang/pkg/hm"

const importedBindingVisibility = PrivateVisibility

func installImportedTypeEnvironment(parentEnv Env, importName string, schemaModule Env) {
	qualifiedOrigin := ImportedBindingOrigin(importName, true)

	parentEnv.AddClass(importName, schemaModule)
	parentEnv.SetTypeOrigin(importName, qualifiedOrigin)
	parentEnv.Add(importName, hm.NewScheme(nil, NonNull(schemaModule)))
	parentEnv.SetVisibility(importName, importedBindingVisibility)
	parentEnv.SetValueOrigin(importName, qualifiedOrigin)

	installUnqualifiedImportSymbols(parentEnv, schemaModule, importName)
}

func installUnqualifiedImportSymbols(parentEnv Env, schemaModule Env, importName string) {
	installUnqualifiedImportValuesForInference(parentEnv, schemaModule, importName)

	if mod, ok := schemaModule.(*CompositeModule); ok {
		if primaryMod, ok := mod.primary.(*Module); ok {
			installUnqualifiedImportTypesFromModule(parentEnv, primaryMod, importName)
			installUnqualifiedImportDirectivesFromModule(parentEnv, primaryMod, importName)
		}
		return
	}
	if mod, ok := schemaModule.(*Module); ok {
		installUnqualifiedImportTypesFromModule(parentEnv, mod, importName)
		installUnqualifiedImportDirectivesFromModule(parentEnv, mod, importName)
	}
}

func installUnqualifiedImportValuesForInference(parentEnv Env, schemaModule Env, importName string) {
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

func installUnqualifiedImportTypesFromModule(parentEnv Env, mod *Module, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for name, class := range mod.classes {
		if name == importName {
			continue
		}

		if _, exists := parentEnv.NamedType(name); exists {
			if localTypeBindingIsUnqualifiedImport(parentEnv, name) {
				addTypeImportProvider(parentEnv, name, importName)
			}
			continue
		}

		parentEnv.AddClass(name, class)
		parentEnv.SetTypeOrigin(name, origin)

		if enumMod, ok := class.(*Module); ok && enumMod.Kind == EnumKind {
			installUnqualifiedImportEnumValuesForInference(parentEnv, enumMod, importName)
		}
	}
}

func installUnqualifiedImportEnumValuesForInference(parentEnv Env, enumMod *Module, importName string) {
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

func installUnqualifiedImportDirectivesFromModule(parentEnv Env, mod *Module, importName string) {
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

func installImportedEvalEnvironment(parentEnv EvalEnv, importName string, moduleEnv EvalEnv) {
	qualifiedOrigin := ImportedBindingOrigin(importName, true)
	parentEnv.SetWithVisibility(importName, moduleEnv, importedBindingVisibility)
	setEvalValueOrigin(parentEnv, importName, qualifiedOrigin)

	installUnqualifiedImportValues(parentEnv, moduleEnv, importName)
}

func installUnqualifiedImportValues(parentEnv EvalEnv, moduleEnv EvalEnv, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for _, binding := range moduleEnv.Bindings(PublicVisibility) {
		name := binding.Key
		value := binding.Value
		if name == importName {
			continue
		}

		if _, exists := parentEnv.GetLocal(name); exists {
			if evalValueBindingIsUnqualifiedImport(parentEnv, name) {
				addEvalValueImportProvider(parentEnv, name, importName)
			}
			continue
		}

		parentEnv.SetWithVisibility(name, value, importedBindingVisibility)
		setEvalValueOrigin(parentEnv, name, origin)

		if enumModuleVal, ok := value.(*ModuleValue); ok {
			if mod, ok := enumModuleVal.Mod.(*Module); ok && mod.Kind == EnumKind {
				installUnqualifiedImportEnumValues(parentEnv, enumModuleVal, importName)
			}
		}
	}
}

func installUnqualifiedImportEnumValues(parentEnv EvalEnv, enumModuleVal *ModuleValue, importName string) {
	origin := ImportedBindingOrigin(importName, false)
	for _, enumBinding := range enumModuleVal.Bindings(PublicVisibility) {
		enumValName := enumBinding.Key
		enumVal := enumBinding.Value

		if _, exists := parentEnv.GetLocal(enumValName); exists {
			if evalValueBindingIsUnqualifiedImport(parentEnv, enumValName) {
				addEvalValueImportProvider(parentEnv, enumValName, importName)
			}
			continue
		}

		parentEnv.SetWithVisibility(enumValName, enumVal, importedBindingVisibility)
		setEvalValueOrigin(parentEnv, enumValName, origin)
	}
}

func localTypeBindingIsUnqualifiedImport(env Env, name string) bool {
	origin, found := env.LocalTypeOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localValueBindingIsUnqualifiedImport(env Env, name string) bool {
	origin, found := env.LocalValueOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func localDirectiveBindingIsUnqualifiedImport(env Env, name string) bool {
	origin, found := env.LocalDirectiveOrigin(name)
	return found && origin.IsUnqualifiedImport()
}

func addTypeImportProvider(env Env, name, importName string) {
	if origin, found := env.LocalTypeOrigin(name); found {
		env.SetTypeOrigin(name, origin.AddImportProvider(importName))
	}
}

func addValueImportProvider(env Env, name, importName string) {
	if origin, found := env.LocalValueOrigin(name); found {
		env.SetValueOrigin(name, origin.AddImportProvider(importName))
	}
}

func addDirectiveImportProvider(env Env, name, importName string) {
	if origin, found := env.LocalDirectiveOrigin(name); found {
		env.SetDirectiveOrigin(name, origin.AddImportProvider(importName))
	}
}

func evalValueBindingIsUnqualifiedImport(env EvalEnv, name string) bool {
	if modVal, ok := evalEnvModuleValue(env); ok {
		return localValueBindingIsUnqualifiedImport(modVal.Mod, name)
	}
	return false
}

func addEvalValueImportProvider(env EvalEnv, name, importName string) {
	if modVal, ok := evalEnvModuleValue(env); ok {
		addValueImportProvider(modVal.Mod, name, importName)
	}
}

func setEvalValueOrigin(env EvalEnv, name string, origin BindingOrigin) {
	if modVal, ok := evalEnvModuleValue(env); ok {
		modVal.Mod.SetValueOrigin(name, origin)
	}
}

func evalEnvModuleValue(env EvalEnv) (*ModuleValue, bool) {
	switch e := env.(type) {
	case *ModuleValue:
		return e, true
	case CompositeEnv:
		return evalEnvModuleValue(e.primary)
	default:
		return nil, false
	}
}
