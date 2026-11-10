package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// projectImportedTypes resolves a project's dang.toml imports as `dang
// migrate` does, for MigrateOptions.ImportedTypes.
func projectImportedTypes(configPath string) (map[string]ImportedTypes, error) {
	if _, err := os.Stat(configPath); err != nil {
		return nil, nil
	}
	config, err := LoadProjectConfig(configPath)
	if err != nil {
		return nil, err
	}
	resolved, err := ResolveImportConfigs(context.Background(), config, filepath.Dir(configPath))
	if err != nil {
		return nil, err
	}
	return BuiltinImportTypes(context.Background(), resolved)
}

// planOne plans migrating the project at root to 2.4 and returns its files'
// migrated text by base name.
func planOne(t *testing.T, root string, opts MigrateOptions) (*ProjectMigration, map[string]string) {
	t.Helper()
	plan, err := PlanMigrations(root, Version2_4, opts)
	require.NoError(t, err)
	require.Len(t, plan.Projects, 1)
	migrated := map[string]string{}
	for _, f := range plan.Projects[0].Files {
		rel, err := filepath.Rel(root, f.Path)
		require.NoError(t, err)
		migrated[rel] = f.Migrated
	}
	return plan.Projects[0], migrated
}

const schemaImportTOML = "dang = \"2.1\"\n\n[imports.Schema]\nschema = \"./schema.graphqls\"\n"

func TestMigrateQualifiesImportTypes(t *testing.T) {
	root := writeDangProject(t, map[string]string{
		"dang.toml":       schemaImportTOML,
		"schema.graphqls": scalarJSONSchema,
		"main.dang": `import Schema

` + entrypointCall + `
pub all(xs: [JSON!]!): [JSON!]! { xs }

assert { JSON.encode(call("T", ("{}" :: JSON!), "f", ("[1]" :: JSON!), null)) != "" }
assert { JSON.encode(all([("1" :: JSON!)])) == "[\"1\"]" }
`,
		// Qualifies nothing: it imports nothing bringing in a JSON type.
		"other.dang": "pub plain: String! = JSON.encode([1])\n",
	})
	project, migrated := planOne(t, root, MigrateOptions{ImportedTypes: projectImportedTypes})

	main := migrated["main.dang"]
	require.Contains(t, main, "  receiverValue: Schema.JSON,\n")
	require.Contains(t, main, "  fnArgs: Schema.JSON!,\n")
	require.Contains(t, main, "): Schema.JSON! {\n")
	require.Contains(t, main, "(request :: Schema.JSON!)")
	require.Contains(t, main, "all(xs: [Schema.JSON!]!): [Schema.JSON!]! { xs }")
	// The value stays the builtin namespace.
	require.Contains(t, main, "let request = JSON.encode({{")
	require.Contains(t, main, `JSON.encode(call("T", ("{}" :: Schema.JSON!), "f", ("[1]" :: Schema.JSON!), null)) != ""`)
	require.NotContains(t, main, ": JSON")
	require.Equal(t, "pub plain: String! = JSON.encode([1])\n", migrated["other.dang"])

	// Migrated, it runs as it did.
	_, err := RunDir(context.Background(), root, false)
	require.NoError(t, err)
	require.NoError(t, project.Write())
	_, err = RunDir(context.Background(), root, false)
	require.NoError(t, err)
}

func TestMigrateQualifiesAutoImportTypes(t *testing.T) {
	// An auto-imported import (Dagger's) needs no import statement to bring
	// its types in.
	root := writeDangProject(t, map[string]string{
		"dang.toml": "dang = \"2.1\"\n",
		"main.dang": entrypointCall,
	})
	imports := map[string]ImportedTypes{"Dagger": {Auto: true, Types: []string{"JSON"}}}
	_, migrated := planOne(t, root, MigrateOptions{
		ImportedTypes: func(string) (map[string]ImportedTypes, error) { return imports, nil },
	})
	require.Contains(t, migrated["main.dang"], "fnArgs: Dagger.JSON!")
	require.Contains(t, migrated["main.dang"], "(request :: Dagger.JSON!)")

	ctx := daggerJSONContext(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dang.toml"), []byte("dang = \"2.4\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.dang"), []byte(migrated["main.dang"]), 0o600))
	env, err := RunDir(ctx, root, false)
	require.NoError(t, err)
	requireEntrypointResult(t, callEntrypoint(t, ctx, env))
}

func TestMigrateLeavesOwnAndExplicitTypes(t *testing.T) {
	imports := map[string]ImportedTypes{
		"Dagger": {Auto: true, Types: []string{"JSON", "YAML"}},
		"YAML":   {},
	}
	opts := MigrateOptions{
		ImportedTypes: func(string) (map[string]ImportedTypes, error) { return imports, nil },
	}
	root := writeDangProject(t, map[string]string{
		"dang.toml": "dang = \"2.1\"\n",
		// The module declares a JSON of its own, in another file.
		"types.dang": "scalar JSON\n",
		// An explicit import of YAML replaces the builtin, in every version.
		"main.dang": "import YAML\n\npub f(j: JSON!, y: YAML!): JSON! { j }\n",
	})
	_, migrated := planOne(t, root, opts)
	require.Equal(t, "import YAML\n\npub f(j: JSON!, y: YAML!): JSON! { j }\n", migrated["main.dang"])

	// Without knowing the imports, nothing is qualified.
	root = writeDangProject(t, map[string]string{
		"dang.toml": "dang = \"2.1\"\n",
		"main.dang": entrypointCall,
	})
	_, migrated = planOne(t, root, MigrateOptions{})
	require.Equal(t, entrypointCall, migrated["main.dang"])
}

func TestMigrateRefusesAmbiguousImportTypes(t *testing.T) {
	imports := map[string]ImportedTypes{
		"Dagger": {Auto: true, Types: []string{"JSON"}},
		"Other":  {Auto: true, Types: []string{"JSON"}},
	}
	root := writeDangProject(t, map[string]string{
		"dang.toml": "dang = \"2.1\"\n",
		"main.dang": entrypointCall,
	})
	_, err := PlanMigrations(root, Version2_4, MigrateOptions{
		ImportedTypes: func(string) (map[string]ImportedTypes, error) { return imports, nil },
	})
	require.ErrorContains(t, err, "type JSON is brought in by more than one import (Dagger, Other)")

	got, err := MigrateSourceWithImports("main.dang", []byte(entrypointCall), Version2_1, Version2_4, map[string]ImportedTypes{
		"Dagger": {Auto: true, Types: []string{"JSON"}},
	})
	require.NoError(t, err)
	require.Contains(t, got, "fnArgs: Dagger.JSON!")
}

func TestMigrateQualifiesImportTypePatterns(t *testing.T) {
	source := "pub f(x: Decoded!): String! {\n  case (x) {\n    j: JSON => \"json\"\n    else => \"other\"\n  }\n}\n"
	got, err := MigrateSourceWithImports("main.dang", []byte(source), Version2_1, Version2_4, map[string]ImportedTypes{
		"Dagger": {Auto: true, Types: []string{"JSON"}},
	})
	require.NoError(t, err)
	require.Contains(t, got, "j: Dagger.JSON! =>")
}

func TestBuiltinImportTypesOfSchemasAndModules(t *testing.T) {
	root := writeDangProject(t, map[string]string{
		"lib/lib.dang": "scalar UUID\n\ntype Random {\n  pub n: Int! = 4\n}\n\ntype Other {\n  pub n: Int! = 1\n}\n",
	})
	schema, err := SchemaFromSDL(scalarJSONSchema, "schema.graphqls")
	require.NoError(t, err)
	got, err := BuiltinImportTypes(context.Background(), []ImportConfig{
		{Name: "Dagger", Schema: schema, AutoImport: true},
		{Name: "Lib", DangModuleDir: filepath.Join(root, "lib")},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]ImportedTypes{
		"Dagger": {Auto: true, Types: []string{"JSON"}},
		"Lib":    {Types: []string{"Random", "UUID"}},
	}, got)
}
