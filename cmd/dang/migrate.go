package main

import (
	"fmt"

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
dang.toml if there is none.

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
	seen := map[string]bool{}
	for _, dir := range dirs {
		dirPlans, err := dang.PlanMigrations(dir, to, opts)
		if err != nil {
			return err
		}
		for _, plan := range dirPlans {
			if !seen[plan.ConfigPath] {
				seen[plan.ConfigPath] = true
				plans = append(plans, plan)
			}
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
