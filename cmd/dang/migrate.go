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
		Short: "Migrate Dang source to a newer edition",
		Long: `Migrate every project under the given directories (default: the current
directory) to a newer edition: each .dang file a dang.toml governs is
rewritten in the target edition's syntax and formatted, and the dang.toml's
edition is updated to match.

By default, migrate only reports what it would change. Use -w to write.

Source no dang.toml governs is already in the latest edition, unless
something else decides its edition (e.g. a Dagger engine version). Use
--from to say which edition it is in; migrating it creates a dang.toml
declaring the new edition.

Migration rewrites syntax. Code that a newer edition rejects for other
reasons (e.g. a stricter type check) is left for you to fix; run the
migrated project to find it. A file that doesn't parse in its project's
edition can't be migrated: migrate refuses to write, unless
--skip-unparseable leaves such files as they are.`,
		Example: `  # See what migrating the current project to the latest edition changes
  dang migrate

  # Migrate it
  dang migrate -w

  # Migrate a project to a specific edition
  dang migrate -w --to 2 ./my-module

  # Migrate a module without a dang.toml that is written in edition 1
  dang migrate -w --from 1 ./my-module`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"."}
			}
			target := dang.LatestEdition
			if to != "" {
				e, err := dang.ParseEdition(to)
				if err != nil {
					return err
				}
				target = e
			}
			var opts dang.MigrateOptions
			if from != "" {
				e, err := dang.ParseEdition(from)
				if err != nil {
					return err
				}
				opts.UngovernedFrom = e
			}
			return runMigrate(cmd, args, target, opts, write, skipUnparseable)
		},
	}

	cmd.Flags().StringVar(&to, "to", "", "Edition to migrate to (default: the latest edition)")
	cmd.Flags().StringVar(&from, "from", "", "Edition of source that no dang.toml governs")
	cmd.Flags().BoolVarP(&write, "write", "w", false, "Write the migrated files and dang.toml")
	cmd.Flags().BoolVar(&skipUnparseable, "skip-unparseable", false, "Leave files that don't parse in their edition as they are")

	return cmd
}

func runMigrate(cmd *cobra.Command, dirs []string, to dang.Edition, opts dang.MigrateOptions, write, skipUnparseable bool) error {
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
		_, _ = fmt.Fprintf(out, "nothing to migrate: no project is older than edition %q\n", to)
		return nil
	}

	var unparseable int
	for _, plan := range plans {
		_, _ = fmt.Fprintf(out, "%s: edition %q -> %q\n", plan.ConfigPath, plan.From, plan.To)
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
		return fmt.Errorf("%d file(s) don't parse in their project's edition; fix them, or use --skip-unparseable to leave them as they are", unparseable)
	}
	for _, plan := range plans {
		if err := plan.Write(); err != nil {
			return err
		}
	}
	return nil
}
