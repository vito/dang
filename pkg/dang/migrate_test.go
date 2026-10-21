package dang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateSource(t *testing.T) {
	migrate := func(src string, from, to Version) (string, error) {
		return MigrateSource("migrate_test.dang", []byte(src), from, to)
	}

	t.Run("Dang 1.0 to 2.0 rewrites selections", func(t *testing.T) {
		src := "# keep me\nlet x = user.{name, posts.{title}}\nlet y = node.{... on User {name}}\n"
		migrated, err := migrate(src, Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "# keep me\nlet x = user.{{name, posts.{{title}}}}\nlet y = node.{{... on User { name }}}\n", migrated)
	})

	t.Run("#nofmt regions stay verbatim", func(t *testing.T) {
		src := "let x = f()   # nofmt\n"
		migrated, err := migrate(src, Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, src, migrated)
	})

	t.Run("a #nofmt region whose meaning changes is refused", func(t *testing.T) {
		// Verbatim, `.{name}` would turn into a dot-block in 2.0.
		_, err := migrate("let x = 1\nlet y = user.{name} # nofmt\n", Version1_0, Version2_0)
		require.ErrorContains(t, err, "would change the meaning of line 2")
	})

	t.Run("template interpolations are migrated", func(t *testing.T) {
		src := "let x = `v=${toJSON(u.{name})}  kept  `\n"
		migrated, err := migrate(src, Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "let x = `v=${toJSON(u.{{name}})}  kept  `\n", migrated)
	})

	t.Run("multi-line template interpolations are migrated", func(t *testing.T) {
		src := "let x = ```\n  a ${u.{name}}\n    b\n```\n"
		migrated, err := migrate(src, Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "let x = ```\n  a ${u.{{name}}}\n    b\n```\n", migrated)
	})

	t.Run("same version is formatting", func(t *testing.T) {
		migrated, err := migrate("let x   = 1\n", Version2_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "let x = 1\n", migrated)
	})

	t.Run("a file spelled the same in both versions is left alone", func(t *testing.T) {
		// It uses nothing 2.1 changed, so nothing is reformatted either,
		// template interpolations included.
		src := "let x   = user.{{name}}\nprint(`x ${a+b}`)\n"
		migrated, err := migrate(src, Version2_0, Version2_1)
		require.NoError(t, err)
		assert.Equal(t, src, migrated)

		// Likewise for a 1.0 file that uses none of what 2.0 changed.
		src = "let x   = 1\n"
		migrated, err = migrate(src, Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, src, migrated)
	})

	t.Run("a file is reformatted when its old spelling must go", func(t *testing.T) {
		// Formatting already prints 2.0's comma separator as `;` in both
		// versions, but the comma itself doesn't parse in 2.1.
		migrated, err := migrate("let x = { let a   = 1, a }\n", Version2_0, Version2_1)
		require.NoError(t, err)
		assert.Equal(t, "let x = { let a = 1; a }\n", migrated)
	})

	t.Run("source must parse as its version", func(t *testing.T) {
		_, err := migrate("let x = user.{{name}}\n", Version1_0, Version2_0)
		require.ErrorContains(t, err, `does not parse as Dang 1.0`)
	})

	t.Run("no migrating backwards", func(t *testing.T) {
		_, err := migrate("let x = 1\n", Version2_0, Version1_0)
		require.ErrorContains(t, err, "cannot migrate")
	})
}

func TestSetConfigVersion(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{
			"replaces the value in place, keeping comments",
			"# my project\ndang = \"1.0\" # old\n\n[imports.Dagger]\ndagger = true\n",
			"# my project\ndang = \"2.0\" # old\n\n[imports.Dagger]\ndagger = true\n",
		},
		{
			"keeps CRLF line endings",
			"dang = '1.0'\r\n[imports.A]\r\n",
			"dang = \"2.0\"\r\n[imports.A]\r\n",
		},
		{
			"adds a missing key with CRLF line endings",
			"# my project\r\n[imports.A]\r\n",
			"dang = \"2.0\"\r\n\r\n# my project\r\n[imports.A]\r\n",
		},
		{
			"adds a missing key before any table",
			"[imports.Dagger]\ndagger = true\n",
			"dang = \"2.0\"\n\n[imports.Dagger]\ndagger = true\n",
		},
		{
			"ignores keys inside tables",
			"[imports.Foo]\ndang = \"1.0\"\n",
			"dang = \"2.0\"\n\n[imports.Foo]\ndang = \"1.0\"\n",
		},
		{"empty config", "", "dang = \"2.0\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(setConfigVersion([]byte(tc.config), Version2_0)))
		})
	}
}

func TestPlanMigrations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	write := func(rel, contents string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	}
	read := func(rel string) string {
		contents, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		return string(contents)
	}

	write("old/dang.toml", "dang = \"1.0\"\n")
	write("old/main.dang", "let x = user.{name}\n")
	// Governed by old/dang.toml, but unchanged.
	write("old/sub/lib.dang", "let y = 1\n")
	// Dot-directories are governed too.
	write("old/.dagger/main.dang", "let z = user.{name}\n")
	// A nested repo is not entered.
	write("old/vendored/.git/HEAD", "")
	write("old/vendored/main.dang", "let x = user.{name}\n")
	// Already current.
	write("new/dang.toml", "dang = \"2.0\"\n")
	write("new/main.dang", "let x = user.{{name}}\n")
	// Ungoverned, so the current version.
	write("loose/main.dang", "let x = user.{{name}}\n")

	plans, err := PlanMigrations(root, Version2_0, MigrateOptions{})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	plan := plans[0]
	assert.Equal(t, filepath.Join(root, "old", "dang.toml"), plan.ConfigPath)
	assert.Equal(t, Version1_0, plan.From)
	assert.Equal(t, Version2_0, plan.To)
	var paths []string
	for _, f := range plan.Files {
		rel, err := filepath.Rel(root, f.Path)
		require.NoError(t, err)
		paths = append(paths, rel)
	}
	assert.Equal(t, []string{"old/.dagger/main.dang", "old/main.dang", "old/sub/lib.dang"}, paths)
	assert.True(t, plan.Files[0].Changed())
	assert.True(t, plan.Files[1].Changed())
	assert.False(t, plan.Files[2].Changed())

	// Planning changes nothing; writing migrates files and config together.
	assert.Equal(t, "let x = user.{name}\n", read("old/main.dang"))
	require.NoError(t, plan.Write())
	assert.Equal(t, "let x = user.{{name}}\n", read("old/main.dang"))
	assert.Equal(t, "let z = user.{{name}}\n", read("old/.dagger/main.dang"))
	assert.Equal(t, "let x = user.{name}\n", read("old/vendored/main.dang"))
	assert.Equal(t, "dang = \"2.0\"\n", read("old/dang.toml"))

	// Now there's nothing left to do.
	plans, err = PlanMigrations(root, Version2_0, MigrateOptions{})
	require.NoError(t, err)
	assert.Empty(t, plans)

	t.Run("refuses a project rooted above the path", func(t *testing.T) {
		write("up/dang.toml", "dang = \"1.0\"\n")
		write("up/inner/main.dang", "let x = 1\n")
		_, err := PlanMigrations(filepath.Join(root, "up", "inner"), Version2_0, MigrateOptions{})
		require.ErrorContains(t, err, "migrate its directory instead")
	})

	t.Run("reports unparseable files", func(t *testing.T) {
		write("broken/dang.toml", "dang = \"1.0\"\n")
		write("broken/main.dang", "let x = user.{name}\n")
		write("broken/fixtures/bad.dang", "let = =\n")
		plans, err := PlanMigrations(filepath.Join(root, "broken"), Version2_0, MigrateOptions{})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.Len(t, plans[0].Files, 1)
		require.Len(t, plans[0].Unparseable, 1)
		assert.Equal(t, filepath.Join(root, "broken", "fixtures", "bad.dang"), plans[0].Unparseable[0].Path)
	})

	t.Run("ungoverned source with a known version", func(t *testing.T) {
		mod := filepath.Join(root, "pinned")
		write("pinned/.git/HEAD", "") // its own repo, like a Dagger module
		write("pinned/main.dang", "let x = user.{name}\n")

		plans, err := PlanMigrations(mod, Version2_0, MigrateOptions{})
		require.NoError(t, err)
		assert.Empty(t, plans, "ungoverned source is the current version by default")

		plans, err = PlanMigrations(mod, Version2_0, MigrateOptions{UndeclaredFrom: Version1_0})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.NoError(t, plans[0].Write())
		assert.Equal(t, "let x = user.{{name}}\n", read("pinned/main.dang"))
		// The new dang.toml takes precedence over an embedder's pin.
		assert.Equal(t, "dang = \"2.0\"\n", read("pinned/dang.toml"))
	})

	t.Run("an old dang.toml without a version", func(t *testing.T) {
		// A 2.0 project already had a dang.toml, for its imports, but no
		// `dang` key: by default that means 2.1, so --from says otherwise.
		mod := filepath.Join(root, "unversioned")
		write("unversioned/.git/HEAD", "")
		write("unversioned/dang.toml", "[imports.Dagger]\ndagger = true\n")
		write("unversioned/main.dang", "for { break }\n")

		plans, err := PlanMigrations(mod, Version2_1, MigrateOptions{})
		require.NoError(t, err)
		assert.Empty(t, plans, "an unversioned dang.toml is the default version")

		plans, err = PlanMigrations(mod, Version2_1, MigrateOptions{UndeclaredFrom: Version2_0})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		assert.Equal(t, Version2_0, plans[0].From)
		require.NoError(t, plans[0].Write())
		assert.Equal(t, "loop { break }\n", read("unversioned/main.dang"))
		assert.Equal(t, "dang = \"2.1\"\n\n[imports.Dagger]\ndagger = true\n", read("unversioned/dang.toml"))
	})
}

func TestInitProject(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	read := func(rel string) string {
		contents, err := os.ReadFile(filepath.Join(root, rel))
		require.NoError(t, err)
		return string(contents)
	}

	t.Run("creates a dang.toml declaring the current version", func(t *testing.T) {
		dir := filepath.Join(root, "fresh")
		require.NoError(t, os.Mkdir(dir, 0755))
		result, err := InitProject(dir)
		require.NoError(t, err)
		assert.True(t, result.Created)
		assert.Equal(t, CurrentVersion, result.Version)
		assert.Equal(t, "dang = \""+CurrentVersion.String()+"\"\n", read("fresh/dang.toml"))

		// Running it again changes nothing.
		result, err = InitProject(dir)
		require.NoError(t, err)
		assert.True(t, result.AlreadyDeclared)
	})

	t.Run("stamps an unversioned dang.toml with what it already means", func(t *testing.T) {
		dir := filepath.Join(root, "legacy")
		require.NoError(t, os.Mkdir(dir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dang.toml"), []byte("# keep\n[imports.Dagger]\ndagger = true\n"), 0644))
		result, err := InitProject(dir)
		require.NoError(t, err)
		assert.False(t, result.Created)
		assert.Equal(t, DefaultVersion, result.Version)
		assert.Equal(t, "dang = \""+DefaultVersion.String()+"\"\n\n# keep\n[imports.Dagger]\ndagger = true\n", read("legacy/dang.toml"))
	})

	t.Run("refuses a directory inside another project", func(t *testing.T) {
		dir := filepath.Join(root, "fresh", "sub")
		require.NoError(t, os.Mkdir(dir, 0755))
		_, err := InitProject(dir)
		require.ErrorContains(t, err, "already part of the project")
	})
}
