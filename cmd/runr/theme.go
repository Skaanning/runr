package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/tui"
)

func newThemeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "theme [name]",
		Short: "List the color themes, or set one",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return config.ThemeNames, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(args) == 0 {
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				for _, name := range config.ThemeNames {
					marker := " "
					if name == s.cfg.Settings.Theme() {
						marker = "*"
					}
					fmt.Fprintf(tw, "%s %s\t%s\n", marker, name, tui.ThemeAbout(name))
				}
				return tw.Flush()
			}
			s.cfg.Settings.ThemeValue = args[0]
			warnings, err := s.save()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Theme set to %s\n", args[0])
			printWarnings(cmd.ErrOrStderr(), warnings, "")
			return nil
		},
	}
}
