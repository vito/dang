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

func TestVersionFragmentBraces(t *testing.T) {
	for _, tc := range []struct {
		version Version
		src     string
	}{
		{Version1_0, `node.{... on User {name}}`},
		{Version2_0, `node.{{... on User {name}}}`},
		{Version2_1, `node.{{... on User {{name}}}}`},
	} {
		t.Run(tc.version.String(), func(t *testing.T) {
			node := parseSingleForm(t, tc.src, tc.version)
			sel, ok := node.(*ObjectSelection)
			require.True(t, ok, "expected *ObjectSelection, got %T", node)
			require.Len(t, sel.InlineFragments, 1)
			assert.Equal(t, "name", sel.InlineFragments[0].Fields[0].Name)
		})
	}

	t.Run("migrating rewrites each brace in its own version", func(t *testing.T) {
		src := "let x = node.{... on User {name}}\n"
		migrated, err := MigrateSource("version_test.dang", []byte(src), Version1_0, Version2_0)
		require.NoError(t, err)
		assert.Equal(t, "let x = node.{{... on User { name }}}\n", migrated)

		migrated, err = MigrateSource("version_test.dang", []byte(migrated), Version2_0, Version2_1)
		require.NoError(t, err)
		assert.Equal(t, "let x = node.{{... on User {{ name }}}}\n", migrated)
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
		assert.Equal(t, forLoopBuiltin, call.Fun.(*Symbol).Name)
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

	t.Run("Dang 2.0 for loops migrate unless they would call a loop of the program's own", func(t *testing.T) {
		loopy := "count(): Int! {\n  for {\n    break 1\n  }\n}\n"
		for _, tc := range []struct {
			name, src string
			collides  bool
		}{
			{"no loop", loopy, false},
			{"a top-level loop", "pub loop(n: Int!): Int! {\n  n\n}\n" + loopy, true},
			{"a top-level loop declared after", loopy + "pub loop: Int! = 1\n", true},
			{"a local loop around it", "count(): Int! {\n  let loop = 1\n  for {\n    break loop\n  }\n}\n", true},
			{"a loop parameter", "count(loop: Int!): Int! {\n  for {\n    break loop\n  }\n}\n", true},
			{"a block parameter", "let xs = [1].map { loop =>\n  for {\n    break loop\n  }\n}\n", true},
			{"a constructor parameter", "type Foo {\n  pub x: Int!\n  new(loop: Int!) {\n    for {\n      break\n    }\n    self.x = loop\n    self\n  }\n}\n", true},
			{"a loop parameter of another function", loopy + "other(loop: Int!): Int! {\n  loop\n}\n", false},
			{"a local loop elsewhere", loopy + "other(): Int! {\n  let loop = 1\n  loop\n}\n", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := MigrateSource("version_test.dang", []byte(tc.src), Version2_0, Version2_1)
				if tc.collides {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "`for` loop becomes a call to `loop`")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("Dang 2.0 for loops loop where the program binds loop", func(t *testing.T) {
		src := "pub loop(n: Int!): Int! { n }\n" +
			"count(): Int! {\n  let i = 0\n  # count to 3\n  for (i < 3) {\n    i += 1 # step\n  }\n  for {\n    break\n  }\n  i\n}\n" +
			"assert { count() == 3 }\nassert { loop(2) == 2 }\n"
		run := func(src string) {
			t.Helper()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(src), 0o600))
			_, err := RunDir(ContextWithVersion(context.Background(), Version2_0), dir, false)
			require.NoError(t, err)
		}
		run(src)

		// Formatting keeps them `for` loops, which the program's own
		// `loop` can't capture, as a `loop` call would be.
		formatted, err := FormatFileWithVersion([]byte(src), Version2_0)
		require.NoError(t, err)
		assert.Contains(t, formatted, "  # count to 3\n  for (i < 3) {\n    i += 1 # step\n  }\n  for {\n    break\n  }\n")
		run(formatted)
		again, err := FormatFileWithVersion([]byte(formatted), Version2_0)
		require.NoError(t, err)
		assert.Equal(t, formatted, again)
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

func TestVersionRescue(t *testing.T) {
	for _, version := range []Version{Version1_0, Version2_0} {
		t.Run("Dang "+version.String()+" rescue is an identifier", func(t *testing.T) {
			for _, src := range []string{
				"let rescue = 1\n",
				"pub rescue: Int! = 1\n",
				"f(rescue: Int!): Int! { rescue }\n",
				"let x = f(rescue: 1)\n",
				"let x = { rescue }\n",
			} {
				_, err := Parse("version_test.dang", []byte(src), WithVersion(version))
				assert.NoError(t, err, "%q", src)
			}
		})

		t.Run("Dang "+version.String()+" has no postfix rescue", func(t *testing.T) {
			_, err := Parse("version_test.dang", []byte("let x = boom rescue 1\n"), WithVersion(version))
			require.Error(t, err)

			// `rescue` on the next line starts a form of its own, rather
			// than rescuing the line before.
			parsed, err := Parse("version_test.dang", []byte("let x = boom\nrescue(1)\n"), WithVersion(version))
			require.NoError(t, err)
			require.Len(t, parsed.(*FileBlock).Forms, 2)
		})

		t.Run("Dang "+version.String()+" try/catch is current", func(t *testing.T) {
			node := parseSingleForm(t, "try { boom } catch { err => 1 }", version)
			r, ok := node.(*RescueExpr)
			require.True(t, ok, "expected *RescueExpr, got %T", node)
			assert.True(t, r.TryCatch)
			assert.False(t, r.Legacy)

			src := "let x = try {\n  boom\n} catch {\n  err => 1\n  e: Error => 2\n}\n"
			formatted, err := FormatFileWithVersion([]byte(src), version)
			require.NoError(t, err)
			assert.Equal(t, src, formatted)
		})
	}

	t.Run("Dang 2.1 try/catch is legacy", func(t *testing.T) {
		node := parseSingleForm(t, "try { boom } catch { err => 1 }", Version2_1)
		r, ok := node.(*RescueExpr)
		require.True(t, ok, "expected *RescueExpr, got %T", node)
		assert.True(t, r.TryCatch)
		assert.True(t, r.Legacy)
	})

	t.Run("Dang 2.0 try/catch migrates to rescue", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = try {\n  boom\n} catch {\n  e: Error => 1\n}\n",
			"let x = boom rescue {\n  e: Error => 1\n}\n",
		)
		assertMigratesTo(t,
			"let x = try {\n  boom\n} catch {\n  err => 1\n}\n",
			"let x = boom rescue 1\n",
		)
	})

	t.Run("Dang 2.1 reserves rescue", func(t *testing.T) {
		_, err := Parse("version_test.dang", []byte("let rescue = 1\n"), WithVersion(Version2_1))
		require.Error(t, err)
	})

	t.Run("Dang 2.1 rescue is a name where only a name can go", func(t *testing.T) {
		for _, src := range []string{
			"let x = f(rescue: 1)\n",
			"let x = x.rescue\n",
			"let x = x.rescue(1)\n",
			"let x = x.{{rescue}}\n",
			"let x = x.{{rescue: name, other: rescue}}\n",
			"let x = {{rescue: 1}}\n",
			"let x: {{rescue: Int!}} = y\n",
		} {
			_, err := Parse("version_test.dang", []byte(src), WithVersion(Version2_1))
			assert.NoError(t, err, "%q", src)
		}
	})
}

// versions lists every Dang version.
var versions = []Version{Version1_0, Version2_0, Version2_1}

// TestReservedWords checks every reserved word in every version: it is an
// identifier exactly where it isn't reserved, `$word` is the identifier in
// every version, it is a name wherever only a name can go (but for null,
// which is a value), and the formatter escapes it exactly where needed.
func TestReservedWords(t *testing.T) {
	require.Contains(t, versions, CurrentVersion, "list the new version in versions")
	for word := range reservedWords {
		for _, v := range versions {
			t.Run(word+" in "+v.String(), func(t *testing.T) {
				parses := func(src string) bool {
					_, err := Parse("version_test.dang", []byte(src), WithVersion(v))
					return err == nil
				}
				reserved := v.reserves(word)

				for _, src := range []string{
					"let " + word + " = 1\n",
					"pub " + word + ": Int! = 1\n",
					"f(" + word + ": Int!): Int! { " + word + " }\n",
					"let x = [1].map { " + word + " => " + word + " }\n",
				} {
					assert.Equal(t, !reserved, parses(src), "%q", src)
				}

				escaped := parseSingleForm(t, "let $"+word+" = $"+word+"\n", v)
				decl, ok := escaped.(*FieldDecl)
				require.True(t, ok, "expected *FieldDecl, got %T", escaped)
				assert.Equal(t, word, decl.Name.Name)
				assert.Equal(t, word, decl.Value.(*Symbol).Name)

				isName := word != "null"
				openBrace, closeBrace := v.selectionBraces()
				for _, src := range []string{
					"let x = f(" + word + ": 1)\n",
					"let x = x." + word + "\n",
					"let x = x." + openBrace + word + ": a" + closeBrace + "\n",
					"let x = {{" + word + ": 1}}\n",
				} {
					assert.Equal(t, isName || !reserved, parses(src), "%q", src)
				}

				formatted, err := FormatFileWithVersion([]byte("let $"+word+" = f($"+word+": x.$"+word+")\n"), v)
				require.NoError(t, err)
				spelled, field := word, word
				if reserved {
					spelled = "$" + word
				}
				if !isName {
					field = "$" + word
				}
				assert.Equal(t, "let "+spelled+" = f("+field+": x."+field+")\n", formatted)
			})
		}
	}
}

// TestKeywordNamesRoundTrip checks that formatting keeps a name spelled like
// a keyword a name, declared and referred to, in every version.
func TestKeywordNamesRoundTrip(t *testing.T) {
	// Every keyword token's word (see the *Token rules of dang.peg).
	words := []string{
		"and", "break", "case", "catch", "continue", "directive", "else",
		"enum", "false", "for", "if", "implements", "import", "interface",
		"let", "new", "null", "on", "or", "pub", "raise", "rescue", "return",
		"scalar", "self", "true", "try", "type", "union",
	}
	for _, v := range versions {
		for _, word := range words {
			src := "pub $" + word + "($" + word + ": Int!): Int! {\n  $" + word + "\n}\n" +
				"let y = $" + word + "(1)\nlet z = [1].map { $" + word + " => $" + word + " }\n"
			want, err := Parse("version_test.dang", []byte(src), WithVersion(v))
			require.NoError(t, err, "%s in %s", word, v)
			formatted, err := FormatFileWithVersion([]byte(src), v)
			require.NoError(t, err, "%s in %s", word, v)
			got, err := Parse("version_test.dang", []byte(formatted), WithVersion(v))
			if !assert.NoError(t, err, "%s in %s:\n%s", word, v, formatted) {
				continue
			}
			_, differs := firstASTDifference(want.(Node), got.(Node))
			assert.False(t, differs, "%s in %s:\n%s", word, v, formatted)
		}
	}
}

// TestReservedWordMigration checks that migrating to a version that reserves
// a word escapes the identifiers spelled like it.
func TestReservedWordMigration(t *testing.T) {
	for word, since := range reservedWords {
		for _, from := range versions {
			if !from.Before(since) {
				continue
			}
			t.Run(word+" from "+from.String(), func(t *testing.T) {
				src := "pub " + word + "(" + word + ": Int!): Int! {\n  " + word + "\n}\n"
				migrated, err := MigrateSource("version_test.dang", []byte(src), from, since)
				require.NoError(t, err)
				assert.Equal(t, "$"+word+"($"+word+": Int!): Int! {\n  $"+word+"\n}\n", migrated)
			})
		}
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dang.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	return path
}
