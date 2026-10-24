package main

import "github.com/spf13/cobra"

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up <name...>",
		Short: "Start groups and/or projects",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("up")
		},
	}
}
