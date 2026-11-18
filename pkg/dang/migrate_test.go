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

	t.Run("meaning errors point to the source as written", func(t *testing.T) {
		// Formatting collapses the parentheses onto one line. The meaning
		// check must use the original tree, not that formatted baseline.
		src := "let x = (\n  1\n)\nlet y = user.{name} # nofmt\n"
		_, err := migrate(src, Version1_0, Version2_0)
		require.ErrorContains(t, err, "would change the meaning of line 4")
		assert.Contains(t, err.Error(), "original source")
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

	t.Run("unsorted imports need no migration", func(t *testing.T) {
		src := "import YAML\nimport JSON\nprint(JSON.encode([1, 2]))\n"
		migrated, err := migrate(src, Version2_1, Version2_5)
		require.NoError(t, err)
		assert.Equal(t, src, migrated)
	})

	t.Run("syntax migration preserves import order", func(t *testing.T) {
		for _, imports := range []string{
			"import YAML\nimport JSON\n",
			"import YAML # nofmt\nimport JSON\n",
			"import Zed\nimport Alpha\n",
		} {
			src := imports + "let x = user.{name}\n"
			migrated, err := migrate(src, Version1_0, Version2_0)
			require.NoError(t, err)
			assert.Equal(t, imports+"\nlet x = user.{{name}}\n", migrated)
		}
	})

	t.Run("ordinary formatting still sorts imports", func(t *testing.T) {
		src := "import YAML\nimport JSON\nprint(JSON.encode([1, 2]))\n"
		want := "import JSON\nimport YAML\n\nprint(JSON.encode([1, 2]))\n"
		formatted, err := FormatFileWithVersion([]byte(src), Version2_5)
		require.NoError(t, err)
		assert.Equal(t, want, formatted)
		migrated, err := migrate(src, Version2_5, Version2_5)
		require.NoError(t, err)
		assert.Equal(t, want, migrated)
	})

	t.Run("else-if normalization", func(t *testing.T) {
		conditional := "let x = if (true) { 1 } else { if (false) { 2 } else { 3 } }\n"
		migrated, err := migrate(conditional, Version2_1, Version2_5)
		require.NoError(t, err)
		assert.Equal(t, conditional, migrated)
		// Force actual syntax migration too, so the normalized conditional
		// is checked in the output instead of returning unchanged source.
		migrated, err = migrate(conditional+"let y = user.{name}\n", Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "let x = if (true) { 1 } else if (false) { 2 } else { 3 }\nlet y = user.{{name}}\n", migrated)
	})

	t.Run("empty catch normalizes before implicit calls", func(t *testing.T) {
		for _, tc := range []struct{ source, want string }{
			{"let x = try { f() } catch {}\n", "let x = f\n"},
			{"let x = try { obj.f() } catch {}\n", "let x = obj.f\n"},
			{"let x = try { fs[0]() } catch {}\n", "let x = fs[0]\n"},
			{"let x = try { try { f() } catch {} } catch {}\n", "let x = { f }\n"},
		} {
			original, err := Parse("original.dang", []byte(tc.source), WithVersion(Version2_0))
			require.NoError(t, err)
			want, err := Parse("expected.dang", []byte(tc.want), WithVersion(Version2_1))
			require.NoError(t, err)
			_, differs := firstASTDifference(original.(Node), want.(Node))
			assert.False(t, differs, "normalizing %s", tc.source)
			migrated, err := migrate(tc.source, Version2_0, Version2_1)
			require.NoError(t, err)
			got, err := Parse("migrated.dang", []byte(migrated), WithVersion(Version2_1))
			require.NoError(t, err)
			_, differs = firstASTDifference(want.(Node), got.(Node))
			assert.False(t, differs, "migrating %s", tc.source)
		}
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

	// The raising else added to a 2.0 case must come out stable, wherever
	// the case sits.
	for _, tc := range []struct{ name, src, want string }{
		{
			"after a multi-line clause",
			"let y = case (x) {\n  0 => 1\n  1 => {\n    2\n  }\n}\n",
			"let y = case (x) {\n  0 => 1\n  1 => {\n    2\n  }\n  else => raise \"no case clause matched\"\n}\n",
		},
		{
			"in a one-line block arg",
			"let y = [1].map { x => case (x) { 1 => \"a\" } }\n",
			"let y = [1].map { x =>\n  case (x) {\n    1 => \"a\"\n    else => raise \"no case clause matched\"\n  }\n}\n",
		},
		{
			"in a one-line for loop",
			"for { case (n) { 1 => break } }\n",
			"loop {\n  case (n) {\n    1 => break\n    else => raise \"no case clause matched\"\n  }\n}\n",
		},
		{
			"keeps a comment after a multi-line clause",
			"let y = case (x) {\n  1 => {\n    2\n  } # done\n}\n",
			"let y = case (x) {\n  1 => {\n    2\n  } # done\n  else => raise \"no case clause matched\"\n}\n",
		},
		{
			"in a one-line object",
			"let y = {{a: case (x) { 1 => 2 }}}\n",
			"let y = {{\n  a: case (x) {\n    1 => 2\n    else => raise \"no case clause matched\"\n  }\n}}\n",
		},
		{
			"in one-line parentheses",
			"let y = (case (x) { 1 => 2 })\n",
			"let y = (\n  case (x) {\n    1 => 2\n    else => raise \"no case clause matched\"\n  }\n)\n",
		},
		{
			"in an interpolation in a one-line block arg",
			"let y = xs.map { x => `v=${case (x) { 1 => 2 }}` }\n",
			"let y = xs.map { x =>\n  `v=${case (x) {\n  1 => 2\n  else => raise \"no case clause matched\"\n}}`\n}\n",
		},
		{
			"keeps the blank line before a closing comment",
			"let y = case (x) {\n  1 => {\n    2\n  }\n\n  # end\n}\n",
			"let y = case (x) {\n  1 => {\n    2\n  }\n  else => raise \"no case clause matched\"\n\n  # end\n}\n",
		},
		{
			"adds no blank line before a closing comment",
			"let y = case (x) {\n  1 => {\n    2\n  }\n  # end\n}\n",
			"let y = case (x) {\n  1 => {\n    2\n  }\n  else => raise \"no case clause matched\"\n  # end\n}\n",
		},
	} {
		t.Run("Dang 2.0 case without else "+tc.name, func(t *testing.T) {
			migrated, err := migrate(tc.src, Version2_0, Version2_1)
			require.NoError(t, err)
			assert.Equal(t, tc.want, migrated)
		})
	}

	t.Run("a one-line block arg too long to stay on one line", func(t *testing.T) {
		// The case only makes the file migrate rather than stay as it is.
		src := "let y = case (x) {\n  0 => 1\n}\n" +
			"let resultOfTheComputation = someList.map { x => doSomething(alpha: x.alpha, beta: x.beta, gamma: x.gamma) }\n"
		_, err := migrate(src, Version2_0, Version2_1)
		require.NoError(t, err)
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

func TestMigrationMeaningComparison(t *testing.T) {
	for _, tc := range []struct {
		name, original, migrated string
		differs                  bool
	}{
		{
			name:     "import reordering is not assumed safe",
			original: "import Zed\nimport Alpha\n",
			migrated: "import Alpha\nimport Zed\n",
			differs:  true,
		},
		{
			name:     "else-if spelling",
			original: "let x = if (true) { 1 } else { if (false) { 2 } else { 3 } }\n",
			migrated: "let x = if (true) { 1 } else if (false) { 2 } else { 3 }\n",
		},
		{
			name:     "else block declarations cannot be dropped",
			original: "let x = if (true) { 1 } else { let y = 2; if (false) { y } else { 3 } }\n",
			migrated: "let x = if (true) { 1 } else if (false) { y } else { 3 }\n",
			differs:  true,
		},
		{
			name:     "literal quote spelling",
			original: "let s = \"\"\"waivers:\n  - kind: vulnerability\n    owner: platform\n\"\"\"\n",
			migrated: "let s = '''\nwaivers:\n  - kind: vulnerability\n    owner: platform\n'''\n",
		},
		{
			name:     "literal indentation is meaning",
			original: "let s = \"\"\"waivers:\n  - kind: vulnerability\n    owner: platform\n\"\"\"\n",
			migrated: "let s = '''\nwaivers:\n    - kind: vulnerability\n      owner: platform\n'''\n",
			differs:  true,
		},
		{
			name:     "constructor auto-call",
			original: "let p = maybe ?? P()\n",
			migrated: "let p = maybe ?? P\n",
		},
		{
			name:     "function or constructor alias auto-call",
			original: "let p = make()\n",
			migrated: "let p = make\n",
		},
		{
			name:     "selected constructor auto-call",
			original: "let p = module.P()\n",
			migrated: "let p = module.P\n",
		},
		{
			name:     "indexed auto-call",
			original: "let p = fs[0]()\n",
			migrated: "let p = fs[0]\n",
		},
		{
			name:     "asserted calls are explicit",
			original: "let p = f!()\n",
			migrated: "let p = f!\n",
			differs:  true,
		},
		{
			name:     "reference suppresses auto-call",
			original: "let p = &f()\n",
			migrated: "let p = &f\n",
			differs:  true,
		},
		{
			name:     "grouped reference suppresses auto-call",
			original: "let p = &(f())\n",
			migrated: "let p = &(f)\n",
			differs:  true,
		},
		{
			name:     "asserted callee suppresses operand auto-call",
			original: "let p = f()!()\n",
			migrated: "let p = f!()\n",
			differs:  true,
		},
		{
			name:     "reference permits receiver auto-call",
			original: "let p = &factory().f\n",
			migrated: "let p = &factory.f\n",
		},
		{
			name:     "reference keeps outer call but permits receiver auto-call",
			original: "let p = &factory().f()\n",
			migrated: "let p = &factory.f()\n",
		},
		{
			name:     "arguments cannot be removed",
			original: "let p = f(x: 1)\n",
			migrated: "let p = f\n",
			differs:  true,
		},
		{
			name:     "arguments cannot be changed",
			original: "let p = f(x: 1)\n",
			migrated: "let p = f(x: 2)\n",
			differs:  true,
		},
		{
			name:     "block arguments cannot be removed",
			original: "let p = f { 1 }\n",
			migrated: "let p = f\n",
			differs:  true,
		},
		{
			name:     "empty parens before a block argument",
			original: "let p = xs.map() { x => x }\n",
			migrated: "let p = xs.map { x => x }\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := Parse("original.dang", []byte(tc.original), WithVersion(Version2_1))
			require.NoError(t, err)
			migrated, err := Parse("migrated.dang", []byte(tc.migrated), WithVersion(Version2_5))
			require.NoError(t, err)
			loc, differs := firstASTDifference(original.(Node), migrated.(Node))
			assert.Equal(t, tc.differs, differs)
			if tc.differs {
				require.NotNil(t, loc)
				assert.Equal(t, 1, loc.Line)
			}
		})
	}
}

func TestMigrationKeepsCallsOfOtherExpressions(t *testing.T) {
	// These general callee expressions are not all expressible in today's
	// parser, but the AST comparison must not equate their calls with reads.
	for _, callee := range []Node{
		&FunCall{Fun: &Symbol{Name: "factory"}},
		&FunctionRef{Expr: &Symbol{Name: "f"}},
		&Conditional{Condition: &Symbol{Name: "condition"}, Then: &Symbol{Name: "f"}, Else: &Symbol{Name: "g"}},
		&Grouped{Expr: &Symbol{Name: "f"}},
		&Symbol{Name: "f", AutoCall: true},
	} {
		called := &FileBlock{Forms: []Node{&FunCall{Fun: callee}}}
		bare := &FileBlock{Forms: []Node{callee}}
		_, differs := firstASTDifference(called, bare)
		assert.True(t, differs, "must preserve explicit call of %T", callee)
	}
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

	plans := planProjects(t, root, Version2_0, MigrateOptions{})
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
	assert.Empty(t, planProjects(t, root, Version2_0, MigrateOptions{}))

	t.Run("refuses a project rooted above the path", func(t *testing.T) {
		write("up/dang.toml", "dang = \"1.0\"\n")
		write("up/inner/main.dang", "let x = 1\n")
		_, err := PlanMigrations(filepath.Join(root, "up", "inner"), Version2_0, MigrateOptions{})
		require.ErrorContains(t, err, "migrate its directory instead")
	})

	t.Run("refuses a for loop that would call a sibling file's loop", func(t *testing.T) {
		write("loopy/dang.toml", "dang = \"2.0\"\n")
		write("loopy/a.dang", "pub loop(n: Int!): Int! {\n  n\n}\n")
		write("loopy/b.dang", "count(): Int! {\n  for {\n    break 1\n  }\n}\n")
		_, err := PlanMigrations(filepath.Join(root, "loopy"), Version2_1, MigrateOptions{})
		require.ErrorContains(t, err, "`for` loop becomes a call to `loop`")

		// Another directory is another module.
		require.NoError(t, os.Remove(filepath.Join(root, "loopy", "a.dang")))
		write("loopy/lib/a.dang", "pub loop(n: Int!): Int! {\n  n\n}\n")
		plans := planProjects(t, filepath.Join(root, "loopy"), Version2_1, MigrateOptions{})
		require.Len(t, plans, 1)
	})

	t.Run("refuses a file", func(t *testing.T) {
		write("script/main.dang", "let x = user.{name}\n")
		_, err := PlanMigrations(filepath.Join(root, "script", "main.dang"), Version2_0, MigrateOptions{UndeclaredFrom: Version1_0})
		require.ErrorContains(t, err, "is not a directory")
	})

	t.Run("reports unparseable files", func(t *testing.T) {
		write("broken/dang.toml", "dang = \"1.0\"\n")
		write("broken/main.dang", "let x = user.{name}\n")
		write("broken/fixtures/bad.dang", "let = =\n")
		plans := planProjects(t, filepath.Join(root, "broken"), Version2_0, MigrateOptions{})
		require.Len(t, plans, 1)
		require.Len(t, plans[0].Files, 1)
		require.Len(t, plans[0].Unparseable, 1)
		assert.Equal(t, filepath.Join(root, "broken", "fixtures", "bad.dang"), plans[0].Unparseable[0].Path)
	})

	t.Run("ungoverned source with a known version", func(t *testing.T) {
		mod := filepath.Join(root, "pinned")
		write("pinned/.git/HEAD", "") // its own repo, like a Dagger module
		write("pinned/main.dang", "let x = user.{name}\n")

		assert.Empty(t, planProjects(t, mod, Version2_0, MigrateOptions{}),
			"ungoverned source is the current version by default")

		plans := planProjects(t, mod, Version2_0, MigrateOptions{UndeclaredFrom: Version1_0})
		require.Len(t, plans, 1)
		require.NoError(t, plans[0].Write())
		assert.Equal(t, "let x = user.{{name}}\n", read("pinned/main.dang"))
		// The new dang.toml takes precedence over an embedder's pin.
		assert.Equal(t, "dang = \"2.0\"\n", read("pinned/dang.toml"))
	})

	t.Run("an old dang.toml without a version", func(t *testing.T) {
		// A 2.0 project already had a dang.toml, for its imports, but no
		// `dang` key: that means the running Dang's version, so --from says
		// otherwise.
		mod := filepath.Join(root, "unversioned")
		write("unversioned/.git/HEAD", "")
		write("unversioned/dang.toml", "[imports.Dagger]\ndagger = true\n")
		write("unversioned/main.dang", "for { break }\n")

		assert.Empty(t, planProjects(t, mod, Version2_1, MigrateOptions{}),
			"an unversioned dang.toml is the current version")

		plans := planProjects(t, mod, Version2_1, MigrateOptions{UndeclaredFrom: Version2_0})
		require.Len(t, plans, 1)
		assert.Equal(t, Version2_0, plans[0].From)
		require.NoError(t, plans[0].Write())
		assert.Equal(t, "loop { break }\n", read("unversioned/main.dang"))
		assert.Equal(t, "dang = \"2.1\"\n\n[imports.Dagger]\ndagger = true\n", read("unversioned/dang.toml"))
	})

	t.Run("--from applies to every project declaring no version", func(t *testing.T) {
		// Source whose version nothing declares is the running Dang's
		// version, wherever it is, so --from overrides that for all of it:
		// root's own project, nested projects without a `dang` key, and
		// ungoverned source alike.
		mod := filepath.Join(root, "mono")
		write("mono/.git/HEAD", "")
		write("mono/dang.toml", "")
		write("mono/main.dang", "for { break }\n")
		write("mono/tools/dang.toml", "")
		write("mono/tools/main.dang", "for { break }\n")
		// A nested project with a declared version keeps it.
		write("mono/declared/dang.toml", "dang = \"1.0\"\n")
		write("mono/declared/main.dang", "let x = user.{name}\n")

		result, err := PlanMigrations(mod, Version2_1, MigrateOptions{UndeclaredFrom: Version2_0})
		require.NoError(t, err)
		froms := map[string]Version{}
		for _, plan := range result.Projects {
			froms[plan.ConfigPath] = plan.From
		}
		assert.Equal(t, map[string]Version{
			filepath.Join(mod, "dang.toml"):             Version2_0,
			filepath.Join(mod, "declared", "dang.toml"): Version1_0,
			filepath.Join(mod, "tools", "dang.toml"):    Version2_0,
		}, froms)
		assert.Equal(t, []string{mod, filepath.Join(mod, "tools")}, result.Undeclared)

		// Without --from, only the declared project is older, and the
		// others are listed, as they may be written for an older Dang.
		result, err = PlanMigrations(mod, Version2_1, MigrateOptions{})
		require.NoError(t, err)
		require.Len(t, result.Projects, 1)
		assert.Equal(t, filepath.Join(mod, "declared", "dang.toml"), result.Projects[0].ConfigPath)
		assert.Equal(t, []string{mod, filepath.Join(mod, "tools")}, result.Undeclared)
	})
}

// planProjects plans migrations that must succeed, returning the projects.
func planProjects(t *testing.T, root string, to Version, opts MigrateOptions) []*ProjectMigration {
	t.Helper()
	result, err := PlanMigrations(root, to, opts)
	require.NoError(t, err)
	return result.Projects
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
		assert.Equal(t, CurrentVersion, result.Version)
		assert.Equal(t, "dang = \""+CurrentVersion.String()+"\"\n\n# keep\n[imports.Dagger]\ndagger = true\n", read("legacy/dang.toml"))
	})

	t.Run("refuses a directory inside another project", func(t *testing.T) {
		dir := filepath.Join(root, "fresh", "sub")
		require.NoError(t, os.Mkdir(dir, 0755))
		_, err := InitProject(dir)
		require.ErrorContains(t, err, "already part of the project")
	})
}
