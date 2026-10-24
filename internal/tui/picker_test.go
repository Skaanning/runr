package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/gitinfo"
)

func testBranchPicker(t *testing.T) *picker {
	t.Helper()
	branches := []gitinfo.Branch{
		{Name: "main", Current: true, Upstream: "origin/main", Ahead: 1, Date: "3 days ago", Subject: "Merge pull request #42 from fix/typo"},
		{Name: "feat/login", Upstream: "origin/feat/login", Ahead: 2, Behind: 1, Date: "2 hours ago", Subject: "Add session cookie"},
		{Name: "feat/search", Date: "1 week ago", Subject: "Search index draft"},
		{Name: "origin/fix/typo", Remote: true, Date: "5 days ago", Subject: "Fix typo in README"},
		{Name: "origin/feat/billing", Remote: true, Date: "yesterday", Subject: "Billing page skeleton"},
	}
	p, _ := branchPicker(newStyles(terminalTheme), branchesMsg{branches: branches}, "api", "api · ~/code/app · main ↑1",
		[]string{"worker"}, func(gitinfo.Branch) tea.Cmd { return nil })
	return p
}

// plainRows strips the styling and trailing spaces from rows.
func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(ansi.Strip(r), " ")
	}
	return out
}

func TestBranchPickerView(t *testing.T) {
	p := testBranchPicker(t)
	p.update(key("down")) // feat/login
	got := plainRows(p.view(76, 16))
	want := []string{
		"api · ~/code/app · main ↑1                            also switches: worker",
		"",
		"›   type to filter                                                5 branches",
		"",
		"Local",
		" ● main          ↑1     Merge pull request #42 from fi…  current  3 days ago",
		"▌  feat/login    ↑2 ↓1  Add session cookie                       2 hours ago",
		"   feat/search          Search index draft                        1 week ago",
		"",
		"Remote",
		"   fix/typo             Fix typo in README          from origin · 5 days ago",
		"   feat/billing         Billing page skeleton        from origin · yesterday",
		"",
		"",
		"",
		"$ git switch feat/login",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("picker:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i, r := range p.view(76, 16) {
		if w := ansi.StringWidth(r); w > 76 {
			t.Errorf("row %d is %d cells", i, w)
		}
	}
	if s := p.st; !strings.Contains(strings.Join(p.view(76, 16), "\n"), s.pill(" current ", s.pal.accent)) {
		t.Error("no current pill")
	}

	p.update(key("up"))
	if got := plainRows(p.view(76, 16))[15]; got != "already on main" {
		t.Errorf("footer on the current branch = %q", got)
	}
}

func TestPickerNarrowDropsInfo(t *testing.T) {
	p := testBranchPicker(t)
	rows := plainRows(p.view(46, 16))
	if rows[5] != "▌● main          ↑1        current  3 days ago" {
		t.Errorf("row = %q, want the subject dropped", rows[5])
	}
	if rows[7] != "   feat/search          Search ind… 1 week ago" {
		t.Errorf("row = %q, want the subject cut", rows[7])
	}
}

func TestPickerShortPane(t *testing.T) {
	p := testBranchPicker(t)
	for range 3 {
		p.update(key("down")) // fix/typo
	}
	// Blank lines go first, then the footer and the context line.
	for _, tt := range []struct {
		height int
		want   []string
	}{
		{6, []string{"api · ~/code/app · main ↑1", "filter", "Remote", "fix/typo", "feat/billing", "$ git switch --track origin/fix/typo"}},
		{4, []string{"api · ~/code/app · main ↑1", "filter", "fix/typo", "$ git switch --track origin/fix/typo"}},
		{3, []string{"api · ~/code/app · main ↑1", "filter", "fix/typo"}},
		{2, []string{"filter", "fix/typo"}},
		{1, []string{"filter"}},
	} {
		rows := plainRows(p.view(60, tt.height))
		if len(rows) != tt.height {
			t.Fatalf("height %d: %d rows", tt.height, len(rows))
		}
		for i, w := range tt.want {
			if w == "filter" {
				w = "›"
			}
			if !strings.Contains(rows[i], w) {
				t.Errorf("height %d: row %d = %q, want %q", tt.height, i, rows[i], w)
			}
		}
	}
	// At the minimum size everything still fits.
	for i, r := range p.view(minWidth-7, 2) {
		if w := ansi.StringWidth(r); w > minWidth-7 {
			t.Errorf("row %d is %d cells", i, w)
		}
	}
}

func TestPickerFilterSections(t *testing.T) {
	p := testBranchPicker(t)
	for _, k := range []string{"f", "e", "a", "t"} {
		p.update(key(k))
	}
	rows := plainRows(p.view(76, 16))
	if !strings.HasSuffix(rows[2], " 3/5") {
		t.Errorf("count = %q", rows[2])
	}
	if rows[4] != "Local" || !strings.HasPrefix(rows[5], "▌ feat/login") || rows[7] != "" || rows[8] != "Remote" || !strings.HasPrefix(rows[9], "  feat/billing") {
		t.Errorf("rows:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(strings.Join(p.view(76, 16), "\n"), p.st.accent.Bold(true).Render("feat")) {
		t.Error("the match is not highlighted")
	}
	p.update(key("b")) // "featb": no matches, no headings
	rows = plainRows(p.view(76, 16))
	if rows[4] != "no matches" || strings.Contains(strings.Join(rows, "\n"), "Local") {
		t.Errorf("rows:\n%s", strings.Join(rows, "\n"))
	}
}

func TestPickerScrollsWithHeadings(t *testing.T) {
	p := testBranchPicker(t)
	// 6 rows: filter, a blank line and 4 list rows.
	p.context, p.footer = "", nil
	for range 4 {
		p.update(key("down")) // feat/billing
	}
	rows := plainRows(p.view(60, 6))
	if rows[3] != "Remote" || !strings.HasPrefix(rows[5], "▌  feat/billing") {
		t.Fatalf("rows:\n%s", strings.Join(rows, "\n"))
	}
	for range 4 {
		p.update(key("up")) // main: the Local heading in view again
	}
	if rows := plainRows(p.view(60, 6)); rows[2] != "Local" {
		t.Fatalf("rows:\n%s", strings.Join(rows, "\n"))
	}
}

func TestProcessesFadeWhilePicking(t *testing.T) {
	m, _ := newPickerModel(t)
	if !strings.Contains(m.render(), m.st.accent.Bold(true).Render("processes")) {
		t.Fatal("the processes pane isn't in the accent")
	}
	m.Update(key("+"))
	out := m.render()
	if !strings.Contains(out, m.st.faint.Render("processes")) || !strings.Contains(out, m.st.heading.Render("start")) {
		t.Error("the processes pane doesn't fade while the picker is open")
	}
}
