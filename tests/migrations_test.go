package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
	"gotest.tools/v3/golden"
)

// migrationsDir holds one directory per migration path between editions,
// named <from>-to-<to>. Its before/ directory's dang.toml declares edition
// <from>, and it holds one script per form the migration rewrites, written
// the old way. after/ declares <to> and holds, under the same names, what
// `dang migrate` makes of each script: golden files, regenerated with
// -update. Each script asserts its own behaviour and runs both before and
// after migrating, so a pair of files covers one migration end to end.
//
// Adding an edition gate means adding its script here. The before/
// directories are excluded from `dang fmt`, which would print them in the
// current spelling (see .dagger/main.dang).
const migrationsDir = "testdata/migrations"

func (DangSuite) TestMigrations(ctx context.Context, t *testctx.T) {
	paths, err := filepath.Glob(filepath.Join(migrationsDir, "*-to-*"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no migration paths in %s", migrationsDir)

	for _, pathDir := range paths {
		name := filepath.Base(pathDir)
		fromName, toName, _ := strings.Cut(name, "-to-")
		from, err := dang.ParseEdition(fromName)
		require.NoError(t, err, "migration path %s", name)
		to, err := dang.ParseEdition(toName)
		require.NoError(t, err, "migration path %s", name)

		beforeDir := filepath.Join(pathDir, "before")
		afterDir := filepath.Join(pathDir, "after")
		requireDirEdition(t, beforeDir, from)
		requireDirEdition(t, afterDir, to)

		scripts, err := filepath.Glob(filepath.Join(beforeDir, "*.dang"))
		require.NoError(t, err)
		require.NotEmpty(t, scripts, "no scripts in %s", beforeDir)

		// after/ holds nothing but the migrated scripts.
		migrated, err := filepath.Glob(filepath.Join(afterDir, "*.dang"))
		require.NoError(t, err)
		for _, script := range migrated {
			_, err := os.Stat(filepath.Join(beforeDir, filepath.Base(script)))
			require.NoError(t, err, "%s has no counterpart in %s", script, beforeDir)
		}

		for _, script := range scripts {
			base := filepath.Base(script)
			t.Run(name+"/"+base, func(ctx context.Context, t *testctx.T) {
				ctx = ioctx.StdoutToContext(ctx, NewTWriter(t))

				source, err := os.ReadFile(script)
				require.NoError(t, err)
				got, err := dang.MigrateSource(script, source, from, to)
				require.NoError(t, err)
				golden.Assert(t, got, filepath.Join("migrations", name, "after", base))

				require.NoError(t, dang.RunFile(ctx, script, false), "before migrating")
				require.NoError(t, dang.RunFile(ctx, filepath.Join(afterDir, base), false), "after migrating")
			})
		}
	}
}

func requireDirEdition(t *testctx.T, dir string, want dang.Edition) {
	edition, err := dang.EditionForDir(context.Background(), dir)
	require.NoError(t, err)
	require.Equal(t, want, edition, "%s must be governed by a dang.toml declaring edition %q", dir, want)
}
