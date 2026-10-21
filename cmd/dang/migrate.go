package main

import (
	"fmt"
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
		Use:   "migrate [flags] [dir...]",
		Short: "Migrate Dang source to a newer Dang version",
		Long: `Migrate every project under the given directories (default: the current
directory) to a newer Dang version: each .dang file a dang.toml governs is
rewritten in the target version's syntax and formatted, and the dang.toml's
` + "`dang`" + ` version is updated to match.

By default, migrate only reports what it would change. Use -w to write.

A dang.toml without a ` + "`dang`" + ` key means ` + dang.DefaultVersion.String() + `, and source no dang.toml
governs is in this Dang's version, unless something else decides its
version (e.g. a Dagger engine version). For either, use --from to say which
version the source is really written for, e.g. a project from an older Dang
that predates the key; migrating then records the new version, creating a
dang.toml if there is none. --from applies to the source no dang.toml
governs and to the project of the given directory itself, not to the
projects nested below it, which may be written for another version: those
without a ` + "`dang`" + ` key are reported, to migrate by naming them.

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
  dang migrate -w --from 1.0 ./my-module`,
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
			return runMigrate(cmd, args, target, opts, write, skipUnparseable)
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
	planIndex := map[string]int{}
	// fromApplied records the projects planned with --from applied, which
	// win over a plan made by a directory above them, for which they were a
	// nested project declaring no version.
	fromApplied := map[string]bool{}
	var undeclared []string
	for _, dir := range dirs {
		dirPlan, err := dang.PlanMigrations(dir, to, opts)
		if err != nil {
			return err
		}
		undeclared = append(undeclared, dirPlan.Undeclared...)
		for _, plan := range dirPlan.Projects {
			applied := !slices.Contains(dirPlan.Undeclared, plan.ConfigPath)
			i, seen := planIndex[plan.ConfigPath]
			switch {
			case !seen:
				planIndex[plan.ConfigPath] = len(plans)
				plans = append(plans, plan)
			case applied && !fromApplied[plan.ConfigPath]:
				plans[i] = plan
			}
			fromApplied[plan.ConfigPath] = fromApplied[plan.ConfigPath] || applied
		}
	}

	slices.Sort(undeclared)
	for _, configPath := range slices.Compact(undeclared) {
		projectDir := filepath.Dir(configPath)
		if slices.ContainsFunc(dirs, func(dir string) bool { return isWithin(projectDir, dir) }) {
			// Another directory argument, in the project, applies --from.
			continue
		}
		_, _ = fmt.Fprintf(out, "%s: declares no Dang version, so --from doesn't apply to it and it stays %s; to migrate it from another version, run migrate --from on its directory\n", configPath, dang.DefaultVersion)
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

// isWithin reports whether path is dir or inside it.
func isWithin(dir, path string) bool {
	path, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
