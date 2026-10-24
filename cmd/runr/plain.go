package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"

	"runr/internal/config"
	"runr/internal/supervisor"
)

// runPlain starts the projects and prints their output interleaved, one line
// per output line, until every project has finished. The first signal on
// sigs stops everything gracefully; a second one kills it.
func runPlain(out io.Writer, cfg *config.Config, names, projects []string, sigs <-chan os.Signal, color bool) error {
	pr := newPlainPrinter(out, cfg, projects, color)
	sup := supervisor.New(cfg, supervisor.Options{})
	if err := sup.Start(names...); err != nil {
		sup.Close()
		return err
	}

	latest := map[string]supervisor.ProcessInfo{}
	events := sup.Events()
	var stopped <-chan struct{}
	userStopped := false

loop:
	for {
		select {
		case e := <-events:
			pr.event(e)
			if se, ok := e.(supervisor.StateEvent); ok {
				latest[se.Info.Name] = se.Info
				if stopped == nil && allFinished(projects, latest) {
					break loop
				}
			}
		case <-sigs:
			if stopped == nil {
				userStopped = true
				pr.notice("stopping… (press Ctrl+C again to force)")
				stopped = sup.StopAll()
			} else {
				pr.notice("forcing shutdown")
				sup.KillAll()
			}
		case <-stopped:
			break loop
		}
	}

	sup.Close()
	for e := range events { // remaining events, e.g. final "stopped" lines
		pr.event(e)
		if se, ok := e.(supervisor.StateEvent); ok {
			latest[se.Info.Name] = se.Info
		}
	}

	if userStopped {
		return nil
	}
	return failures(projects, latest)
}

// allFinished reports whether every project has reached a final state.
func allFinished(projects []string, latest map[string]supervisor.ProcessInfo) bool {
	for _, name := range projects {
		switch latest[name].State {
		case supervisor.Exited, supervisor.Crashed, supervisor.Stopped, supervisor.Blocked:
		default:
			return false // includes projects with no state event yet (Pending)
		}
	}
	return true
}

// failures returns an error listing projects that crashed or were blocked.
func failures(projects []string, latest map[string]supervisor.ProcessInfo) error {
	var failed []string
	for _, name := range projects {
		info := latest[name]
		switch info.State {
		case supervisor.Crashed:
			failed = append(failed, fmt.Sprintf("%s (%s)", name, info.Exit))
		case supervisor.Blocked:
			failed = append(failed, name+" (blocked)")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// plainPrinter formats supervisor events as prefixed lines.
type plainPrinter struct {
	w       io.Writer
	color   bool
	width   int
	labels  map[string]string // project name → padded, colored label
	noticeP string
}

func newPlainPrinter(w io.Writer, cfg *config.Config, projects []string, color bool) *plainPrinter {
	p := &plainPrinter{w: w, color: color, labels: map[string]string{}}
	for _, name := range projects {
		p.width = max(p.width, len(cfg.Projects[name].Label(name)))
	}
	p.width = max(p.width, len("runr"))

	next := 0
	for _, name := range projects {
		proj := cfg.Projects[name]
		c := proj.Color
		if c == "" {
			c = config.DefaultColors[next%len(config.DefaultColors)]
			next++
		}
		p.labels[name] = p.paint(sgrColor(config.ColorCode(c)), pad(proj.Label(name), p.width))
	}
	p.noticeP = p.paint("1", pad("runr", p.width))
	return p
}

func (p *plainPrinter) event(e supervisor.Event) {
	le, ok := e.(supervisor.LineEvent)
	if !ok {
		return
	}
	label := p.labels[le.Line.Project]
	if le.Line.Stream == supervisor.System {
		fmt.Fprintf(p.w, "%s » %s\n", label, p.paint("2", le.Line.Text))
		return
	}
	text := le.Line.Text
	if p.color {
		text += "\x1b[0m" // keep the program's colors from bleeding into the next line
	}
	fmt.Fprintf(p.w, "%s │ %s\n", label, text)
}

func (p *plainPrinter) notice(msg string) {
	fmt.Fprintf(p.w, "%s » %s\n", p.noticeP, msg)
}

// paint wraps s in an SGR sequence when color is enabled.
func (p *plainPrinter) paint(sgr, s string) string {
	if !p.color || sgr == "" {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

// sgrColor converts a color code (ANSI index or hex) to an SGR foreground parameter.
func sgrColor(code string) string {
	if strings.HasPrefix(code, "#") {
		hex := code[1:]
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		r, _ := strconv.ParseUint(hex[0:2], 16, 8)
		g, _ := strconv.ParseUint(hex[2:4], 16, 8)
		b, _ := strconv.ParseUint(hex[4:6], 16, 8)
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
	n, err := strconv.Atoi(code)
	switch {
	case err != nil:
		return ""
	case n < 8:
		return strconv.Itoa(30 + n)
	case n < 16:
		return strconv.Itoa(90 + n - 8)
	default:
		return "38;5;" + code
	}
}

func pad(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// colorEnabled reports whether w is a terminal and NO_COLOR is not set.
func colorEnabled(w io.Writer) bool {
	return os.Getenv("NO_COLOR") == "" && isTerminal(w)
}

// isTerminal reports whether f (a reader or writer) is a terminal.
func isTerminal(f any) bool {
	file, ok := f.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
