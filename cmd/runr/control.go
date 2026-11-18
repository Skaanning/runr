package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/control"
	"github.com/Skaanning/runr/internal/logfile"
	"github.com/Skaanning/runr/internal/tui"
)

// controlDir is where sessions put their control sockets, or "" if runr's
// state directory can't be found (then sessions can't be reached).
func controlDir() string {
	dir, err := control.Dir()
	if err != nil {
		return ""
	}
	return dir
}

// serveControl lets runr commands in other terminals reach sess, which was
// started with titles, until the returned function is called. Nothing
// happens if the socket can't be created.
func serveControl(sess recordingSession, cfg *config.Config, titles []string) (stop func()) {
	dir := controlDir()
	if dir == "" {
		return func() {}
	}
	labels := map[string]string{} // copied: the launcher's forms change cfg
	for name, p := range cfg.Projects {
		labels[name] = p.Label(name)
	}
	srv, err := control.Listen(dir, sess, control.Info{
		Pid:     os.Getpid(),
		Started: time.Now(),
		Titles:  append([]string(nil), titles...),
		Labels:  labels,
	})
	if err != nil {
		return func() {}
	}
	return srv.Close
}

// sessionFlag adds --session, for choosing between several sessions.
func sessionFlag(cmd *cobra.Command, pid *int) {
	cmd.Flags().IntVar(pid, "session", 0, "the session to use, by runr's pid (see runr status), when several run")
}

// runningSessions returns the running sessions; problems reaching some are
// reported on stderr.
func runningSessions(stderr io.Writer) []control.Session {
	found, problems := control.Sessions(controlDir())
	for _, p := range problems {
		fmt.Fprintf(stderr, "runr: warning: %v\n", p)
	}
	return found
}

// expandGroups replaces the groups among names by their projects, as far
// as the config can be read.
func expandGroups(names []string) []string {
	s, err := loadStore()
	if err != nil {
		return names
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		members := []string{n}
		if g, ok := s.cfg.Groups[n]; ok && s.cfg.Projects[n] == nil {
			members = g
		}
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	var pid int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the processes of the running sessions",
		Long: "Show every runr session running in another terminal, and the state of its\n" +
			"processes. --json prints the same as JSON, for scripts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			found := runningSessions(cmd.ErrOrStderr())
			if pid != 0 {
				s, err := control.Pick(found, nil, pid)
				if err != nil {
					return err
				}
				found = []control.Session{s}
			}
			if len(found) == 0 {
				return control.ErrNoSession
			}
			if asJSON {
				return printStatusJSON(cmd.OutOrStdout(), found)
			}
			printStatus(cmd.OutOrStdout(), found, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	sessionFlag(cmd, &pid)
	return cmd
}

func printStatusJSON(w io.Writer, sessions []control.Session) error {
	out := make([]control.Status, len(sessions))
	for i, s := range sessions {
		out[i] = s.Status
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// printStatus prints each session's header and a table of its processes.
func printStatus(w io.Writer, sessions []control.Session, now time.Time) {
	for i, s := range sessions {
		if i > 0 {
			fmt.Fprintln(w)
		}
		st := s.Status
		header := "session " + strconv.Itoa(st.Pid)
		if len(st.Titles) > 0 {
			header += " · " + strings.Join(st.Titles, ", ")
		}
		fmt.Fprintf(w, "%s · started %s\n", header, startedAt(st.Started.Local(), now))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATE\tUPTIME\tPID\tPORTS\tERRORS\tRESTARTS")
		for _, p := range st.Processes {
			state, up, pidText, portsText := p.State, "-", "-", "-"
			if p.Exit != "" {
				state += " (" + p.Exit + ")"
			}
			if p.Pid != 0 {
				up, pidText = tui.Uptime(now.Sub(p.StartedAt)), strconv.Itoa(p.Pid)
			}
			if len(p.Ports) > 0 {
				parts := make([]string, len(p.Ports))
				for i, port := range p.Ports {
					parts[i] = ":" + strconv.Itoa(port)
				}
				portsText = strings.Join(parts, " ")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\n", p.Name, state, up, pidText, portsText, p.Errors, p.Restarts)
		}
		tw.Flush()
	}
}

// startedAt formats when a session started: the time if it was today,
// with the date otherwise.
func startedAt(t, now time.Time) string {
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func newLogsCmd() *cobra.Command {
	var lines, pid int
	var follow, errorsOnly bool
	cmd := &cobra.Command{
		Use:   "logs [name...]",
		Short: "Print the output of a running session's projects",
		Long: "Print the most recent output of projects in a runr session running in another\n" +
			"terminal: the given projects and groups, or all of them, interleaved as in the\n" +
			"combined view, in plain text. -f keeps printing new lines until Ctrl+C or the\n" +
			"session ends.",
		ValidArgsFunction: completeProjectsAndGroups,
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines < 0 {
				return errors.New("--lines can't be negative")
			}
			names := expandGroups(args)
			sess, err := control.Pick(runningSessions(cmd.ErrOrStderr()), names, pid)
			if err != nil {
				return err
			}
			labels := logLabels(sess.Status, names)
			w := bufio.NewWriter(cmd.OutOrStdout())
			req := control.Request{Op: control.OpLogs, Names: names, Tail: lines, Errors: errorsOnly, Follow: follow}
			err = control.Do(sess.Path, req, func(r control.Reply) error {
				if l := r.Line; l != nil {
					w.WriteString(logfile.Format(l.Time.Local(), labels[l.Project], l.Runr, l.Text))
					w.WriteByte('\n')
				}
				if follow {
					return w.Flush()
				}
				return nil
			})
			if ferr := w.Flush(); err == nil {
				err = ferr
			}
			if err == nil && follow {
				fmt.Fprintln(cmd.ErrOrStderr(), "runr: the session ended")
			}
			return err
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "print the last this many lines (0: all that are kept)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	cmd.Flags().BoolVar(&errorsOnly, "errors", false, "only lines that look like errors (settings.error_pattern) and crashes")
	sessionFlag(cmd, &pid)
	return cmd
}

// logLabels returns the labels for log lines by project, padded to align:
// none when there is only one project.
func logLabels(st control.Status, names []string) map[string]string {
	if len(names) == 1 || len(st.Processes) == 1 {
		return nil
	}
	labels := map[string]string{}
	width := 0
	for _, p := range st.Processes {
		labels[p.Name] = p.Label
		width = max(width, utf8.RuneCountInString(p.Label))
	}
	for name, l := range labels {
		labels[name] = pad(l, width)
	}
	return labels
}
