package dang

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Migrating source from before Dang 2.4 to 2.4 or later changes what a bare
// type named like a builtin import means wherever an import brings in a type
// of that name unqualified (see importTypesOverBuiltinImports): `JSON` was
// Dagger's `scalar JSON`, and becomes the builtin JSON namespace. No spelling
// differs, so the parser can't migrate it; what the imports bring in decides.
// Given that, the migration qualifies such types by the import's name, so
// `receiverValue: JSON` becomes `receiverValue: Dagger.JSON` and keeps its
// meaning (see qualifyImportTypes).

// ImportedTypes is what an import brings into the source that imports it,
// as far as migrating it needs to know (see BuiltinImportTypes).
type ImportedTypes struct {
	// Auto reports whether every file gets the import without importing it
	// (ImportConfig.AutoImport), as Dagger modules get Dagger's.
	Auto bool
	// Types lists the types it brings in unqualified that are named like a
	// builtin import (e.g. JSON), sorted.
	Types []string
}

// BuiltinImportTypes returns, by import name, the types each of configs
// brings in unqualified that are named like a builtin import, for migrating
// the source that imports them (see MigrateOptions.ImportedTypes). An import
// whose schema is not loaded yet is introspected, and a native Dang module is
// compiled.
func BuiltinImportTypes(ctx context.Context, configs []ImportConfig) (map[string]ImportedTypes, error) {
	ctx = ContextWithImportConfigs(ctx, configs...)
	result := map[string]ImportedTypes{}
	for _, config := range configs {
		var mod TypeScope
		switch {
		case config.DangModuleDir != "":
			m, err := loadDangModule(ctx, config.Name, config.DangModuleDir)
			if err != nil {
				return nil, fmt.Errorf("import %s: %w", config.Name, err)
			}
			mod = m.exportsType
		default:
			schema := config.Schema
			if schema == nil {
				if config.Client == nil {
					continue
				}
				var err error
				schema, err = introspectSchema(ctx, config.Client, config.Dagger)
				if err != nil {
					return nil, fmt.Errorf("import %s: %w", config.Name, err)
				}
			}
			mod = TypeScopeFromSchema(config.Name, schema)
		}
		result[config.Name] = ImportedTypes{
			Auto:  config.AutoImport,
			Types: unqualifiedBuiltinImportTypes(mod, config.Name),
		}
	}
	return result, nil
}

// unqualifiedBuiltinImportTypes returns the types import importName, whose
// namespace is mod, brings in unqualified that are named like a builtin
// import, as installUnqualifiedImportTypesFromModule installs them.
func unqualifiedBuiltinImportTypes(mod TypeScope, importName string) []string {
	var declared *Type
	switch m := mod.(type) {
	case *OverlayTypeScope:
		declared, _ = m.primary.(*Type)
	case *Type:
		declared = m
	}
	if declared == nil {
		return nil
	}
	var names []string
	for name := range declared.objects {
		if name == importName {
			continue
		}
		if _, builtin := PreludeType(name); builtin {
			continue
		}
		if _, ok := builtinImport(name); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// importTypeQualifiers returns, for source parsed as file, which bare types
// to qualify when migrating it past importTypesOverBuiltinImports: by the
// builtin import name each type is spelled as, the import to qualify it with.
// imports is what the project's imports bring in (see BuiltinImportTypes),
// and declared reports the names the file's module declares at its top
// level, which name its own declarations rather than an import's type. A
// type the file spells bare that two imports both bring in is ambiguous, and
// an error.
func importTypeQualifiers(file *FileBlock, imports map[string]ImportedTypes, declared func(string) bool) (map[string]string, error) {
	bare := bareBuiltinImportTypes(file)
	if len(imports) == 0 || len(bare) == 0 {
		return nil, nil
	}
	// The imports in effect: every auto-import, and those the file names.
	inEffect := map[string]bool{}
	for name, imp := range imports {
		if imp.Auto {
			inEffect[name] = true
		}
	}
	file.Walk(func(n Node) bool {
		if imp, ok := n.(*ImportDecl); ok && imp.Name != nil {
			inEffect[imp.Name.Name] = true
		}
		return true
	})

	providers := map[string][]string{}
	for name := range inEffect {
		for _, t := range imports[name].Types {
			// An import of that name replaces the builtin import, in every
			// version, and so does a declaration of it.
			if !bare[t] || inEffect[t] || (declared != nil && declared(t)) {
				continue
			}
			providers[t] = append(providers[t], name)
		}
	}
	qualifiers := map[string]string{}
	for t, names := range providers {
		if len(names) > 1 {
			slices.Sort(names)
			return nil, fmt.Errorf("type %s is brought in by more than one import (%s), so it can't be qualified; qualify it by hand", t, strings.Join(names, ", "))
		}
		qualifiers[t] = names[0]
	}
	return qualifiers, nil
}

// qualifyImportTypes qualifies the bare types in file named like a key of
// qualifiers by the import it maps to, and reports whether it changed any.
func qualifyImportTypes(file *FileBlock, qualifiers map[string]string) bool {
	changed := false
	walkBareTypes(file, func(name string) *NamedTypeNode {
		imp, ok := qualifiers[name]
		if !ok {
			return nil
		}
		changed = true
		return &NamedTypeNode{Name: imp}
	})
	return changed
}

// bareBuiltinImportTypes returns the names file spells a type by bare that
// are named like a builtin import, which migrating it past
// importTypesOverBuiltinImports may need to qualify.
func bareBuiltinImportTypes(file *FileBlock) map[string]bool {
	names := map[string]bool{}
	walkBareTypes(file, func(name string) *NamedTypeNode {
		if _, ok := builtinImport(name); ok {
			names[name] = true
		}
		return nil
	})
	return names
}

// walkBareTypes calls qualify with the name of each type file spells bare,
// in type positions and type patterns, and qualifies it by what qualify
// returns, if anything. A qualified type's qualifier (the `JSON` of
// `JSON.Foo`) is not a bare type, and neither is a directive's scope (the
// `JSON` of `@JSON.field`), which names a namespace.
func walkBareTypes(file *FileBlock, qualify func(name string) *NamedTypeNode) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			switch n := v.Interface().(type) {
			case *NamedTypeNode:
				if n.Base == nil {
					if base := qualify(n.Name); base != nil {
						base.Loc = n.Loc
						n.Base = base
					}
				}
				return
			case *CaseClause:
				if n.TypePattern != nil && n.PatternBase == nil {
					if base := qualify(n.TypePattern.Name); base != nil {
						base.Loc = n.TypePattern.Loc
						n.PatternBase = base
					}
				}
			case *DirectiveApplication:
				for _, arg := range n.Args {
					walk(reflect.ValueOf(arg))
				}
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				field := v.Type().Field(i)
				if !field.IsExported() || field.Type == sourceLocationPtrType || field.Name == "InferredTypeHolder" {
					continue
				}
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(file))
}

// declaresAtTop reports the names file declares at its top level, its
// imports aside: those are the file's own, not its module's.
func declaresAtTop(file *FileBlock) []string {
	var names []string
	for _, form := range file.Forms {
		if _, ok := form.(*ImportDecl); ok {
			continue
		}
		names = append(names, form.DeclaredSymbols()...)
	}
	return names
}
