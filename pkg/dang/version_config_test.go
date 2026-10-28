package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		in, want, err string
	}{
		{in: "1.0", want: "1.0"},
		{in: "2.0", want: "2.0"},
		{in: CurrentVersion.String(), want: CurrentVersion.String()},
		// A leading v, a patch and prerelease or build suffixes are
		// accepted; a Dang version is major.minor, so the patch and the
		// prerelease are dropped.
		{in: "v2.0", want: "2.0"},
		{in: "2.0.1", want: "2.0"},
		{in: "v2.1.4", want: "2.1"},
		{in: "2.1.0-rc.1+abc", want: "2.1"},
		{in: "2", err: "want major.minor"},
		{in: "2.0.", err: "want major.minor"},
		{in: "02.0", err: "want major.minor"},
		{in: "V2.0", err: "want major.minor"},
		{in: "2.0.0-", err: "want major.minor"},
		{in: "0.9", err: "the oldest supported version is 1.0"},
		{in: "0.0", err: "the oldest supported version is 1.0"},
		{in: "99.0", err: "upgrade Dang"},
		{in: Version{Major: CurrentVersion.Major, Minor: CurrentVersion.Minor + 1}.String(), err: "upgrade Dang"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			v, err := ParseVersion(tc.in)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, v.String())
		})
	}
}

func TestVersionOrder(t *testing.T) {
	assert.True(t, Version1_0.Before(Version2_0))
	assert.True(t, Version{Major: 1, Minor: 9}.Before(Version2_0))
	assert.True(t, Version2_0.Before(Version{Major: 2, Minor: 10}))
	assert.True(t, Version{Major: 0, Minor: 19}.Before(Version{Major: 0, Minor: 19, Patch: 1}))
	assert.Equal(t, "0.19.1", Version{Major: 0, Minor: 19, Patch: 1}.String())
	assert.False(t, Version2_0.Before(Version1_0))
	assert.False(t, Version2_0.Before(Version2_0))
	// The zero value is the current version, like an unset parser option.
	assert.False(t, Version{}.Before(CurrentVersion))
	assert.Equal(t, CurrentVersion.String(), Version{}.String())
}

func TestVersionPrereleaseOrder(t *testing.T) {
	parse := func(s string) Version {
		t.Helper()
		v, err := parseVersion("test", s)
		require.NoError(t, err)
		return v
	}
	// Semver precedence, oldest first (the spec's own example, then betas
	// whose numbers need comparing numerically).
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-beta.20", "1.0.0-beta.21",
		"1.0.0-beta.100", "1.0.0-rc.1", "1.0.0", "1.0.1-0", "1.0.1",
	}
	for i, a := range ordered {
		for j, b := range ordered {
			assert.Equal(t, i < j, parse(a).Before(parse(b)), "%s before %s", a, b)
		}
	}
	// Build metadata is dropped.
	assert.Equal(t, parse("v1.0.0-beta.21"), parse("v1.0.0-beta.21+abcdef12.dirty"))
	assert.Equal(t, parse("v1.0.0"), parse("v1.0.0+abc"))
	assert.Equal(t, "1.0.0-beta.21", parse("v1.0-beta.21").String())
	assert.Equal(t, Version{Major: 1, Prerelease: "beta.21"}, parse("1.0.0-beta.21"))
}

func TestProjectConfigVersion(t *testing.T) {
	t.Run("declared version", func(t *testing.T) {
		config, err := LoadProjectConfig(writeConfig(t, "dang = \"1.0\"\n"))
		require.NoError(t, err)
		assert.Equal(t, Version1_0, config.Dang)
		assert.Equal(t, Version1_0, config.EffectiveVersion())
	})

	t.Run("declared version with a v and a patch", func(t *testing.T) {
		config, err := LoadProjectConfig(writeConfig(t, "dang = \"v2.0.3\"\n"))
		require.NoError(t, err)
		assert.Equal(t, Version2_0, config.Dang)
	})

	t.Run("missing version is the default", func(t *testing.T) {
		config, err := LoadProjectConfig(writeConfig(t, "[imports.Dagger]\ndagger = true\n"))
		require.NoError(t, err)
		assert.True(t, config.Dang.IsZero())
		assert.Equal(t, DefaultVersion, config.EffectiveVersion())
	})

	t.Run("version newer than this Dang", func(t *testing.T) {
		_, err := LoadProjectConfig(writeConfig(t, "dang = \"99.0\"\n"))
		require.ErrorContains(t, err, "this project needs Dang 99.0")
	})

	t.Run("non-string version", func(t *testing.T) {
		_, err := LoadProjectConfig(writeConfig(t, "dang = 2.0\n"))
		require.ErrorContains(t, err, "the Dang version must be a string")
	})

	t.Run("nil config", func(t *testing.T) {
		var config *ProjectConfig
		assert.Equal(t, DefaultVersion, config.EffectiveVersion())
	})
}

func TestVersionFromContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, CurrentVersion, VersionFromContext(ctx))

	// An embedder's version applies to source no dang.toml governs.
	hostCtx := ContextWithVersion(ctx, Version1_0)
	assert.Equal(t, Version1_0, VersionFromContext(hostCtx))

	// A project config on the context does not decide the version: only
	// the source's path does (see TestVersionForDir).
	assert.Equal(t, CurrentVersion, VersionFromContext(
		ContextWithProjectConfig(ctx, "dang.toml", &ProjectConfig{Dang: Version1_0})))
}

func TestVersionForDir(t *testing.T) {
	ctx := context.Background()
	hostCtx := ContextWithVersion(ctx, Version1_0)

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

	nested := mkdir("one/nested")
	write("one/dang.toml", "dang = \"1.0\"\n")
	implicit := mkdir("implicit")
	write("implicit/dang.toml", "[imports.Dagger]\ndagger = true\n")
	ungoverned := mkdir("ungoverned")
	broken := mkdir("broken")
	write("broken/dang.toml", "dang = 2\n")

	for _, tc := range []struct {
		name string
		ctx  context.Context
		dir  string
		want Version
	}{
		{"nearest dang.toml, walking up", ctx, nested, Version1_0},
		{"dang.toml beats the embedder", hostCtx, filepath.Join(root, "one"), Version1_0},
		{"dang.toml without a version", ctx, implicit, DefaultVersion},
		{"dang.toml without a version: embedder", hostCtx, implicit, Version1_0},
		{"boundary reached: current", ctx, ungoverned, CurrentVersion},
		{"boundary reached: embedder", hostCtx, ungoverned, Version1_0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, err := VersionForDir(tc.ctx, tc.dir)
			require.NoError(t, err)
			assert.Equal(t, tc.want, version)
		})
	}

	t.Run("broken dang.toml", func(t *testing.T) {
		version, err := VersionForDir(hostCtx, broken)
		require.Error(t, err)
		assert.Equal(t, Version1_0, version)
	})
}
