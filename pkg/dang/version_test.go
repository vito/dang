package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseSingleForm parses src as the given Dang version and returns its only
// form.
func parseSingleForm(t *testing.T, src string, version Version) Node {
	t.Helper()
	parsed, err := Parse("version_test.dang", []byte(src), WithVersion(version))
	require.NoError(t, err)
	block := parsed.(*FileBlock)
	require.Len(t, block.Forms, 1)
	return block.Forms[0]
}

func TestVersionSelectionSyntax(t *testing.T) {
	t.Run("Dang 1.0 single braces are object selection", func(t *testing.T) {
		node := parseSingleForm(t, `user.{name, posts.{title}}`, Version1_0)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.Fields, 2)
		assert.Equal(t, "name", sel.Fields[0].Name)
		assert.Equal(t, "posts", sel.Fields[1].Name)
		require.NotNil(t, sel.Fields[1].Selection)
		assert.Equal(t, "title", sel.Fields[1].Selection.Fields[0].Name)
	})

	t.Run("Dang 1.0 inline fragments use single braces", func(t *testing.T) {
		node := parseSingleForm(t, `node.{... on User {name}, ... on Post {title}}`, Version1_0)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.InlineFragments, 2)
		assert.Equal(t, "User", sel.InlineFragments[0].TypeName.Name)
		assert.Equal(t, "name", sel.InlineFragments[0].Fields[0].Name)
	})

	t.Run("Dang 1.0 rejects double-brace selection", func(t *testing.T) {
		_, err := Parse("version_test.dang", []byte(`user.{{name}}`), WithVersion(Version1_0))
		require.Error(t, err)
	})

	t.Run("Dang 2.0 double braces are object selection", func(t *testing.T) {
		node := parseSingleForm(t, `user.{{name, posts.{{title}}}}`, Version2_0)
		sel, ok := node.(*ObjectSelection)
		require.True(t, ok, "expected *ObjectSelection, got %T", node)
		require.Len(t, sel.Fields, 2)
	})

	t.Run("Dang 2.0 single braces are dot-block application", func(t *testing.T) {
		node := parseSingleForm(t, `41.{ inc(_) }`, Version2_0)
		_, ok := node.(*DotApply)
		require.True(t, ok, "expected *DotApply, got %T", node)
	})

	t.Run("no version option parses as the current version", func(t *testing.T) {
		parsed, err := Parse("version_test.dang", []byte(`41.{ inc(_) }`))
		require.NoError(t, err)
		_, ok := parsed.(*FileBlock).Forms[0].(*DotApply)
		require.True(t, ok, "the current version should have dot-blocks")
	})
}

func TestVersionFormatting(t *testing.T) {
	src := "let x = user.{name, posts.{title}}\nlet y = node.{... on User {name}}\n"

	formatted, err := FormatFileWithVersion([]byte(src), Version1_0)
	require.NoError(t, err)
	assert.Equal(t, "let x = user.{name, posts.{title}}\nlet y = node.{... on User { name }}\n", formatted)

	// Formatting is idempotent, and the output still parses as 1.0.
	again, err := FormatFileWithVersion([]byte(formatted), Version1_0)
	require.NoError(t, err)
	assert.Equal(t, formatted, again)

	// Current formatting is unchanged.
	formatted2, err := FormatFile([]byte("let x = user.{{name}}\n"))
	require.NoError(t, err)
	assert.Equal(t, "let x = user.{{name}}\n", formatted2)
}

// assertMigratesTo checks that Dang 2.0 source migrates to want in 2.1, which
// proves the 2.0 form parses into the same AST as the 2.1 one.
func assertMigratesTo(t *testing.T, src, want string) {
	t.Helper()
	migrated, err := MigrateSource("version_test.dang", []byte(src), Version2_0, Version2_1)
	require.NoError(t, err)
	assert.Equal(t, want, migrated)
}

func TestVersionForLoops(t *testing.T) {
	t.Run("Dang 2.0 condition loop is a guarded loop", func(t *testing.T) {
		assertMigratesTo(t,
			"for (i < 3) {\n  i += 1\n}\n",
			"loop {\n  if (i < 3) {\n    i += 1\n  } else {\n    break\n  }\n}\n",
		)
	})

	t.Run("Dang 2.0 infinite loop is a loop", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = for {\n  break 42\n}\n",
			"let x = loop {\n  break 42\n}\n",
		)
	})

	t.Run("Dang 2.0 loop comments stay in place", func(t *testing.T) {
		assertMigratesTo(t,
			"# before\nfor (i < 3) {\n  # body\n  i += 1 # step\n}\nlet y = 2\n",
			"# before\nloop {\n  if (i < 3) {\n    # body\n    i += 1 # step\n  } else {\n    break\n  }\n}\nlet y = 2\n",
		)
	})

	t.Run("Dang 1.0 has the loops too", func(t *testing.T) {
		node := parseSingleForm(t, `for { 1 }`, Version1_0)
		call, ok := node.(*FunCall)
		require.True(t, ok, "expected *FunCall, got %T", node)
		assert.Equal(t, "loop", call.Fun.(*Symbol).Name)
	})

	t.Run("Dang 2.1 for is an identifier", func(t *testing.T) {
		node := parseSingleForm(t, `for { 1 }`, Version2_1)
		call, ok := node.(*FunCall)
		require.True(t, ok, "expected *FunCall, got %T", node)
		assert.Equal(t, "for", call.Fun.(*Symbol).Name)

		_, err := Parse("version_test.dang", []byte(`for (true) { 1 }`), WithVersion(Version2_1))
		require.Error(t, err)
	})

	t.Run("Dang 2.0 for remains an identifier elsewhere", func(t *testing.T) {
		node := parseSingleForm(t, `for + 1`, Version2_0)
		_, ok := node.(*Addition)
		require.True(t, ok, "expected *Addition, got %T", node)
	})
}

func TestVersionCommaFormSep(t *testing.T) {
	t.Run("Dang 2.0 commas separate block forms", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = { let a = 1, a }\n",
			"let x = { let a = 1; a }\n",
		)
	})

	t.Run("Dang 2.0 commas separate block argument forms", func(t *testing.T) {
		assertMigratesTo(t,
			"[1].map { x => let y = x, y }\n",
			"[1].map { x =>\n  let y = x\n  y\n}\n",
		)
	})

	t.Run("Dang 2.0 commas separate top-level forms", func(t *testing.T) {
		assertMigratesTo(t, "let a = 1, let b = 2\n", "let a = 1\nlet b = 2\n")
	})

	t.Run("Dang 2.1 rejects commas between forms", func(t *testing.T) {
		for _, src := range []string{
			"let x = { let a = 1, a }\n",
			"[1].map { x => let y = x, y }\n",
			"let a = 1, let b = 2\n",
		} {
			_, err := Parse("version_test.dang", []byte(src), WithVersion(Version2_1))
			assert.Error(t, err, "%q", src)
		}
	})
}

func TestVersionUnmatchedCase(t *testing.T) {
	t.Run("Dang 2.0 case without else raises when nothing matches", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\"\n  2 => \"two\"\n}\n",
			"let x = case (n) {\n  1 => \"one\"\n  2 => \"two\"\n  else => raise \"no case clause matched\"\n}\n",
		)
	})

	t.Run("Dang 2.0 case with else is unchanged", func(t *testing.T) {
		src := "let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n}\n"
		assertMigratesTo(t, src, src)
	})

	t.Run("Dang 2.0 synthetic else keeps comments in place", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  # first\n  1 => \"one\" # one\n  2 => \"two\" # two\n  # end\n}\nlet y = 2\n",
			"let x = case (n) {\n  # first\n  1 => \"one\" # one\n  2 => \"two\" # two\n  else => raise \"no case clause matched\"\n  # end\n}\nlet y = 2\n",
		)
	})

	t.Run("Dang 2.0 unmatched case is non-null and raises", func(t *testing.T) {
		dir := t.TempDir()
		src := "let f(n: Int!): String! { case (n) { 1 => \"one\" } }\nassert { f(1) == \"one\" }\nf(2)\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(src), 0o600))
		_, err := RunDir(ContextWithVersion(context.Background(), Version2_0), dir, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no case clause matched")
	})

	t.Run("Dang 2.1 case without else is unchanged", func(t *testing.T) {
		node := parseSingleForm(t, "case (n) { 1 => \"one\" }", Version2_1)
		c, ok := node.(*Case)
		require.True(t, ok, "expected *Case, got %T", node)
		require.Len(t, c.Clauses, 1)
	})
}

func TestVersionClausesAfterElse(t *testing.T) {
	t.Run("Dang 2.0 drops clauses after else", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n  5 => \"five\"\n}\n",
			"let x = case (n) {\n  1 => \"one\"\n  else => \"many\"\n}\n",
		)
	})

	t.Run("Dang 2.0 keeps standalone comments of dropped clauses", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = case (n) {\n  1 => \"one\" # one\n  else => \"many\" # many\n  # five is special\n  5 => \"five\"\n}\nlet y = 2\n",
			"let x = case (n) {\n  1 => \"one\" # one\n  else => \"many\" # many\n  # five is special\n}\nlet y = 2\n",
		)
	})

	t.Run("Dang 2.1 keeps clauses after else", func(t *testing.T) {
		node := parseSingleForm(t, "case (n) { else => \"many\", 5 => \"five\" }", Version2_1)
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
