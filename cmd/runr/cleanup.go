package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/plaintext"
	"github.com/Skaanning/runr/internal/proc"
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
			"killed or crashed, and stop them (SIGTERM, then SIGKILL after shutdown_timeout).\n" +
			"Then run the stop_cmd of each of those projects that has one, also when its\n" +
			"process is gone already.",
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
					switch {
					case c.Gone:
						fmt.Fprintf(tw, "%s\tgone\tstop_cmd: %s\n", c.Project, c.StopCmd)
					case c.StopCmd != "":
						fmt.Fprintf(tw, "%s\tpid %d\t%s\tthen stop_cmd: %s\n", c.Project, c.Pid, c.Command, c.StopCmd)
					default:
						fmt.Fprintf(tw, "%s\tpid %d\t%s\n", c.Project, c.Pid, c.Command)
					}
				}
			}
			tw.Flush()
			question := "kill them? [y/N] "
			if len(stopProjects(orphans, nil)) > 0 {
				question = "kill them and run the stop_cmds? [y/N] "
			}
			if !yes && !confirm(cmd.InOrStdin(), out, question) {
				fmt.Fprintln(out, "left them running")
				return nil
			}
			var cfg *config.Config
			if s, err := loadStore(); err == nil {
				cfg = s.cfg
			}
			fmt.Fprintln(out, cleanUp(orphans, stopProjects(orphans, cfg), shutdownTimeout(), out))
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

// leftoversFor offers orphans to the TUI. Their projects' settings are
// taken from cfg now: the launcher's forms change it later.
func leftoversFor(orphans []sessions.Orphan, cfg *config.Config, timeout time.Duration) *tui.Leftovers {
	if len(orphans) == 0 {
		return nil
	}
	what, which := sessions.Describe(orphans)
	projects := stopProjects(orphans, cfg)
	return &tui.Leftovers{
		What:   what,
		Which:  which,
		Kill:   func() string { return cleanUp(orphans, projects, timeout, io.Discard) },
		Forget: func() { sessions.Forget(orphans) },
	}
}

// stopProjects returns copies of the projects of orphans that have a
// stop_cmd, by name, as registered in cfg (nil: none), for the
// environment their stop_cmd runs with.
func stopProjects(orphans []sessions.Orphan, cfg *config.Config) map[string]config.Project {
	out := map[string]config.Project{}
	for _, o := range orphans {
		for _, c := range o.Children {
			if c.StopCmd == "" {
				continue
			}
			var p config.Project
			if cfg != nil && cfg.Projects[c.Project] != nil {
				p = *cfg.Projects[c.Project]
			}
			out[c.Project] = p
		}
	}
	return out
}

// cleanUp stops the orphans' processes, then runs their stop_cmds (output
// on out), and describes what it did.
func cleanUp(orphans []sessions.Orphan, projects map[string]config.Project, timeout time.Duration, out io.Writer) string {
	forced := sessions.Kill(orphans, timeout)
	ran, failed := runStopCmds(orphans, projects, timeout, out)
	return killReport(orphans, forced, timeout, ran, failed)
}

// stopCmdTimeout is how long a leftover's stop_cmd may run before it is
// killed (as in a session). A variable so tests can shorten it.
var stopCmdTimeout = time.Minute

// runStopCmds runs the stop_cmd of each orphaned child that has one, one
// after the other, in the dir it was started in, with the environment of
// its project in projects. Their lines go to out, after the project's name.
// It returns how many ran and how those that failed ended.
func runStopCmds(orphans []sessions.Orphan, projects map[string]config.Project, timeout time.Duration, out io.Writer) (ran int, failed []string) {
	var mu sync.Mutex // lines come from stdout and stderr readers
	for _, o := range orphans {
		for _, c := range o.Children {
			if c.StopCmd == "" {
				continue
			}
			p := projects[c.Project]
			p.StopCmd = c.StopCmd // as it was when the process started
			spec, err := supervisor.StopSpec(&p, c.Dir, os.Environ(), timeout)
			var pr *proc.Process
			if err == nil {
				name := c.Project
				pr, err = proc.Start(spec, func(l proc.Line) {
					if !l.Partial {
						mu.Lock()
						fmt.Fprintf(out, "%s │ %s\n", name, plaintext.StripANSI(l.Text))
						mu.Unlock()
					}
				})
			}
			if err != nil {
				failed = append(failed, fmt.Sprintf("stop_cmd of %s failed to start: %v", c.Project, err))
				continue
			}
			ran++
			select {
			case <-pr.Done():
			case <-time.After(stopCmdTimeout):
				pr.Kill()
				<-pr.Done()
			}
			if st := pr.Status(); !st.Success() {
				failed = append(failed, fmt.Sprintf("stop_cmd of %s failed (%s)", c.Project, st))
			}
		}
	}
	return ran, failed
}

// killReport describes what cleanUp did, e.g. "stopped 2 leftover
// processes (killed api after 5s), ran 1 stop_cmd".
func killReport(orphans []sessions.Orphan, forced []sessions.Child, timeout time.Duration, ran int, failed []string) string {
	n := 0
	for _, o := range orphans {
		for _, c := range o.Children {
			if !c.Gone {
				n++
			}
		}
	}
	var parts []string
	if n > 0 {
		report := fmt.Sprintf("stopped %d leftover %s", n, plural(n, "process", "processes"))
		if len(forced) > 0 {
			var names []string
			for _, c := range forced {
				names = append(names, c.Project)
			}
			report += fmt.Sprintf(" (killed %s after %v)", strings.Join(names, ", "), timeout)
		}
		parts = append(parts, report)
	}
	if ran > 0 {
		parts = append(parts, fmt.Sprintf("ran %d %s", ran, plural(ran, "stop_cmd", "stop_cmds")))
	}
	report := strings.Join(parts, ", ")
	if len(failed) > 0 {
		report = strings.Join(append([]string{report}, failed...), "; ")
	}
	return report
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
