package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "runr",
		Short: "Run multiple local projects at once",
		Long: "runr runs multiple local projects (e.g. microservices) at once and shows\n" +
			"their output in a terminal UI. Run without arguments to open the launcher.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("launcher")
		},
	}

	root.AddCommand(
		newUpCmd(),
		newAddCmd(),
		newEditCmd(),
		newRmCmd(),
		newLsCmd(),
		newGroupCmd(),
	)
	return root
}

func notImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}
