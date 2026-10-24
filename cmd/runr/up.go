package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/gitinfo"
	"github.com/Skaanning/runr/internal/supervisor"
	"github.com/Skaanning/runr/internal/tui"
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
			// The TUI needs a terminal for both keys and drawing; otherwise
			// (e.g. piped to a file) behave like --plain.
			if plain || !isTerminal(cmd.InOrStdin()) || !isTerminal(cmd.OutOrStdout()) {
				return runPlain(cmd.OutOrStdout(), s.cfg, args, projects, sigs, colorsFor(cmd.OutOrStdout()))
			}
			return runTUI(cmd.ErrOrStderr(), s.cfg, args, sigs)
		},
	}
	// Plain interleaved output, for debugging and non-interactive use.
	cmd.Flags().BoolVar(&plain, "plain", false, "print interleaved output instead of opening the TUI")
	_ = cmd.Flags().MarkHidden("plain")
	return cmd
}

// runTUI starts the projects and shows the running view until the user
// quits. Projects that ended crashed or blocked are listed on stderr
// afterwards, since the TUI's screen is gone by then.
func runTUI(stderr io.Writer, cfg *config.Config, names []string, sigs <-chan os.Signal) error {
	sup := supervisor.New(cfg, supervisor.Options{})
	err := sup.Start(names...)
	if err == nil {
		err = tui.Run(sup, cfg, names, gitReader(), sigs)
	}
	finish(stderr, sup)
	return err
}

// gitReader returns the reader for the TUI's git display, or nil (hiding
// it) when git is not installed.
func gitReader() tui.GitReader {
	g, err := gitinfo.New()
	if err != nil {
		return nil
	}
	return g
}

// finish shuts a supervisor down after the TUI has exited and reports
// projects that ended crashed or blocked.
func finish(stderr io.Writer, sup *supervisor.Supervisor) {
	final := sup.Processes()
	sup.Close() // kills anything still alive if the TUI failed
	for range sup.Events() {
	}
	if s := summarize(final); s != "" {
		fmt.Fprintf(stderr, "runr: %s\n", s)
	}
}

// summarize describes the projects that ended crashed or blocked, or
// returns "" if there are none.
func summarize(infos []supervisor.ProcessInfo) string {
	var parts []string
	for _, info := range infos {
		switch info.State {
		case supervisor.Crashed:
			parts = append(parts, fmt.Sprintf("%s crashed (%s)", info.Name, info.Exit))
		case supervisor.Blocked:
			parts = append(parts, info.Name+" was blocked")
		}
	}
	return strings.Join(parts, ", ")
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
