package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/sessions"
	"github.com/Skaanning/runr/internal/supervisor"
	"github.com/Skaanning/runr/internal/tui"
)

func newCleanupCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Stop processes left running by a runr that was killed",
		Long: "List the processes that earlier runr sessions left running because runr itself was\n" +
			"killed or crashed, and stop them (SIGTERM, then SIGKILL after shutdown_timeout).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			orphans := findLeftovers(sessionsDir())
			if len(orphans) == 0 {
				fmt.Fprintln(out, "no processes left over from earlier runr sessions")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, o := range orphans {
				for _, c := range o.Children {
					fmt.Fprintf(tw, "%s\tpid %d\t%s\n", c.Project, c.Pid, c.Command)
				}
			}
			tw.Flush()
			if !yes && !confirm(cmd.InOrStdin(), out, "kill them? [y/N] ") {
				fmt.Fprintln(out, "left them running")
				return nil
			}
			timeout := shutdownTimeout()
			fmt.Fprintln(out, killReport(orphans, sessions.Kill(orphans, timeout), timeout))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask before killing them")
	return cmd
}

// confirm asks a yes/no question on in, defaulting to no.
func confirm(in io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// shutdownTimeout is the configured shutdown timeout, or the default when
// the config can't be read.
func shutdownTimeout() time.Duration {
	if s, err := loadStore(); err == nil {
		return s.cfg.Settings.ShutdownTimeout()
	}
	return config.DefaultShutdownTimeout
}

// sessionsDir is where session files are kept, or "" where runr doesn't
// record them (Windows).
func sessionsDir() string {
	dir, err := sessions.Dir()
	if err != nil {
		return ""
	}
	return dir
}

// findLeftovers returns the processes left over by runr sessions that were
// killed; errors reading the session files mean none.
func findLeftovers(dir string) []sessions.Orphan {
	if dir == "" {
		return nil
	}
	orphans, _ := sessions.Find(dir)
	return orphans
}

// track starts recording this session's processes in opts, so that they
// can be cleaned up if runr is killed. The returned recorder (nil if
// recording isn't possible) must be closed after a clean shutdown.
func track(dir string, opts *supervisor.Options) *sessions.Recorder {
	if dir == "" {
		return nil
	}
	rec, err := sessions.Open(dir)
	if err != nil {
		return nil
	}
	opts.Tracker = rec
	return rec
}

// leftoversFor offers orphans to the TUI.
func leftoversFor(orphans []sessions.Orphan, timeout time.Duration) *tui.Leftovers {
	if len(orphans) == 0 {
		return nil
	}
	what, which := sessions.Describe(orphans)
	return &tui.Leftovers{
		What:   what,
		Which:  which,
		Kill:   func() string { return killReport(orphans, sessions.Kill(orphans, timeout), timeout) },
		Forget: func() { sessions.Forget(orphans) },
	}
}

// killReport describes what Kill did, e.g. "stopped 2 leftover processes
// (killed api after 5s)".
func killReport(orphans []sessions.Orphan, forced []sessions.Child, timeout time.Duration) string {
	n := 0
	for _, o := range orphans {
		n += len(o.Children)
	}
	what := "processes"
	if n == 1 {
		what = "process"
	}
	report := fmt.Sprintf("stopped %d leftover %s", n, what)
	if len(forced) > 0 {
		var names []string
		for _, c := range forced {
			names = append(names, c.Project)
		}
		report += fmt.Sprintf(" (killed %s after %v)", strings.Join(names, ", "), timeout)
	}
	return report
}
