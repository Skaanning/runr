package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestNamedColorsResolveThroughPalette(t *testing.T) {
	var p palette
	for i := range p.ansi {
		p.ansi[i] = color.RGBA{R: uint8(i), A: 0xff} // distinct from any terminal color
	}
	st := newStyles(theme{dark: p, light: p})
	if len(config.ColorNames) != len(p.ansi) {
		t.Fatalf("%d color names for %d palette colors", len(config.ColorNames), len(p.ansi))
	}
	for i, name := range config.ColorNames {
		if got := st.color(name); got != p.ansi[i] {
			t.Errorf("color(%q) = %v, want palette color %d", name, got, i)
		}
	}
	// Numbers and hex codes are used as is.
	for _, c := range []string{"3", "208", "#ff8800", "#f80"} {
		if got, want := st.color(c), lipgloss.Color(c); got != want {
			t.Errorf("color(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestTerminalThemeKeepsANSIColors(t *testing.T) {
	st := newStyles(terminalTheme)
	for _, name := range config.ColorNames {
		if got, want := st.color(name), lipgloss.Color(config.ColorCode(name)); got != want {
			t.Errorf("color(%q) = %v, want %v", name, got, want)
		}
	}
	for _, tt := range []struct{ name, got, want string }{
		{"system", st.system, "\x1b[2;3m"},
		{"systemErr", st.systemErr, "\x1b[31;3m"},
		{"match", st.match, "\x1b[7m"},
		{"current", st.current, "\x1b[30;43m"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestStateCounts(t *testing.T) {
	st := newStyles(terminalTheme)
	states := []supervisor.State{
		supervisor.Running, supervisor.Exited, // ●
		supervisor.Pending, supervisor.Waiting, supervisor.Starting, supervisor.Stopping, // ◐
		supervisor.Crashed, supervisor.Blocked, // ✗
		supervisor.Stopped, // not counted
	}
	if got := ansi.Strip(st.stateCounts(states)); got != "● 2  ◐ 4  ✗ 2" {
		t.Errorf("counts = %q", got)
	}
	if got := ansi.Strip(st.stateCounts([]supervisor.State{supervisor.Crashed, supervisor.Stopped})); got != "✗ 1" {
		t.Errorf("counts = %q, want zeros left out", got)
	}
}

func TestHeaderDropsCountsWhenNarrow(t *testing.T) {
	m, _ := newTestModel(t, 60, 7)
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running)}})
	if got := screen(m)[0]; got != "  runr   backend            ● 1  ◐ 2   combined · following" {
		t.Errorf("header = %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 7})
	if got := screen(m)[0]; got != "  runr   backend   combined · following" {
		t.Errorf("narrow header = %q", got)
	}
}

func TestSelectionBar(t *testing.T) {
	m, _ := newTestModel(t, 60, 7)
	m.Update(eventsMsg{events: []supervisor.Event{state("db", supervisor.Running)}})
	row := strings.Split(m.render(), "\n")[2]
	side, _, _ := strings.Cut(row, "│")
	// The background covers the whole sidebar row, also after inner resets.
	if !strings.HasPrefix(side, "\x1b[48;5;236m") || strings.Count(side, "\x1b[m\x1b[48;5;236m") < 3 {
		t.Errorf("selected row = %q", side)
	}
	if strings.Contains(strings.Split(m.render(), "\n")[3], "48;5;236") {
		t.Error("unselected row has the selection background")
	}

	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if row := strings.Split(m.render(), "\n")[2]; !strings.HasPrefix(row, "\x1b[48;5;254m") {
		t.Errorf("light terminal: selected row = %q", row)
	}
}

func TestSixteenColorFallbacks(t *testing.T) {
	m, _ := newTestModel(t, 60, 7)
	m.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	out := m.render()
	rows := strings.Split(out, "\n")
	if strings.Contains(out, "48;5;") || strings.Contains(out, "38;5;") {
		t.Errorf("256-color sequences on a 16-color terminal: %q", out)
	}
	if !strings.Contains(rows[2], "\x1b[1;7;36mdb") { // bold, reverse, cyan
		t.Errorf("selected row = %q, want the name in reverse video", rows[2])
	}
	if !strings.HasPrefix(rows[1], "\x1b[2m─") {
		t.Errorf("rule = %q, want faint", rows[1])
	}
	if got := helpRow(m); got != " ↑↓ select  tab view  r restart  s start  + start  q quit" {
		t.Errorf("help = %q, want bold keys without caps", got)
	}
	if !strings.Contains(rows[0], "\x1b[1;30;46m runr ") {
		t.Errorf("header = %q, want the badge in the accent color", rows[0])
	}

	m.Update(tea.ColorProfileMsg{Profile: colorprofile.ASCII})
	if rows := strings.Split(m.render(), "\n"); !strings.Contains(rows[0], "\x1b[1;7m runr ") {
		t.Errorf("header = %q, want the badge in reverse video", rows[0])
	}
}

func TestFailureLinesAreRed(t *testing.T) {
	m, _ := newTestModel(t, 60, 7)
	m.Update(eventsMsg{events: []supervisor.Event{
		system("db", "starting: db"),
		supervisor.LineEvent{Line: supervisor.LogLine{Project: "db", Stream: supervisor.SystemError, Text: "crashed (exit code 1)"}},
	}})
	out := m.render()
	if !strings.Contains(out, m.st.system+"starting: db") || !strings.Contains(out, m.st.systemErr+"crashed (exit code 1)") {
		t.Errorf("render = %q", out)
	}
}

func TestLauncherBadgeAndKeycaps(t *testing.T) {
	m := newTestLauncher(t, launcherConfig(t))
	rows := strings.Split(m.render(), "\n")
	if !strings.HasPrefix(rows[0], " \x1b[1;30;46m runr \x1b[m") {
		t.Errorf("header = %q", rows[0])
	}
	if !strings.Contains(rows[len(rows)-1], "\x1b[1;48;5;236m q \x1b[m") {
		t.Errorf("help = %q", rows[len(rows)-1])
	}
}
