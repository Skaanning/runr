package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
)

func newGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "group",
		Short: "Manage groups of projects",
	}
	group.AddCommand(
		&cobra.Command{
			Use:               "create <group> <project...>",
			Short:             "Create a group",
			Args:              cobra.MinimumNArgs(2),
			ValidArgsFunction: completeAfterFirstArg(nil, completeProjects),
			RunE: func(cmd *cobra.Command, args []string) error {
				name, members := args[0], dedupe(args[1:])
				s, err := loadStore()
				if err != nil {
					return err
				}
				if _, exists := s.cfg.Groups[name]; exists {
					return fmt.Errorf("group %q already exists (use runr group add to extend it)", name)
				}
				s.cfg.Groups[name] = members

				warnings, err := s.save()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Created group %q: %s\n", name, strings.Join(members, ", "))
				printWarnings(cmd.ErrOrStderr(), warnings, name)
				return nil
			},
		},
		&cobra.Command{
			Use:               "add <group> <project...>",
			Short:             "Add projects to a group",
			Args:              cobra.MinimumNArgs(2),
			ValidArgsFunction: completeAfterFirstArg(completeGroups, completeProjects),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := args[0]
				s, err := loadStore()
				if err != nil {
					return err
				}
				members, exists := s.cfg.Groups[name]
				if !exists {
					return fmt.Errorf("unknown group %q (use runr group create to make it)", name)
				}

				var added, present []string
				for _, project := range dedupe(args[1:]) {
					if contains(members, project) {
						present = append(present, project)
					} else {
						added = append(added, project)
					}
				}
				out := cmd.OutOrStdout()
				if len(added) == 0 {
					fmt.Fprintf(out, "Group %q already contains %s\n", name, strings.Join(present, ", "))
					return nil
				}
				s.cfg.Groups[name] = append(members, added...)

				warnings, err := s.save()
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Added %s to group %q\n", strings.Join(added, ", "), name)
				if len(present) > 0 {
					fmt.Fprintf(out, "  already in group: %s\n", strings.Join(present, ", "))
				}
				printWarnings(cmd.ErrOrStderr(), warnings, name)
				return nil
			},
		},
		&cobra.Command{
			Use:               "rm <group> <project...>",
			Short:             "Remove projects from a group",
			Args:              cobra.MinimumNArgs(2),
			ValidArgsFunction: completeAfterFirstArg(completeGroups, completeGroupMembers),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := args[0]
				s, err := loadStore()
				if err != nil {
					return err
				}
				members, exists := s.cfg.Groups[name]
				if !exists {
					return fmt.Errorf("unknown group %q", name)
				}

				remove := dedupe(args[1:])
				for _, project := range remove {
					if !contains(members, project) {
						return fmt.Errorf("%q is not in group %q", project, name)
					}
				}
				for _, project := range remove {
					members = without(members, project)
				}
				if members == nil {
					members = []string{} // keep the (now empty) group
				}
				s.cfg.Groups[name] = members

				warnings, err := s.save()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %s from group %q\n", strings.Join(remove, ", "), name)
				printWarnings(cmd.ErrOrStderr(), warnings, name)
				return nil
			},
		},
		&cobra.Command{
			Use:               "delete <group>",
			Short:             "Delete a group (its projects are kept)",
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: completeFirstArg(completeGroups),
			RunE: func(cmd *cobra.Command, args []string) error {
				name := args[0]
				s, err := loadStore()
				if err != nil {
					return err
				}
				if _, exists := s.cfg.Groups[name]; !exists {
					return fmt.Errorf("unknown group %q", name)
				}
				delete(s.cfg.Groups, name)

				if _, err := s.save(); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted group %q\n", name)
				return nil
			},
		},
		&cobra.Command{
			Use:   "ls",
			Short: "List groups",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := loadStore()
				if err != nil {
					return err
				}
				if len(s.cfg.Groups) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No groups yet. Create one with:\n  runr group create <group> <project...>")
					return nil
				}
				printGroups(cmd.OutOrStdout(), s.cfg)
				return nil
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

// dedupe returns list without repeated entries, keeping the first occurrence.
func dedupe(list []string) []string {
	var out []string
	for _, s := range list {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}

// completeGroups offers group names for shell completion.
func completeGroups(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	s, err := loadStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return sortedNames(s.cfg.Groups), cobra.ShellCompDirectiveNoFileComp
}

// completeGroupMembers offers the members of the group named by the first argument.
func completeGroupMembers(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	s, err := loadStore()
	if err != nil || len(args) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return s.cfg.Groups[args[0]], cobra.ShellCompDirectiveNoFileComp
}

// completeAfterFirstArg completes the first argument with first (nothing if
// nil) and every later argument with rest.
func completeAfterFirstArg(first, rest cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			if first == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return first(cmd, args, toComplete)
		}
		return rest(cmd, args, toComplete)
	}
}
