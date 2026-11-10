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
		Long: `Declare this Dang's version in a directory's dang.toml (default: the
current directory), creating the file if needed, so a later Dang keeps
reading the source the same way.

Run it when starting a new project. For a project written for an older
Dang, use dang migrate --from <version> instead.`,
		Example: `  # Start a project in the current directory
  dang init

  # Pin the version of an existing project
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
			}
			return nil
		},
	}
}
