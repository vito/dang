package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vito/dang/v2/pkg/dang"
)

func migrateCmd() *cobra.Command {
	var (
		to              string
		from            string
		write           bool
		skipUnparseable bool
	)

	cmd := &cobra.Command{
		Use:   "migrate [flags] [path...]",
		Short: "Migrate Dang source to a newer Dang version",
		Long: `Migrate every project under the given directories (default: the current
directory) to a newer Dang version: each .dang file a dang.toml governs is
rewritten in the target version's syntax and formatted, and the dang.toml's
` + "`dang`" + ` version is updated to match.

By default, migrate only reports what it would change. Use -w to write.

Given a file, migrate prints what migrating it makes of it instead, writing
nothing. A file can't be migrated with -w: its version is its project's, so
migrating it alone would leave the rest of the project read in the new
version without having been rewritten for it.

Source whose version nothing declares - a dang.toml without a ` + "`dang`" + ` key,
or no dang.toml at all - is in this Dang's version, unless something else
decides its version (e.g. a Dagger engine version). Use --from to say which
version such source is really written for, e.g. a project from an older Dang
that predates the key; migrating then records the new version, creating a
dang.toml if there is none. --from applies to all of it under the given
directories: the source no dang.toml governs and every dang.toml without a
` + "`dang`" + ` key. Projects whose dang.toml declares a version migrate from it.
Without --from, migrate lists the projects that declare no version, since
nothing can tell an older Dang's source among them but you.

Migration rewrites syntax. Code that a newer version rejects for other
reasons (e.g. a stricter type check) is left for you to fix; run the
migrated project to find it. A file that doesn't parse in its project's
version can't be migrated: migrate refuses to write, unless
--skip-unparseable leaves such files as they are.

The output is for this Dang: migrating to a version older than this Dang's
can print spellings this Dang reads in that version but that Dang release
did not (e.g. ` + "`loop`" + ` for a ` + "`for`" + ` loop).`,
		Example: `  # See what migrating the current project to this Dang's version changes
  dang migrate

  # Migrate it
  dang migrate -w

  # Migrate a Dang 2.0 project whose dang.toml predates the dang key
  dang migrate -w --from 2.0 ./my-module

  # Migrate a module without a dang.toml that is written for Dang 1.0
  dang migrate -w --from 1.0 ./my-module

  # Print what migrating one file from Dang 2.0 makes of it
  dang migrate --from 2.0 ./my-module/main.dang`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"."}
			}
			target := dang.CurrentVersion
			if to != "" {
				v, err := dang.ParseVersion(to)
				if err != nil {
					return err
				}
				target = v
			}
			var opts dang.MigrateOptions
			if from != "" {
				v, err := dang.ParseVersion(from)
				if err != nil {
					return err
				}
				opts.UndeclaredFrom = v
			}
			resolve, stop := importedTypesResolver(cmd.Context())
			defer stop()
			opts.ImportedTypes = resolve
			var files, dirs []string
			for _, arg := range args {
				info, err := os.Stat(arg)
				if err != nil {
					return err
				}
				if info.IsDir() {
					dirs = append(dirs, arg)
				} else {
					files = append(files, arg)
				}
			}
			for _, file := range files {
				if write {
					return fmt.Errorf("%s is a file: migrate -w moves whole projects, so pass its project's directory, %s (without -w, migrate prints a file's migration)", file, projectDir(file))
				}
				if err := previewMigration(cmd, file, target, opts); err != nil {
					return err
				}
			}
			if len(dirs) == 0 {
				return nil
			}
			return runMigrate(cmd, dirs, target, opts, write, skipUnparseable)
		},
	}

	cmd.Flags().StringVar(&to, "to", "", "Dang version to migrate to (default: this Dang's version)")
	cmd.Flags().StringVar(&from, "from", "", "Dang version of source whose version no dang.toml declares")
	cmd.Flags().BoolVarP(&write, "write", "w", false, "Write the migrated files and dang.toml")
	cmd.Flags().BoolVar(&skipUnparseable, "skip-unparseable", false, "Leave files that don't parse in their version as they are")

	return cmd
}

func runMigrate(cmd *cobra.Command, dirs []string, to dang.Version, opts dang.MigrateOptions, write, skipUnparseable bool) error {
	out := cmd.OutOrStdout()

	// Plan everything before writing anything, so a failure in one directory
	// can't leave another half-migrated, and overlapping directories don't
	// migrate a project twice.
	var plans []*dang.ProjectMigration
	seen := map[string]bool{}
	var undeclared []string
	for i, dir := range dirs {
		if coveredByAnother(i, dirs) {
			// Planning the directory above it plans it too; planning it on
			// its own would also plan its ungoverned source a second time,
			// as a project of its own.
			continue
		}
		dirPlan, err := dang.PlanMigrations(dir, to, opts)
		if err != nil {
			return err
		}
		undeclared = append(undeclared, dirPlan.Undeclared...)
		for _, plan := range dirPlan.Projects {
			if seen[plan.ConfigPath] {
				continue
			}
			seen[plan.ConfigPath] = true
			plans = append(plans, plan)
		}
	}

	if opts.UndeclaredFrom.IsZero() {
		// Such source may be written for an older Dang, from before it
		// declared its version; only the user can say so.
		slices.Sort(undeclared)
		for _, dir := range slices.Compact(undeclared) {
			_, _ = fmt.Fprintf(out, "%s: declares no Dang version, so it is read as Dang %s; if it is written for an older one, migrate it with --from\n", dir, dang.CurrentVersion)
		}
	}

	if len(plans) == 0 {
		_, _ = fmt.Fprintf(out, "nothing to migrate: no project is older than Dang %s\n", to)
		return nil
	}

	var unparseable int
	for _, plan := range plans {
		_, _ = fmt.Fprintf(out, "%s: Dang %s -> %s\n", plan.ConfigPath, plan.From, plan.To)
		for _, f := range plan.Files {
			if f.Changed() {
				_, _ = fmt.Fprintf(out, "  %s\n", f.Path)
			}
		}
		for _, f := range plan.Unparseable {
			unparseable++
			_, _ = fmt.Fprintf(out, "  %s: not migrated: %v\n", f.Path, f.Err)
		}
	}

	if !write {
		_, _ = fmt.Fprintln(out, "(dry run; use -w to write)")
		return nil
	}
	if unparseable > 0 && !skipUnparseable {
		return fmt.Errorf("%d file(s) don't parse in their project's Dang version; fix them, or use --skip-unparseable to leave them as they are", unparseable)
	}
	for _, plan := range plans {
		if err := plan.Write(); err != nil {
			return err
		}
	}
	return nil
}

// previewMigration prints what migrating file to Dang version to makes of it,
// writing nothing. A file's version belongs to its project, so a file is only
// previewed: migrating it alone would leave its project half-migrated, its
// other files read in the new version without having been rewritten for it.
func previewMigration(cmd *cobra.Command, file string, to dang.Version, opts dang.MigrateOptions) error {
	source, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	configPath, config, err := dang.FindProjectConfig(filepath.Dir(file))
	if err != nil {
		return err
	}
	from := opts.UndeclaredFrom
	if config != nil && !config.Dang.IsZero() {
		from = config.Dang
	} else if from.IsZero() {
		from = dang.CurrentVersion
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: declares no Dang version, so it is read as Dang %s; if it is written for an older one, preview it with --from\n", file, from)
	}
	if !from.Before(to) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: nothing to migrate: it is Dang %s, not older than %s\n", file, from, to)
		_, err = cmd.OutOrStdout().Write(source)
		return err
	}
	var imports map[string]dang.ImportedTypes
	// Only the move to 2.4 depends on what the imports bring in.
	if configPath != "" && opts.ImportedTypes != nil && from.Before(dang.Version2_4) && !to.Before(dang.Version2_4) {
		imports, err = opts.ImportedTypes(configPath)
		if err != nil {
			return fmt.Errorf("%s: resolving the imports of %s: %w", file, configPath, err)
		}
	}
	migrated, err := dang.MigrateSourceWithImports(file, source, from, to, imports)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: Dang %s -> %s (preview; migrate its project's directory, %s, to write)\n", file, from, to, projectDir(file))
	_, err = fmt.Fprint(cmd.OutOrStdout(), migrated)
	return err
}

// importedTypesResolver returns a MigrateOptions.ImportedTypes that resolves
// a project's dang.toml imports as running it would, Dagger's included, and a
// func stopping the import services it started. A project no dang.toml
// governs has no imports it can know of: an embedder (e.g. a Dagger SDK)
// supplies those.
func importedTypesResolver(ctx context.Context) (func(string) (map[string]dang.ImportedTypes, error), func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	services := &dang.ServiceRegistry{}
	ctx = dang.ContextWithServices(ctx, services)
	resolve := func(configPath string) (map[string]dang.ImportedTypes, error) {
		if _, err := os.Stat(configPath); err != nil {
			return nil, nil
		}
		config, err := dang.LoadProjectConfig(configPath)
		if err != nil {
			return nil, err
		}
		dir := filepath.Dir(configPath)
		ctx := dang.ContextWithProjectConfig(ctx, configPath, config)
		resolved, err := dang.ResolveImportConfigs(ctx, config, dir)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(resolved, func(ic dang.ImportConfig) bool { return ic.Dagger }) {
			resolved = dang.ResolveDaggerImport(ctx, resolved, dir)
		}
		return dang.BuiltinImportTypes(ctx, resolved)
	}
	return resolve, services.StopAll
}

// projectDir returns the directory of the project file belongs to: that of
// the dang.toml governing it, or its own directory if none does.
func projectDir(file string) string {
	dir, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return filepath.Dir(file)
	}
	if configPath, _, err := dang.FindProjectConfig(dir); err == nil && configPath != "" {
		dir = filepath.Dir(configPath)
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, dir); err == nil && isWithin(cwd, dir) {
			return rel
		}
	}
	return dir
}

// coveredByAnother reports whether migrating another of dirs migrates
// dirs[i] too: dirs[i] is inside it, and no repository boundary (.git), which
// PlanMigrations doesn't cross, lies between them. Of the arguments naming
// the same directory, the first covers the rest.
func coveredByAnother(i int, dirs []string) bool {
	abs, err := filepath.Abs(dirs[i])
	if err != nil {
		return false
	}
	for j, other := range dirs {
		otherAbs, err := filepath.Abs(other)
		if err != nil || j == i {
			continue
		}
		if otherAbs == abs {
			if j < i {
				return true
			}
			continue
		}
		if !isWithin(otherAbs, abs) {
			continue
		}
		crossesRepo := false
		for d := abs; d != otherAbs; d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
				crossesRepo = true
				break
			}
		}
		if !crossesRepo {
			return true
		}
	}
	return false
}

// isWithin reports whether path is dir or inside it.
func isWithin(dir, path string) bool {
	path, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
