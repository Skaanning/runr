package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"runr/internal/config"
	"runr/internal/supervisor"
	"runr/internal/tui"
)

func newUpCmd() *cobra.Command {
	var plain bool
	cmd := &cobra.Command{
		Use:               "up <name...>",
		Short:             "Start groups and/or projects",
		Long:              "Start the given groups and/or projects, plus everything they depend on.",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeProjectsAndGroups,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			projects, err := prepareUp(s.cfg, args, cmd.ErrOrStderr())
			if err != nil {
				return err
			}

			sigs := make(chan os.Signal, 2)
			signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer signal.Stop(sigs)
			if plain {
				return runPlain(cmd.OutOrStdout(), s.cfg, args, projects, sigs, colorEnabled(cmd.OutOrStdout()))
			}
			return runTUI(s.cfg, args, sigs)
		},
	}
	// Plain interleaved output, for debugging and non-interactive use.
	cmd.Flags().BoolVar(&plain, "plain", false, "print interleaved output instead of opening the TUI")
	_ = cmd.Flags().MarkHidden("plain")
	return cmd
}

// runTUI starts the projects and shows the running view until the user quits.
func runTUI(cfg *config.Config, names []string, sigs <-chan os.Signal) error {
	sup := supervisor.New(cfg, supervisor.Options{})
	err := sup.Start(names...)
	if err == nil {
		err = tui.Run(sup, cfg, names, sigs)
	}
	sup.Close() // kills anything still alive if the TUI failed
	for range sup.Events() {
	}
	return err
}

// prepareUp validates the config and checks that every project that would be
// started can be: names resolve and working directories exist. It returns
// the resolved projects in start order.
func prepareUp(cfg *config.Config, names []string, stderr io.Writer) ([]string, error) {
	warnings, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	projects, err := cfg.Resolve(names...)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("nothing to start: %s contains no projects", strings.Join(names, ", "))
	}

	inSet := map[string]bool{}
	for _, name := range projects {
		inSet[name] = true
	}
	var missing []string
	for _, w := range warnings {
		if !inSet[w.Name] {
			continue
		}
		if w.Field == "dir" {
			missing = append(missing, w.String())
		} else {
			fmt.Fprintf(stderr, "warning: %s\n", w)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("cannot start:\n  %s", strings.Join(missing, "\n  "))
	}
	return projects, nil
}

// completeProjectsAndGroups offers every project and group name.
func completeProjectsAndGroups(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	s, err := loadStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := append(sortedNames(s.cfg.Groups), sortedNames(s.cfg.Projects)...)
	return names, cobra.ShellCompDirectiveNoFileComp
}
