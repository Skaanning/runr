package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"runr/internal/config"
)

func newGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "group",
		Short: "Manage groups of projects",
	}
	group.AddCommand(
		&cobra.Command{
			Use:   "create <group> <project...>",
			Short: "Create a group",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("group create")
			},
		},
		&cobra.Command{
			Use:   "add <group> <project...>",
			Short: "Add projects to a group",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("group add")
			},
		},
		&cobra.Command{
			Use:   "rm <group> <project...>",
			Short: "Remove projects from a group",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("group rm")
			},
		},
		&cobra.Command{
			Use:   "delete <group>",
			Short: "Delete a group",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("group delete")
			},
		},
		&cobra.Command{
			Use:   "ls",
			Short: "List groups",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("group ls")
			},
		},
	)
	return group
}

func printGroups(w io.Writer, cfg *config.Config) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tPROJECTS")
	for _, name := range sortedNames(cfg.Groups) {
		fmt.Fprintf(tw, "%s\t%s\n", name, strings.Join(cfg.Groups[name], ", "))
	}
	tw.Flush()
}
