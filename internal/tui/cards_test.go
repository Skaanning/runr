package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Skaanning/runr/internal/config"
)

// cardsConfig has groups g0…g(groups-1), each with project p0, and the
// projects p0…p(projects-1).
func cardsConfig(t *testing.T, groups, projects int) *config.Config {
	cfg := &config.Config{Projects: map[string]*config.Project{}, Groups: map[string][]string{}}
	for i := range projects {
		cfg.Projects[fmt.Sprintf("p%02d", i)] = &config.Project{Dir: t.TempDir(), Cmd: "x"}
	}
	for i := range groups {
		cfg.Groups[fmt.Sprintf("g%d", i)] = []string{"p00"}
	}
	return cfg
}

func newCardsLauncher(t *testing.T, cfg *config.Config, width, height int) *launcherModel {
	m := newLauncherModel(cfg, nil, newStyles(terminalTheme))
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func TestLauncherWordmark(t *testing.T) {
	m := newCardsLauncher(t, launcherConfig(t), 96, 20)
	rows := launcherScreen(m)
	want := []string{
		"",
		"   ┏━┓╻ ╻┏┓╻┏━┓",
		"   ┣┳┛┃ ┃┃┗┫┣┳┛   3 projects · 2 groups",
		"   ╹┗╸┗━┛╹ ╹╹┗╸",
		"",
		"   Groups",
	}
	if got := strings.Join(rows[:6], "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("top:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}

	// Shorter terminals get the header instead; forms have their pane's title.
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 19})
	if got := launcherScreen(m)[0]; got != "  runr" {
		t.Fatalf("header at 19 rows = %q", got)
	}
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 30})
	m.Update(key("n"))
	if got := launcherScreen(m)[0]; !strings.HasPrefix(got, "╭─ new project ─") {
		t.Fatalf("top row with a form = %q", got)
	}
}

func TestWordmarkGradient(t *testing.T) {
	st := newStyles(catppuccin)
	if st.wordmark[0].GetForeground() != catppuccin.dark.accent || st.wordmark[3].GetForeground() != catppuccin.dark.ansi[ansiMagenta] {
		t.Error("the gradient doesn't go from the accent to magenta")
	}
	if st.wordmark[1].GetForeground() != catppuccin.dark.gradient[0] {
		t.Error("the gradient's middle colors aren't used")
	}
	st.setProfile(colorprofile.ANSI)
	for i, s := range st.wordmark {
		if s.GetForeground() != st.pal.accent {
			t.Errorf("16 colors: letter %d isn't in the accent", i)
		}
	}
}

func TestLauncherCardMovement(t *testing.T) {
	// At 96 columns two cards fit in a row: g0 g1 / g2 g3 / g4.
	m := newCardsLauncher(t, cardsConfig(t, 5, 2), 96, 30)
	at := func(want string) {
		t.Helper()
		if it, _ := m.current(); it.name != want {
			t.Fatalf("cursor on %q, want %q", it.name, want)
		}
	}
	for _, step := range []struct{ key, want string }{
		{"left", "g0"}, {"right", "g1"}, {"right", "g1"}, {"down", "g3"},
		{"left", "g2"}, {"down", "g4"}, {"right", "g4"}, {"down", "p00"},
		{"up", "g4"}, {"up", "g2"}, {"right", "g3"}, {"down", "g4"},
		{"up", "g2"}, {"up", "g0"}, {"up", "g0"},
		{"right", "g1"}, {"down", "g3"}, {"down", "g4"}, {"down", "p00"},
		{"down", "p01"}, {"down", "p01"}, {"left", "p01"}, {"up", "p00"},
	} {
		m.Update(key(step.key))
		at(step.want)
	}

	// From the last row the cursor comes back to the column it left from.
	m = newCardsLauncher(t, cardsConfig(t, 2, 1), 96, 30)
	for _, step := range []struct{ key, want string }{
		{"right", "g1"}, {"down", "p00"}, {"up", "g1"}, {"left", "g0"}, {"down", "p00"}, {"up", "g0"},
	} {
		m.Update(key(step.key))
		at(step.want)
	}
}

func TestLauncherCards(t *testing.T) {
	cfg := cardsConfig(t, 3, 1)
	cfg.Groups["g2"] = nil
	m := newCardsLauncher(t, cfg, 96, 16)
	m.Update(key("right")) // g1
	rows := launcherScreen(m)[2:9]
	want := []string{
		"   Groups",
		"   ╭─ g0 ───────────────────────────── 1 ─╮  ╭─ g1 ───────────────────────────── 1 ─╮",
		"   │ [ ] ● p00                            │  │ [ ] ● p00                            │",
		"   ╰──────────────────────────────────────╯  ╰──────────────────────────────────────╯",
		"   ╭─ g2 ───────────────────────────── 0 ─╮",
		"   │ [ ] (empty)                          │",
		"   ╰──────────────────────────────────────╯",
	}
	if got := strings.Join(rows, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("cards:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if !strings.Contains(m.render(), m.st.accent.Render("╭─ ")) {
		t.Error("the card under the cursor has no accent border")
	}

	// Without colors the card under the cursor has a thick border.
	m.Update(tea.ColorProfileMsg{Profile: colorprofile.Ascii})
	if got := launcherScreen(m)[3]; !strings.Contains(got, "╭─ g0 ─") || !strings.Contains(got, "┏━ g1 ━") {
		t.Fatalf("row = %q", got)
	}

	// Long member lists wrap; the other cards of the row grow with them.
	cfg.Groups["g0"] = []string{"p00", "p00", "p00", "p00", "p00"}
	m.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	rows = launcherScreen(m)[3:7]
	want = []string{
		"   ╭─ g0 ───────────────────────────── 5 ─╮  ╭─ g1 ───────────────────────────── 1 ─╮",
		"   │ [ ] ● p00  ● p00  ● p00  ● p00       │  │ [ ] ● p00                            │",
		"   │     ● p00                            │  │                                      │",
		"   ╰──────────────────────────────────────╯  ╰──────────────────────────────────────╯",
	}
	if got := strings.Join(rows, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("wrapped card:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}

	// Narrow terminals get one narrower card per row.
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 16})
	rows = launcherScreen(m)[3:8]
	want = []string{
		"   ╭─ g0 ─────────────── 5 ─╮",
		"   │ [ ] ● p00  ● p00       │",
		"   │     ● p00  ● p00       │",
		"   │     ● p00              │",
		"   ╰────────────────────────╯",
	}
	if got := strings.Join(rows, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("narrow card:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestLauncherWithoutGroupsOrProjects(t *testing.T) {
	// Only projects: no cards, and up/down as before.
	m := newCardsLauncher(t, cardsConfig(t, 0, 2), 96, 20)
	if rows := launcherScreen(m); rows[5] != "   Projects" || !strings.HasPrefix(rows[6], " ▸ [ ] ● p00") || !strings.HasPrefix(rows[len(rows)-1], "  ↑↓  move ") {
		t.Fatalf("screen:\n%s", strings.Join(rows, "\n"))
	}
	m.Update(key("down"))
	m.Update(key("left"))
	if it, _ := m.current(); it.name != "p01" {
		t.Fatalf("cursor on %q", it.name)
	}

	// Only groups (all empty): no projects heading, and down stays on the cards.
	cfg := &config.Config{Groups: map[string][]string{"a": nil, "b": nil, "c": nil}}
	m = newCardsLauncher(t, cfg, 96, 20)
	if got := strings.Join(launcherScreen(m), "\n"); strings.Contains(got, "Projects") || !strings.Contains(got, "╭─ c ") {
		t.Fatalf("screen:\n%s", got)
	}
	for _, k := range []string{"down", "down", "right"} {
		m.Update(key(k))
	}
	if it, _ := m.current(); it.name != "c" {
		t.Fatalf("cursor on %q, want c", it.name)
	}
}

func TestLauncherScrollsWithCards(t *testing.T) {
	// 30 projects in a 20-row terminal: 13 body rows under the wordmark.
	m := newCardsLauncher(t, cardsConfig(t, 3, 30), 96, 20)
	body := func() []string { return launcherScreen(m)[5:18] }
	for range 40 {
		m.Update(key("down"))
	}
	if rows := body(); !strings.HasPrefix(rows[12], " ▸ [ ] ● p29") {
		t.Fatalf("body:\n%s", strings.Join(rows, "\n"))
	}
	for range 29 {
		m.Update(key("up")) // p00: the projects heading stays in view
	}
	if rows := body(); rows[0] != "   Projects" || !strings.HasPrefix(rows[1], " ▸ [ ] ● p00") {
		t.Fatalf("body:\n%s", strings.Join(rows, "\n"))
	}
	m.Update(key("up")) // g2, the whole card in view
	if rows := body(); !strings.HasPrefix(rows[0], "   ╭─ g2 ") || !strings.HasPrefix(rows[2], "   ╰─") {
		t.Fatalf("body:\n%s", strings.Join(rows, "\n"))
	}
	m.Update(key("up")) // g0: the groups heading too
	if rows := body(); rows[0] != "   Groups" {
		t.Fatalf("body:\n%s", strings.Join(rows, "\n"))
	}
}
