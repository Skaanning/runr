package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Skaanning/runr/internal/supervisor"
)

// emitError appends an error line to the session's logs and returns its
// event, like the supervisor does for a line matching error_pattern.
func emitError(sess *fakeSession, project, text string) supervisor.Event {
	return supervisor.LineEvent{Line: sess.logs.AppendLine(supervisor.LogLine{Project: project, Text: text, Error: true})}
}

// errorModel is a running view whose db printed lines 0-11, errors at 1, 5
// and 9, and api one error after them.
func errorModel(t *testing.T, width, height int) (*runningModel, *fakeSession) {
	m, sess := newTestModel(t, width, height)
	var events []supervisor.Event
	for i := range 12 {
		if i%4 == 1 {
			events = append(events, emitError(sess, "db", fmt.Sprint("line ", i, " ERROR")))
		} else {
			events = append(events, emit(sess, "db", fmt.Sprint("line ", i)))
		}
	}
	events = append(events, emitError(sess, "api", "panic: boom"),
		supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: "db", State: supervisor.Running, Errors: 3}},
		supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: "api", State: supervisor.Exited, Errors: 1}})
	m.Update(eventsMsg{events: events})
	return m, sess
}

func TestRunningErrors(t *testing.T) {
	m, _ := errorModel(t, 80, 12) // 8 log rows: db's lines 5-11 and api's
	rows := screen(m)
	for _, want := range []string{"││▌db     line 5 ERROR", "││ db     line 6 ", "││▌db     line 9 ERROR", "││▌api    panic: boom", "│ ▌  3 errors ", "│    1 error "} {
		if !slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, want) }) {
			t.Errorf("no %q in:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	red, yellow := m.st.err.Render("▌"), m.st.warn.Render("▌")
	if out := m.render(); strings.Count(out, red) != 3 || strings.Contains(out, yellow) {
		t.Errorf("markers: %d red, %d yellow", strings.Count(out, red), strings.Count(out, yellow))
	}

	m.Update(key("e")) // from the top of the screen
	if got := helpRow(m); !strings.HasPrefix(got, " error 2/4  e/E  next/prev error  esc  clear  ") {
		t.Errorf("help = %q", got)
	}
	if out := m.render(); strings.Count(out, yellow) != 1 || !strings.Contains(out, yellow+m.styleOf("db").Render("db    ")+" line 5") {
		t.Errorf("not marked as the current error:\n%q", out)
	}
	if !strings.Contains(screen(m)[0], "paused") {
		t.Errorf("still following: %q", screen(m)[0])
	}

	m.Update(key("e"))
	m.Update(key("e"))
	if got := helpRow(m); !strings.HasPrefix(got, " error 4/4") {
		t.Errorf("help = %q", got)
	}
	m.Update(key("e"))
	if got := helpRow(m); got != " errors wrapped to the top" {
		t.Errorf("help = %q", got)
	}
	if !slices.Contains(logRows(m), "▌db     line 1 ERROR") {
		t.Errorf("line 1 not revealed: %q", logRows(m))
	}
	m.Update(key("E"))
	if got := helpRow(m); got != " errors wrapped to the bottom" {
		t.Errorf("help = %q", got)
	}

	// With a search, both show.
	m.Update(key("/"))
	typeText(m, "line")
	m.Update(key("enter"))
	m.Update(key("e"))
	if got := helpRow(m); !strings.HasPrefix(got, " /line 12/12  error 4/4  n/N  next/prev  e/E  next/prev error  ") {
		t.Errorf("help = %q", got)
	}
	m.Update(key("esc"))
	if got := helpRow(m); strings.Contains(got, "error") || strings.Contains(m.render(), yellow) {
		t.Errorf("esc did not clear: %q", got)
	}
}

func TestRunningErrorsSingleView(t *testing.T) {
	m, _ := errorModel(t, 80, 12)
	m.Update(key("down")) // worker: no output
	m.Update(key("down"))
	m.Update(key("tab"))
	m.Update(key("e"))
	if m.notice != "no errors in this view" {
		t.Errorf("notice = %q", m.notice)
	}

	m.Update(key("up")) // api
	m.Update(key("E"))
	if got := helpRow(m); !strings.HasPrefix(got, " error 1/1") {
		t.Errorf("help = %q", got)
	}
	if !slices.Contains(logRows(m), "▌panic: boom") {
		t.Errorf("rows = %q", logRows(m))
	}

	m.Update(key("tab")) // the combined view isn't at that error
	if got := helpRow(m); strings.Contains(got, "error ") {
		t.Errorf("help = %q", got)
	}
}

func TestRunningErrorsHint(t *testing.T) {
	m, _ := newTestModel(t, 160, 12)
	if strings.Contains(helpRow(m), "e  errors") {
		t.Error("errors hint without errors")
	}
	m, _ = errorModel(t, 160, 12)
	if got := helpRow(m); !strings.Contains(got, "/  search  e  errors  ") {
		t.Errorf("help = %q", got)
	}
}

func TestLogViewMarksWrappedErrors(t *testing.T) {
	v := newLogView(100, plainFormat)
	v.setSize(4, 5)
	v.append(supervisor.LogLine{Seq: 1, Text: "ok"},
		supervisor.LogLine{Seq: 2, Text: "error!", Error: true}, // two rows
		supervisor.LogLine{Seq: 3, Text: "ok"})
	if got := v.rowMarks(); !slices.Equal(got, []rowMark{noMark, errorMark, errorMark, noMark, noMark}) {
		t.Errorf("marks = %v (rows %q)", got, v.view())
	}
	v.current = 2
	v.invalidate()
	v.setSize(4, 2) // from the bottom: the error's last row and "ok"
	if got := v.rowMarks(); !slices.Equal(got, []rowMark{currentError, noMark}) {
		t.Errorf("marks = %v (rows %q)", got, v.view())
	}
	v.scrollTop()
	if got := v.rowMarks(); !slices.Equal(got, []rowMark{noMark, currentError}) {
		t.Errorf("from the top: marks = %v (rows %q)", got, v.view())
	}
}
