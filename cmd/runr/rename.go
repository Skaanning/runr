package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <name> <new-name>",
		Short: "Rename a project or group",
		Long: "Rename a project or group. When a project is renamed, the groups containing it\n" +
			"and the projects depending on it are updated too.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeFirstArg(completeProjectsAndGroups),
		RunE: func(cmd *cobra.Command, args []string) error {
			old, name := args[0], args[1]
			s, err := loadStore()
			if err != nil {
				return err
			}
			kind := "project"
			if _, ok := s.cfg.Groups[old]; ok {
				kind = "group"
			}
			groups, dependents, err := s.cfg.Rename(old, name)
			if err != nil {
				return err
			}
			if _, err := s.save(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Renamed %s %q to %q\n", kind, old, name)
			if len(groups) > 0 {
				fmt.Fprintf(out, "  updated groups: %s\n", strings.Join(groups, ", "))
			}
			if len(dependents) > 0 {
				fmt.Fprintf(out, "  updated depends_on of: %s\n", strings.Join(dependents, ", "))
			}
			return nil
		},
	}
}
