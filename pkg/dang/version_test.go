package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
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
			{"an object literal field", "let o = {{loop: 1, x: for {\n  break 2\n}}}\n", true},
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

	t.Run("Dang 2.0 try/catch stays where rescue would lose something", func(t *testing.T) {
		for _, src := range []string{
			// Dead clauses, which rescue rejects.
			"let x = try {\n  boom\n} catch {\n  e: Error => 1\n  err => 2\n}\n",
			"let x = try {\n  boom\n} catch {\n  e: Foo => 1\n  f: Foo => 2\n}\n",
			"let x = try {\n  boom\n} catch {\n  err => 1\n  e: Foo => 2\n}\n",
			// A comment in a catch without clauses.
			"let x = try {\n  boom\n} catch {\n  # nothing to do\n}\n",
		} {
			assertMigratesTo(t, src, src)
		}
	})

	t.Run("Dang 2.0 try/catch comments survive migrating", func(t *testing.T) {
		assertMigratesTo(t,
			"let x = try {\n  boom # inner\n} catch {\n  # ignore\n  err => \"\" # really\n}\n",
			"let x = {\n  boom # inner\n} rescue {\n  # ignore\n  err: Error => \"\" # really\n}\n",
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

// TestVersionTypePatterns checks that a type pattern may leave out its `!`
// before 2.4 only, where it means the same, and that migrating adds it.
func TestVersionTypePatterns(t *testing.T) {
	bare := "let x = case (p) {\n  c: Cat => 1\n  g: Lib.Dog => 2\n  xs: [Int!] => 3\n  m: Map[Int!] => 4\n}\n"
	banged := "let x = case (p) {\n  c: Cat! => 1\n  g: Lib.Dog! => 2\n  xs: [Int!]! => 3\n  m: Map[Int!]! => 4\n}\n"
	mixed := "let x = case (p) {\n  c: Cat => 1\n  g: Lib.Dog! => 2\n  xs: [Decoded!]! => 3\n  m: Map[Int!] => 4\n}\n"

	clauses := func(t *testing.T, src string, v Version) []*CaseClause {
		t.Helper()
		node := parseSingleForm(t, src, v)
		decl, ok := node.(*FieldDecl)
		require.True(t, ok, "expected *FieldDecl, got %T", node)
		c, ok := decl.Value.(*Case)
		require.True(t, ok, "expected *Case, got %T", decl.Value)
		return c.Clauses
	}

	for _, v := range []Version{Version2_1} {
		t.Run("Dang "+v.String()+" type patterns may leave out the !", func(t *testing.T) {
			cs := clauses(t, bare, v)
			require.Len(t, cs, 4)
			assert.Equal(t, "Cat", cs[0].TypePattern.Name)
			assert.Equal(t, "Lib.Dog", cs[1].PatternName())
			assert.Equal(t, "[Int!]", cs[2].PatternName())
			assert.Equal(t, "Map[Int!]", cs[3].PatternName())
			for _, c := range cs {
				assert.False(t, c.BarePattern)
			}

			// Both spellings parse into the same AST.
			withBang, err := Parse("version_test.dang", []byte(banged), WithVersion(v))
			require.NoError(t, err)
			without, err := Parse("version_test.dang", []byte(bare), WithVersion(v))
			require.NoError(t, err)
			_, differs := firstASTDifference(without.(Node), withBang.(Node))
			assert.False(t, differs)

			// Formatting keeps each pattern as written, `!` or not.
			for _, src := range []string{bare, banged, mixed} {
				formatted, err := FormatFileWithVersion([]byte(src), v)
				require.NoError(t, err)
				assert.Equal(t, src, formatted)
			}
		})
	}

	t.Run("Dang 2.4 marks type patterns without the !", func(t *testing.T) {
		for _, c := range clauses(t, bare, Version2_4) {
			assert.True(t, c.BarePattern, c.PatternName())
		}
		for _, c := range clauses(t, banged, Version2_4) {
			assert.False(t, c.BarePattern, c.PatternName())
		}
		formatted, err := FormatFileWithVersion([]byte(banged), Version2_4)
		require.NoError(t, err)
		assert.Equal(t, banged, formatted)
	})

	t.Run("Dang 2.4 rejects a type pattern without the !", func(t *testing.T) {
		for _, tc := range []struct{ src, want string }{
			{"union U = A | B\ntype A { a: Int! = 1 }\ntype B { b: Int! = 2 }\nlet u: U! = A()\nlet x = case (u) {\n  a: A => 1\n  else => 2\n}\n", "write `a: A! =>`"},
			{"let x = [1]\nlet y = case (x) {\n  xs: [Int!] => 1\n}\n", "write `xs: [Int!]! =>`"},
			{"let x = 1 rescue {\n  e: Error => 2\n}\n", "write `e: Error! =>`"},
		} {
			parsed, err := Parse("version_test.dang", []byte(tc.src), WithVersion(Version2_4))
			require.NoError(t, err)
			typeScope, _ := BuildScopesFromImports("", nil)
			_, err = InferFormsWithPhases(context.Background(), parsed.(*FileBlock).Forms, typeScope, hm.NewSimpleFresher())
			require.ErrorContains(t, err, tc.want, tc.src)
		}
	})

	t.Run("migrating to 2.4 adds the !", func(t *testing.T) {
		migrated, err := MigrateSource("version_test.dang", []byte(bare), Version2_1, Version2_4)
		require.NoError(t, err)
		assert.Equal(t, banged, migrated)

		migrated, err = MigrateSource("version_test.dang", []byte(mixed), Version2_1, Version2_4)
		require.NoError(t, err)
		assert.Equal(t, "let x = case (p) {\n  c: Cat! => 1\n  g: Lib.Dog! => 2\n  xs: [Decoded!]! => 3\n  m: Map[Int!]! => 4\n}\n", migrated)

		// Source already spelled the 2.4 way is left as it is.
		migrated, err = MigrateSource("version_test.dang", []byte(banged), Version2_1, Version2_4)
		require.NoError(t, err)
		assert.Equal(t, banged, migrated)

		migrated, err = MigrateSource("version_test.dang", []byte("let x = boom rescue {\n  e: Error => 1\n  else => 2\n}\n"), Version2_1, Version2_4)
		require.NoError(t, err)
		assert.Equal(t, "let x = boom rescue {\n  e: Error! => 1\n  else => 2\n}\n", migrated)

		// A try/catch, deprecated since 2.1, becomes postfix rescue.
		migrated, err = MigrateSource("version_test.dang", []byte("let x = try {\n  boom\n} catch {\n  e: Error => e.message\n}\n"), Version2_1, Version2_4)
		require.NoError(t, err)
		assert.Equal(t, "let x = boom rescue {\n  e: Error! => e.message\n}\n", migrated)
	})
}

// TestVersionQuotes checks that `"..."` and `"""..."""` take `${` literally
// before 2.5, where `"""` is also raw, and process escapes and interpolate
// from 2.5 on; that the opening line of a multi-line string holds at most a
// language tag from 2.5 on; and that migrating keeps every string's value.
func TestVersionQuotes(t *testing.T) {
	str := func(t *testing.T, src string, v Version) *String {
		t.Helper()
		node := parseSingleForm(t, src, v)
		s, ok := node.(*String)
		require.True(t, ok, "expected *String, got %T", node)
		return s
	}
	parseErr := func(t *testing.T, src string, v Version) string {
		t.Helper()
		_, err := Parse("version_test.dang", []byte(src), WithVersion(v))
		require.Error(t, err)
		return err.Error()
	}

	t.Run("Dang 2.4 double quotes take ${ literally", func(t *testing.T) {
		assert.Equal(t, "${x} \t", str(t, `"${x} \t"`, Version2_4).Value)
		assert.Equal(t, `a\tb ${x}`, str(t, "\"\"\"\n  a\\tb ${x}\n\"\"\"", Version2_4).Value)
		assert.Equal(t, "sql\nselect", str(t, "\"\"\"sql\nselect\n\"\"\"", Version2_4).Value)
		assert.Equal(t, "hello world\nmore", parseSingleForm(t, "```hello world\nmore\n```", Version2_4).(*Template).Parts[0].Lit)
	})

	t.Run("Dang 2.5 double quotes process escapes and interpolate", func(t *testing.T) {
		tmpl, ok := parseSingleForm(t, `"a\t${x}\${y}"`, Version2_5).(*Template)
		require.True(t, ok)
		assert.Equal(t, byte('"'), tmpl.Quote)
		require.Len(t, tmpl.Parts, 3)
		assert.Equal(t, "a\t", tmpl.Parts[0].Lit)
		assert.Equal(t, "${y}", tmpl.Parts[2].Lit)

		assert.Equal(t, "${y} $z", str(t, `"\${y} $z"`, Version2_5).Value)

		// Escapes are text, not indentation: dedenting comes first.
		s := str(t, "\"\"\"sql\n  \\tselect \\\"\"\"\n    1\n\"\"\"", Version2_5)
		assert.Equal(t, "\tselect \"\"\"\n  1", s.Value)
		assert.Equal(t, "sql", s.Lang)

		assert.Equal(t, "inline", str(t, `"""inline"""`, Version2_5).Value)
	})

	t.Run("single quotes are verbatim", func(t *testing.T) {
		for _, v := range []Version{Version2_4, Version2_5} {
			assert.Equal(t, `${x}\t`, str(t, `'${x}\t'`, v).Value)
			assert.Equal(t, "", str(t, `''`, v).Value)
			s := str(t, "'''sh\n  echo \"${HOME}\" \\\n    it's\n'''", v)
			assert.Equal(t, "echo \"${HOME}\" \\\n  it's", s.Value)
			assert.Equal(t, "sh", s.Lang)
			assert.Equal(t, "it's", str(t, `'''it's'''`, v).Value)
		}
	})

	t.Run("Dang 2.5 rejects text on the opening line", func(t *testing.T) {
		for _, src := range []string{
			"let x = \"\"\"Summary.\nmore\n\"\"\"",
			"let x = \"\"\"${x}\nmore\n\"\"\"",
			"'''sh echo\nmore\n'''",
			"```hello world\nmore\n```",
		} {
			assert.Contains(t, parseErr(t, src, Version2_5), "text on the opening line of a multi-line string")
		}
	})

	t.Run("docstrings stay verbatim", func(t *testing.T) {
		node := parseSingleForm(t, "\"\"\"Summary \\n ${x}\nmore\n\"\"\"\nlet a = 1\n", Version2_5)
		decl, ok := node.(*FieldDecl)
		require.True(t, ok, "expected *FieldDecl, got %T", node)
		assert.Equal(t, "Summary \\n ${x}\nmore", decl.DocString)
	})

	t.Run("a declaration's error isn't blamed on its docstring", func(t *testing.T) {
		for _, doc := range []string{"\"\"\"Summary.\nmore\n\"\"\"", "\"\"\"\nSummary \\d.\n\"\"\""} {
			msg := parseErr(t, doc+"\npub f(x: Int!): Int! { x + }\n", Version2_5)
			assert.NotContains(t, msg, "opening line", doc)
			assert.NotContains(t, msg, "invalid escape", doc)
		}
	})

	t.Run("whitespace may trail a language tag", func(t *testing.T) {
		s := str(t, "\"\"\"sql  \r\n  select 1\n\"\"\"", Version2_5)
		assert.Equal(t, "sql", s.Lang)
		assert.Equal(t, "select 1", s.Value)
	})

	t.Run("migrating to 2.5 keeps every string's value", func(t *testing.T) {
		for _, tc := range []struct{ src, want string }{
			{"let a = \"${HOME}/bin\"\n", "let a = '${HOME}/bin'\n"},
			{"let a = \"it's ${HOME}\\n\"\n", "let a = \"it's \\${HOME}\\n\"\n"},
			{"let a = \"plain\\tstring\"\n", "let a = \"plain\\tstring\"\n"},
			{"let a = \"\"\"a\\b\"\"\"\n", "let a = '''a\\b'''\n"},
			{"let a = \"\"\"a'\\b\"\"\"\n", "let a = '''a'\\b'''\n"},
			{"let a = \"\"\"a\\b'\"\"\"\n", "let a = \"\"\"a\\\\b'\"\"\"\n"},
			{"let a = \"\"\"\n  C:\\path ${x}\n\"\"\"\n", "let a = '''\n  C:\\path ${x}\n'''\n"},
			{"let a = \"\"\"\n  C:\\path'\"\"\"\n", "let a = \"\"\"\nC:\\\\path'\n\"\"\"\n"},
			{"let a = \"\"\"\n  ''' \\\n\"\"\"\n", "let a = \"\"\"\n''' \\\\\n\"\"\"\n"},
			{"let a = \"\"\"Summary.\n  more\n\"\"\"\n", "let a = \"\"\"\nSummary.\n  more\n\"\"\"\n"},
			{"let a = \"\"\"sql\nselect\n\"\"\"\n", "let a = \"\"\"\nsql\nselect\n\"\"\"\n"},
			{"let a = ```hello ${1}\n  more\n```\n", "let a = ```\nhello ${1}\n  more\n```\n"},
			{"let a = \"\"\"\n  no escapes\n\"\"\"\n", "let a = \"\"\"\n  no escapes\n\"\"\"\n"},
		} {
			migrated, err := MigrateSource("version_test.dang", []byte(tc.src), Version2_4, Version2_5)
			require.NoError(t, err, tc.src)
			assert.Equal(t, tc.want, migrated, tc.src)
		}
	})
}

// versions lists every Dang version.
var versions = []Version{Version1_0, Version2_0, Version2_1, Version2_4, Version2_5}

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
				"let y = $" + word + "(1)\nlet z = [1].map { $" + word + " => $" + word + " }\n" +
				"let w = $" + word + " - 1\nlet c = case (1) {\n  $" + word + " => 2\n}\n" +
				"let d = case (1) {\n  $" + word + ": Int! => 2\n}\n"
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
