package dang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateSource(t *testing.T) {
	migrate := func(src string, from, to Edition) (string, error) {
		return MigrateSource("migrate_test.dang", []byte(src), from, to)
	}

	t.Run("edition 1 to 2 rewrites selections", func(t *testing.T) {
		src := "# keep me\nlet x = user.{name, posts.{title}}\nlet y = node.{... on User {name}}\n"
		migrated, err := migrate(src, Edition1, Edition2)
		require.NoError(t, err)
		assert.Equal(t, "# keep me\nlet x = user.{{name, posts.{{title}}}}\nlet y = node.{{... on User {{ name }}}}\n", migrated)
	})

	t.Run("#nofmt regions stay verbatim", func(t *testing.T) {
		src := "let x = f()   # nofmt\n"
		migrated, err := migrate(src, Edition1, Edition2)
		require.NoError(t, err)
		assert.Equal(t, src, migrated)
	})

	t.Run("a #nofmt region whose meaning changes is refused", func(t *testing.T) {
		// Verbatim, `.{name}` would turn into a dot-block in edition 2.
		_, err := migrate("let x = 1\nlet y = user.{name} # nofmt\n", Edition1, Edition2)
		require.ErrorContains(t, err, "would change the meaning of line 2")
	})

	t.Run("template interpolations are migrated", func(t *testing.T) {
		src := "let x = `v=${toJSON(u.{name})}  kept  `\n"
		migrated, err := migrate(src, Edition1, Edition2)
		require.NoError(t, err)
		assert.Equal(t, "let x = `v=${toJSON(u.{{name}})}  kept  `\n", migrated)
	})

	t.Run("multi-line template interpolations are migrated", func(t *testing.T) {
		src := "let x = ```\n  a ${u.{name}}\n    b\n```\n"
		migrated, err := migrate(src, Edition1, Edition2)
		require.NoError(t, err)
		assert.Equal(t, "let x = ```\n  a ${u.{{name}}}\n    b\n```\n", migrated)
	})

	t.Run("same edition is formatting", func(t *testing.T) {
		migrated, err := migrate("let x   = 1\n", Edition2, Edition2)
		require.NoError(t, err)
		assert.Equal(t, "let x = 1\n", migrated)
	})

	t.Run("source must parse as its edition", func(t *testing.T) {
		_, err := migrate("let x = user.{{name}}\n", Edition1, Edition2)
		require.ErrorContains(t, err, `does not parse as edition "1"`)
	})

	t.Run("no migrating backwards", func(t *testing.T) {
		_, err := migrate("let x = 1\n", Edition2, Edition1)
		require.ErrorContains(t, err, "cannot migrate")
	})
}

func TestEditionOrder(t *testing.T) {
	assert.True(t, Edition1.Before(Edition2))
	assert.False(t, Edition2.Before(Edition1))
	assert.False(t, Edition2.Before(Edition2))
	// The zero value is the latest edition, like an unset parser option.
	assert.False(t, Edition("").Before(LatestEdition))
}

func TestSetConfigEdition(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{
			"replaces the key in place",
			"# my project\nedition = \"1\" # old\n\n[imports.Dagger]\ndagger = true\n",
			"# my project\nedition = \"2\"\n\n[imports.Dagger]\ndagger = true\n",
		},
		{
			"adds a missing key before any table",
			"[imports.Dagger]\ndagger = true\n",
			"edition = \"2\"\n\n[imports.Dagger]\ndagger = true\n",
		},
		{
			"ignores keys inside tables",
			"[imports.Foo]\nedition = \"1\"\n",
			"edition = \"2\"\n\n[imports.Foo]\nedition = \"1\"\n",
		},
		{"empty config", "", "edition = \"2\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, string(setConfigEdition([]byte(tc.config), Edition2)))
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

	write("old/dang.toml", "edition = \"1\"\n")
	write("old/main.dang", "let x = user.{name}\n")
	// Governed by old/dang.toml, but unchanged.
	write("old/sub/lib.dang", "let y = 1\n")
	// Dot-directories are governed too.
	write("old/.dagger/main.dang", "let z = user.{name}\n")
	// A nested repo is not entered.
	write("old/vendored/.git/HEAD", "")
	write("old/vendored/main.dang", "let x = user.{name}\n")
	// Already current.
	write("new/dang.toml", "edition = \"2\"\n")
	write("new/main.dang", "let x = user.{{name}}\n")
	// Ungoverned, so the latest edition.
	write("loose/main.dang", "let x = user.{{name}}\n")

	plans, err := PlanMigrations(root, Edition2, MigrateOptions{})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	plan := plans[0]
	assert.Equal(t, filepath.Join(root, "old", "dang.toml"), plan.ConfigPath)
	assert.Equal(t, Edition1, plan.From)
	assert.Equal(t, Edition2, plan.To)
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
	assert.Equal(t, "edition = \"2\"\n", read("old/dang.toml"))

	// Now there's nothing left to do.
	plans, err = PlanMigrations(root, Edition2, MigrateOptions{})
	require.NoError(t, err)
	assert.Empty(t, plans)

	t.Run("refuses a project rooted above the path", func(t *testing.T) {
		write("up/dang.toml", "edition = \"1\"\n")
		write("up/inner/main.dang", "let x = 1\n")
		_, err := PlanMigrations(filepath.Join(root, "up", "inner"), Edition2, MigrateOptions{})
		require.ErrorContains(t, err, "migrate its directory instead")
	})

	t.Run("reports unparseable files", func(t *testing.T) {
		write("broken/dang.toml", "edition = \"1\"\n")
		write("broken/main.dang", "let x = user.{name}\n")
		write("broken/fixtures/bad.dang", "let = =\n")
		plans, err := PlanMigrations(filepath.Join(root, "broken"), Edition2, MigrateOptions{})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.Len(t, plans[0].Files, 1)
		require.Len(t, plans[0].Unparseable, 1)
		assert.Equal(t, filepath.Join(root, "broken", "fixtures", "bad.dang"), plans[0].Unparseable[0].Path)
	})

	t.Run("ungoverned source with a known edition", func(t *testing.T) {
		mod := filepath.Join(root, "pinned")
		write("pinned/.git/HEAD", "") // its own repo, like a Dagger module
		write("pinned/main.dang", "let x = user.{name}\n")

		plans, err := PlanMigrations(mod, Edition2, MigrateOptions{})
		require.NoError(t, err)
		assert.Empty(t, plans, "ungoverned source is the latest edition by default")

		plans, err = PlanMigrations(mod, Edition2, MigrateOptions{UngovernedFrom: Edition1})
		require.NoError(t, err)
		require.Len(t, plans, 1)
		require.NoError(t, plans[0].Write())
		assert.Equal(t, "let x = user.{{name}}\n", read("pinned/main.dang"))
		// The new dang.toml takes precedence over an embedder's pin.
		assert.Equal(t, "edition = \"2\"\n", read("pinned/dang.toml"))
	})
}
