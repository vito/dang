package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseSingleForm parses src as the given edition and returns its only form.
func parseSingleForm(t *testing.T, src string, edition Edition) Node {
	t.Helper()
	parsed, err := Parse("edition_test.dang", []byte(src), WithEdition(edition))
	require.NoError(t, err)
	block := parsed.(*FileBlock)
	require.Len(t, block.Forms, 1)
	return block.Forms[0]
}

func TestEditionSelectionSyntax(t *testing.T) {
	t.Run("edition 1 single braces are object selection", func(t *testing.T) {
		node := parseSingleForm(t, `user.{name, posts.{title}}`, Edition1)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.Fields, 2)
		assert.Equal(t, "name", sel.Fields[0].Name)
		assert.Equal(t, "posts", sel.Fields[1].Name)
		require.NotNil(t, sel.Fields[1].Selection)
		assert.Equal(t, "title", sel.Fields[1].Selection.Fields[0].Name)
	})

	t.Run("edition 1 inline fragments use single braces", func(t *testing.T) {
		node := parseSingleForm(t, `node.{... on User {name}, ... on Post {title}}`, Edition1)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.InlineFragments, 2)
		assert.Equal(t, "User", sel.InlineFragments[0].TypeName.Name)
		assert.Equal(t, "name", sel.InlineFragments[0].Fields[0].Name)
	})

	t.Run("edition 1 rejects double-brace selection", func(t *testing.T) {
		_, err := Parse("edition_test.dang", []byte(`user.{{name}}`), WithEdition(Edition1))
		require.Error(t, err)
	})

	t.Run("edition 2 double braces are object selection", func(t *testing.T) {
		node := parseSingleForm(t, `user.{{name, posts.{{title}}}}`, Edition2)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.Fields, 2)
	})

	t.Run("edition 2 single braces are dot-block application", func(t *testing.T) {
		node := parseSingleForm(t, `41.{ inc(_) }`, Edition2)
		_, ok := node.(*DotApply)
		require.True(t, ok, "expected *DotApply, got %T", node)
	})

	t.Run("no edition option parses as the default edition", func(t *testing.T) {
		parsed, err := Parse("edition_test.dang", []byte(`41.{ inc(_) }`))
		require.NoError(t, err)
		_, ok := parsed.(*FileBlock).Forms[0].(*DotApply)
		require.True(t, ok, "default edition should have dot-blocks")
	})
}

func TestEditionFormatting(t *testing.T) {
	src := "let x = user.{name, posts.{title}}\nlet y = node.{... on User {name}}\n"

	formatted, err := FormatFileWithEdition([]byte(src), Edition1)
	require.NoError(t, err)
	assert.Equal(t, "let x = user.{name, posts.{title}}\nlet y = node.{... on User { name }}\n", formatted)

	// Formatting is idempotent, and the output still parses as edition 1.
	again, err := FormatFileWithEdition([]byte(formatted), Edition1)
	require.NoError(t, err)
	assert.Equal(t, formatted, again)

	// Edition 2 formatting is unchanged.
	formatted2, err := FormatFile([]byte("let x = user.{{name}}\n"))
	require.NoError(t, err)
	assert.Equal(t, "let x = user.{{name}}\n", formatted2)
}

// assertMigratesTo checks that edition 1 source migrates to want, which
// proves the edition 1 form parses into the same AST as the edition 2 one.
func assertMigratesTo(t *testing.T, src, want string) {
	t.Helper()
	migrated, err := MigrateSource("edition_test.dang", []byte(src), Edition1, Edition2)
	require.NoError(t, err)
	assert.Equal(t, want, migrated)
}

func TestEditionForLoops(t *testing.T) {
	t.Run("edition 1 condition loop is a guarded loop", func(t *testing.T) {
		assertMigratesTo(t,
			"for (i < 3) {\n  i += 1\n}\n",
			"loop {\n  if (i < 3) {\n    i += 1\n  } else {\n    break\n  }\n}\n",
		)
	})

	t.Run("edition 1 infinite loop is a loop", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = for {\n  break 42\n}\n",
			"let x = loop {\n  break 42\n}\n",
		)
	})

	t.Run("edition 1 loop comments stay in place", func(t *testing.T) {
		assertMigratesTo(t,
			"# before\nfor (i < 3) {\n  # body\n  i += 1 # step\n}\nlet y = 2\n",
			"# before\nloop {\n  if (i < 3) {\n    # body\n    i += 1 # step\n  } else {\n    break\n  }\n}\nlet y = 2\n",
		)
	})

	t.Run("edition 2 for is an identifier", func(t *testing.T) {
		node := parseSingleForm(t, `for { 1 }`, Edition2)
		call, ok := node.(*FunCall)
		require.True(t, ok, "expected *FunCall, got %T", node)
		assert.Equal(t, "for", call.Fun.(*Symbol).Name)

		_, err := Parse("edition_test.dang", []byte(`for (true) { 1 }`), WithEdition(Edition2))
		require.Error(t, err)
	})

	t.Run("edition 1 for remains an identifier elsewhere", func(t *testing.T) {
		node := parseSingleForm(t, `for + 1`, Edition1)
		_, ok := node.(*Addition)
		require.True(t, ok, "expected *Addition, got %T", node)
	})
}

func TestEditionCommaFormSep(t *testing.T) {
	t.Run("edition 1 commas separate block forms", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = { let a = 1, a }\n",
			"let x = { let a = 1; a }\n",
		)
	})

	t.Run("edition 1 commas separate block argument forms", func(t *testing.T) {
		assertMigratesTo(t,
			"[1].map { x => let y = x, y }\n",
			"[1].map { x =>\n  let y = x\n  y\n}\n",
		)
	})

	t.Run("edition 1 commas separate top-level forms", func(t *testing.T) {
		assertMigratesTo(t, "let a = 1, let b = 2\n", "let a = 1\nlet b = 2\n")
	})

	t.Run("edition 2 rejects commas between forms", func(t *testing.T) {
		for _, src := range []string{
			"let x = { let a = 1, a }\n",
			"[1].map { x => let y = x, y }\n",
			"let a = 1, let b = 2\n",
		} {
			_, err := Parse("edition_test.dang", []byte(src), WithEdition(Edition2))
			assert.Error(t, err, "%q", src)
		}
	})
}

func TestEditionUnmatchedCase(t *testing.T) {
	t.Run("edition 1 case without else raises when nothing matches", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\"\n  2 => \"two\"\n}\n",
			"let x = case (n) {\n  1 => \"one\"\n  2 => \"two\"\n  else => raise \"no case clause matched\"\n}\n",
		)
	})

	t.Run("edition 1 case with else is unchanged", func(t *testing.T) {
		src := "let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n}\n"
		assertMigratesTo(t, src, src)
	})

	t.Run("edition 1 synthetic else keeps comments in place", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  # first\n  1 => \"one\" # one\n  2 => \"two\" # two\n  # end\n}\nlet y = 2\n",
			"let x = case (n) {\n  # first\n  1 => \"one\" # one\n  2 => \"two\" # two\n  else => raise \"no case clause matched\"\n  # end\n}\nlet y = 2\n",
		)
	})

	t.Run("edition 1 unmatched case is non-null and raises", func(t *testing.T) {
		dir := t.TempDir()
		src := "let f(n: Int!): String! { case (n) { 1 => \"one\" } }\nassert { f(1) == \"one\" }\nf(2)\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(src), 0o600))
		_, err := RunDir(ContextWithEdition(context.Background(), Edition1), dir, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no case clause matched")
	})

	t.Run("edition 2 case without else is unchanged", func(t *testing.T) {
		node := parseSingleForm(t, "case (n) { 1 => \"one\" }", Edition2)
		c, ok := node.(*Case)
		require.True(t, ok, "expected *Case, got %T", node)
		require.Len(t, c.Clauses, 1)
	})
}

func TestEditionClausesAfterElse(t *testing.T) {
	t.Run("edition 1 drops clauses after else", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n  5 => \"five\"\n}\n",
			"let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n}\n",
		)
	})

	t.Run("edition 1 keeps standalone comments of dropped clauses", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\" # one\n  else => \"many\" # many\n  # five is special\n  5 => \"five\"\n}\nlet y = 2\n",
			"let x = case (n) {\n  1 => \"one\" # one\n  else => \"many\" # many\n  # five is special\n}\nlet y = 2\n",
		)
	})

	t.Run("edition 2 keeps clauses after else", func(t *testing.T) {
		node := parseSingleForm(t, "case (n) { else => \"many\", 5 => \"five\" }", Edition2)
		c, ok := node.(*Case)
		require.True(t, ok, "expected *Case, got %T", node)
		require.Len(t, c.Clauses, 2)
	})
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dang.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	return path
}

func TestProjectConfigEdition(t *testing.T) {
	t.Run("declared edition", func(t *testing.T) {
		config, err := LoadProjectConfig(writeConfig(t, "edition = \"1\"\n"))
		require.NoError(t, err)
		assert.Equal(t, Edition1, config.Edition)
		assert.Equal(t, Edition1, config.EffectiveEdition())
	})

	t.Run("missing edition is the default", func(t *testing.T) {
		config, err := LoadProjectConfig(writeConfig(t, "[imports.Dagger]\ndagger = true\n"))
		require.NoError(t, err)
		assert.Equal(t, Edition(""), config.Edition)
		assert.Equal(t, DefaultEdition, config.EffectiveEdition())
	})

	t.Run("unknown edition", func(t *testing.T) {
		_, err := LoadProjectConfig(writeConfig(t, "edition = \"3\"\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown edition "3"`)
	})

	t.Run("non-string edition", func(t *testing.T) {
		_, err := LoadProjectConfig(writeConfig(t, "edition = 2\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "edition must be a string")
	})

	t.Run("nil config", func(t *testing.T) {
		var config *ProjectConfig
		assert.Equal(t, DefaultEdition, config.EffectiveEdition())
	})
}

func TestEditionFromContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, LatestEdition, EditionFromContext(ctx))

	// An embedder's edition applies to source no dang.toml governs.
	hostCtx := ContextWithEdition(ctx, Edition1)
	assert.Equal(t, Edition1, EditionFromContext(hostCtx))

	// A project config on the context does not decide the edition: only
	// the source's path does (see TestEditionForDir).
	assert.Equal(t, LatestEdition, EditionFromContext(
		ContextWithProjectConfig(ctx, "dang.toml", &ProjectConfig{Edition: Edition1})))
}

func TestEditionForDir(t *testing.T) {
	ctx := context.Background()
	hostCtx := ContextWithEdition(ctx, Edition1)

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	mkdir := func(rel string) string {
		dir := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(dir, 0755))
		return dir
	}
	write := func(rel, contents string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte(contents), 0644))
	}

	edition1 := mkdir("one/nested")
	write("one/dang.toml", "edition = \"1\"\n")
	implicit := mkdir("implicit")
	write("implicit/dang.toml", "[imports.Dagger]\ndagger = true\n")
	ungoverned := mkdir("ungoverned")
	broken := mkdir("broken")
	write("broken/dang.toml", "edition = 2\n")

	for _, tc := range []struct {
		name string
		ctx  context.Context
		dir  string
		want Edition
	}{
		{"nearest dang.toml, walking up", ctx, edition1, Edition1},
		{"dang.toml beats the embedder", hostCtx, filepath.Join(root, "one"), Edition1},
		{"dang.toml without edition", hostCtx, implicit, DefaultEdition},
		{"boundary reached: latest", ctx, ungoverned, LatestEdition},
		{"boundary reached: embedder", hostCtx, ungoverned, Edition1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edition, err := EditionForDir(tc.ctx, tc.dir)
			require.NoError(t, err)
			assert.Equal(t, tc.want, edition)
		})
	}

	t.Run("broken dang.toml", func(t *testing.T) {
		edition, err := EditionForDir(hostCtx, broken)
		require.Error(t, err)
		assert.Equal(t, Edition1, edition)
	})
}
