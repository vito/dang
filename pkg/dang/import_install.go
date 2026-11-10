package dang

import "github.com/vito/dang/v2/pkg/hm"

const importedBindingVisibility = PrivateVisibility

// installImportedTypeScope installs import importName into parentTypeScope.
// Its unqualified names skip those shadowed reports as locally declared
// (nil: none; see import_shadowing.go).
func installImportedTypeScope(parentTypeScope TypeScope, importName string, schemaModule TypeScope, shadowed func(string) bool) {
	installQualifiedImportTypeScope(parentTypeScope, importName, schemaModule)
	installUnqualifiedImportSymbols(parentTypeScope, schemaModule, importName, shadowed)
}

// installQualifiedImportTypeScope installs only import importName's
// namespace into parentTypeScope, for an import whose names are reached
// qualified only (see builtin_imports.go).
func installQualifiedImportTypeScope(parentTypeScope TypeScope, importName string, schemaModule TypeScope) {
	qualifiedOrigin := ImportedBindingOrigin(importName, true)

	parentTypeScope.AddObject(importName, schemaModule)
	parentTypeScope.SetTypeOrigin(importName, qualifiedOrigin)
	parentTypeScope.Add(importName, hm.NewScheme(nil, NonNull(schemaModule)))
	parentTypeScope.SetVisibility(importName, importedBindingVisibility)
	parentTypeScope.SetValueOrigin(importName, qualifiedOrigin)
}

func installUnqualifiedImportSymbols(parentTypeScope TypeScope, schemaModule TypeScope, importName string, shadowed func(string) bool) {
	installUnqualifiedImportValuesForInference(parentTypeScope, schemaModule, importName, shadowed)

	if mod, ok := schemaModule.(*OverlayTypeScope); ok {
		if primaryMod, ok := mod.primary.(*Type); ok {
			installUnqualifiedImportTypesFromModule(parentTypeScope, primaryMod, importName, shadowed)
			installUnqualifiedImportDirectivesFromModule(parentTypeScope, primaryMod, importName)
		}
		return
	}
	if mod, ok := schemaModule.(*Type); ok {
		installUnqualifiedImportTypesFromModule(parentTypeScope, mod, importName, shadowed)
		installUnqualifiedImportDirectivesFromModule(parentTypeScope, mod, importName)
	}
}

func isShadowed(shadowed func(string) bool, name string) bool {
	return shadowed != nil && shadowed(name)
}

func installUnqualifiedImportValuesForInference(parentTypeScope TypeScope, schemaModule TypeScope, importName string, shadowed func(string) bool) {
	origin := ImportedBindingOrigin(importName, false)
	for name, scheme := range schemaModule.Bindings(PublicVisibility) {
		if name == importName {
			continue
		}

		// An unqualified import never shadows a builtin, a declaration
		// beside it, or another import's namespace (such as a builtin
		// import's, see builtin_imports.go); it is still reachable qualified
		// (see installUnqualifiedImportValues).
		if isBuiltinValueName(name) || isShadowed(shadowed, name) || resolvesToImportNamespace(parentTypeScope, name) {
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

func installUnqualifiedImportTypesFromModule(parentTypeScope TypeScope, mod *Type, importName string, shadowed func(string) bool) {
	origin := ImportedBindingOrigin(importName, false)
	for name, object := range mod.objects {
		if name == importName || isShadowed(shadowed, name) {
			continue
		}
		// An unqualified import never shadows a builtin type; it is still
		// reachable qualified.
		if _, builtin := PreludeType(name); builtin {
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
			installUnqualifiedImportEnumValuesForInference(parentTypeScope, enumMod, importName, shadowed)
		}
	}
}

func installUnqualifiedImportEnumValuesForInference(parentTypeScope TypeScope, enumMod *Type, importName string, shadowed func(string) bool) {
	origin := ImportedBindingOrigin(importName, false)
	for enumValName, enumValScheme := range enumMod.Bindings(PublicVisibility) {
		if isShadowed(shadowed, enumValName) {
			continue
		}
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

func installImportedValueScope(parentScope ValueScope, importName string, importValueScope ValueScope, shadowed func(string) bool) {
	installQualifiedImportValueScope(parentScope, importName, importValueScope)
	installUnqualifiedImportValues(parentScope, importValueScope, importName, shadowed)
}

// installQualifiedImportValueScope binds only import importName's namespace
// in parentScope (see installQualifiedImportTypeScope).
func installQualifiedImportValueScope(parentScope ValueScope, importName string, importValueScope ValueScope) {
	// Binding origins live on the type environment and are established during
	// inference. Evaluation only populates runtime values; mutating origins here
	// can clobber local declarations and races with shared/static type modules.
	parentScope.Bind(importName, importValueScope, importedBindingVisibility)
	markImportNamespace(parentScope, importName)
}

func installUnqualifiedImportValues(parentScope ValueScope, importValueScope ValueScope, importName string, shadowed func(string) bool) {
	for _, binding := range importValueScope.Bindings(PublicVisibility) {
		name := binding.Key
		value := binding.Value
		if name == importName {
			continue
		}

		// An unqualified import never shadows a builtin, a declaration
		// beside it, or another import's namespace; it is still reachable
		// qualified.
		if _, exists := parentScope.LookupLocal(name); exists || isBuiltinValueName(name) || isShadowed(shadowed, name) || bindsImportNamespace(parentScope, name) {
			continue
		}

		parentScope.Bind(name, value, importedBindingVisibility)

		if enumModuleVal, ok := value.(*Object); ok {
			if mod, ok := enumModuleVal.Mod.(*Type); ok && mod.Kind == EnumKind {
				installUnqualifiedImportEnumValues(parentScope, enumModuleVal, shadowed)
			}
		}
	}
}

func installUnqualifiedImportEnumValues(parentScope ValueScope, enumModuleVal *Object, shadowed func(string) bool) {
	for _, enumBinding := range enumModuleVal.Bindings(PublicVisibility) {
		enumValName := enumBinding.Key
		enumVal := enumBinding.Value

		if _, exists := parentScope.LookupLocal(enumValName); exists || isShadowed(shadowed, enumValName) {
			continue
		}

		parentScope.Bind(enumValName, enumVal, importedBindingVisibility)
	}
}

// resolvesToImportNamespace reports whether name, looked up in env, is an
// import's namespace: a qualified import in env's own frame or in a frame env
// resolves through, such as a builtin import below a module's scope.
func resolvesToImportNamespace(env TypeScope, name string) bool {
	switch e := env.(type) {
	case *OverlayTypeScope:
		if _, found := e.primary.SchemeOf(name); found {
			return resolvesToImportNamespace(e.primary, name)
		}
		return resolvesToImportNamespace(e.lexical, name)
	case *Type:
		if _, found := e.LocalSchemeOf(name); !found && e.Parent != nil {
			return resolvesToImportNamespace(e.Parent, name)
		}
	}
	origin, found := env.LocalValueOrigin(name)
	return found && origin.Kind == BindingOriginImport && origin.Qualified
}

// markImportNamespace records that scope's own frame binds name to an
// import's namespace, for bindsImportNamespace: runtime frames carry no
// binding origins (see installQualifiedImportValueScope).
func markImportNamespace(scope ValueScope, name string) {
	switch s := scope.(type) {
	case *Object:
		s.mu.Lock()
		setIn(&s.importNamespaces, name, true)
		s.mu.Unlock()
	case OverlayValueScope:
		markImportNamespace(s.primary, name)
	case *ConstructorScope:
		markImportNamespace(s.locals, name)
	}
}

// bindsImportNamespace is resolvesToImportNamespace at runtime: whether name,
// looked up in scope (without forcing anything), is an import's namespace.
func bindsImportNamespace(scope ValueScope, name string) bool {
	switch s := scope.(type) {
	case *Object:
		cell := s.dynamicScope
		for o := s; o != nil; {
			o.mu.Lock()
			_, pending := o.Pending[name]
			_, hasVal := o.Values[name]
			namespace := o.importNamespaces[name]
			parent, _ := o.nextFrame(cell)
			o.mu.Unlock()
			if pending || hasVal {
				return namespace
			}
			o = parent
		}
	case OverlayValueScope:
		if s.primary.Has(name) {
			return bindsImportNamespace(s.primary, name)
		}
		return bindsImportNamespace(s.lexical, name)
	case *ConstructorScope:
		if s.locals.Has(name) {
			return bindsImportNamespace(s.locals, name)
		}
		if s.args.Has(name) {
			return false
		}
		if inst, ok := s.instance(); ok && inst.Has(name) {
			return false
		}
		return bindsImportNamespace(s.closure, name)
	}
	return false
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
