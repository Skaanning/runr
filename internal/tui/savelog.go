package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/logfile"
	"github.com/Skaanning/runr/internal/supervisor"
)

// savedLogMsg reports that a log was written to path (or why not).
type savedLogMsg struct {
	owner *runningModel
	path  string
	lines int
	err   error
}

// saveLog writes the log on screen, the combined one or the selected
// project's, to runr-<name|combined>-<YYYYMMDD-HHMMSS>.log in the directory
// runr was started from (saveDir in tests). The lines are taken now and
// written in the background, in plain text (see logfile.Format); combined
// lines start with the project.
func (m *runningModel) saveLog() tea.Cmd {
	name, lines := "combined", []supervisor.LogLine(nil)
	labels := map[string]string{} // by project, padded to align
	if m.single != nil {
		name, lines = m.singleFor, m.sess.Logs().Lines(m.singleFor)
	} else {
		lines = m.sess.Logs().Combined()
		width := 0
		for _, l := range lines {
			if _, ok := labels[l.Project]; !ok {
				labels[l.Project] = m.labelOf(l.Project)
				width = max(width, ansi.StringWidth(labels[l.Project]))
			}
		}
		for p, label := range labels {
			labels[p] = pad(label, width)
		}
	}
	path := filepath.Join(m.saveDir, fmt.Sprintf("runr-%s-%s.log", name, m.now().Format("20060102-150405")))
	return func() tea.Msg {
		return savedLogMsg{owner: m, path: path, lines: len(lines), err: writeLog(path, lines, labels)}
	}
}

// writeLog writes lines to a new file at path, each with its project's
// label (none when labels has none).
func writeLog(path string, lines []supervisor.LogLine, labels map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, l := range lines {
		w.WriteString(logfile.Format(l.Time, labels[l.Project], l.Stream.Runr(), l.Text))
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// savedNotice is the footer's report on a saved log. The path is relative
// to the directory runr was started from, so the file name isn't cut off.
func savedNotice(msg savedLogMsg) string {
	if msg.err != nil {
		return "can't save the log: " + msg.err.Error()
	}
	return fmt.Sprintf("saved %d lines to %s", msg.lines, config.DisplayPath(msg.path))
}
