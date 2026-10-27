package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	write := func(name, contents string) string {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
		return path
	}
	migrate := func(args ...string) (string, error) {
		cmd := migrateCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}

	const oldSource = "for { break }\n"

	t.Run("prints the migration of a file in a declared project", func(t *testing.T) {
		write("declared/dang.toml", "dang = \"2.0\"\n")
		file := write("declared/main.dang", oldSource)
		out, err := migrate("--to", "2.1", file)
		require.NoError(t, err)
		assert.Equal(t, "loop { break }\n", out)

		src, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, oldSource, string(src), "a preview writes nothing")
	})

	t.Run("--from applies to a file whose version is undeclared", func(t *testing.T) {
		write("keyless/dang.toml", "")
		file := write("keyless/main.dang", oldSource)
		out, err := migrate("--from", "2.0", "--to", "2.1", file)
		require.NoError(t, err)
		assert.Equal(t, "loop { break }\n", out)
	})

	t.Run("--from doesn't override a declared version", func(t *testing.T) {
		file := write("current/main.dang", "loop { break }\n")
		write("current/dang.toml", "dang = \"2.1\"\n")
		out, err := migrate("--from", "2.0", "--to", "2.1", file)
		require.NoError(t, err)
		assert.Equal(t, "loop { break }\n", out)
	})

	t.Run("-w refuses a file", func(t *testing.T) {
		write("refused/dang.toml", "dang = \"2.0\"\n")
		file := write("refused/main.dang", oldSource)
		_, err := migrate("-w", "--to", "2.1", file)
		require.ErrorContains(t, err, "migrate -w moves whole projects")

		src, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Equal(t, oldSource, string(src))
	})
}
