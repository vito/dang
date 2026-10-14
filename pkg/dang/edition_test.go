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
	assert.Equal(t, DefaultEdition, EditionFromContext(ctx))

	// An embedder's edition applies when no dang.toml governs the source.
	hostCtx := ContextWithEdition(ctx, Edition1)
	assert.Equal(t, Edition1, EditionFromContext(hostCtx))

	// A dang.toml takes precedence, including its implicit default.
	assert.Equal(t, DefaultEdition, EditionFromContext(
		ContextWithProjectConfig(hostCtx, "dang.toml", &ProjectConfig{})))
	assert.Equal(t, Edition1, EditionFromContext(
		ContextWithProjectConfig(ctx, "dang.toml", &ProjectConfig{Edition: Edition1})))
}
