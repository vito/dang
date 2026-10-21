package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/vito/dang/v2/pkg/dang"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a dang.toml declaring the Dang version",
		Long: `Make a directory (default: the current directory) a Dang project whose
dang.toml declares the Dang version its source is written in.

Without a dang.toml, init creates one declaring this Dang's version, which
is what the directory's source already means. An existing dang.toml that
declares no version gets the version it already means (` + dang.DefaultVersion.String() + `), so
nothing changes meaning; use dang migrate to move it forward.

Source whose version something else decides (e.g. a Dagger engine version)
should use dang migrate --from instead, which records the version it is
really written in.`,
		Example: `  # Start a project in the current directory
  dang init

  # Pin an existing dang.toml's version
  dang init ./my-module`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			result, err := dang.InitProject(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case result.Created:
				_, _ = fmt.Fprintf(out, "created %s for Dang %s\n", result.ConfigPath, result.Version)
			case result.AlreadyDeclared:
				_, _ = fmt.Fprintf(out, "%s already declares Dang %s\n", result.ConfigPath, result.Version)
			default:
				_, _ = fmt.Fprintf(out, "%s now declares Dang %s, the version it already used\n", result.ConfigPath, result.Version)
				if result.Version.Before(dang.CurrentVersion) {
					_, _ = fmt.Fprintf(out, "run dang migrate to move it to Dang %s\n", dang.CurrentVersion)
				}
			}
			return nil
		},
	}
}
