package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

// dirTree creates root/Projects with the folders billing-api (a git
// repository), billing-old, customer-portal and .hidden, and a file.
func dirTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"billing-api/.git", "billing-old", "customer-portal", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, "Projects", filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Projects", "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSubdirs(t *testing.T) {
	root := dirTree(t)
	dirs, err := subdirs(filepath.Join(root, "Projects"), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []subdir{{".hidden", false}, {"billing-api", true}, {"billing-old", false}, {"customer-portal", false}}
	if !slices.Equal(dirs, want) {
		t.Fatalf("dirs = %v, want %v", dirs, want)
	}
	if _, err := subdirs(filepath.Join(root, "nope"), false); err == nil {
		t.Fatal("no error for a missing directory")
	}
}

func TestTypedParent(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tt := range []struct{ value, parent, sep string }{
		{"~/Projects/ru", "~/Projects/", "/"},
		{"~/Projects/", "~/Projects/", "/"},
		{"/", "/", "/"},
		{"proj", "", sep},
		{"", "", sep},
	} {
		if parent, s := typedParent(tt.value); parent != tt.parent || s != tt.sep {
			t.Errorf("%q: %q, %q; want %q, %q", tt.value, parent, s, tt.parent, tt.sep)
		}
	}
}

// focusDir moves the focus of the open project form to the directory field
// and replaces its value with dir, as pasted.
func focusDir(t *testing.T, m *launcherModel, dir string) *formField {
	t.Helper()
	f := m.form.form
	for f.focused().key != "dir" {
		m.Update(key("tab"))
	}
	fd := f.focused()
	fd.setValue("")
	pasteDir(t, m, dir)
	return fd
}

// pasteDir pastes s into the focused directory field and lists the folders
// for its completion, without waiting for the directory check.
func pasteDir(t *testing.T, m *launcherModel, s string) {
	t.Helper()
	m.Update(tea.PasteMsg{Content: s})
	pf := projectFormOf(t, m)
	pf.listAsked = false
	runCmds(m, pf.listParent())
}

func TestProjectFormCompletesDir(t *testing.T) {
	root := dirTree(t)
	projects := filepath.Join(root, "Projects") + string(filepath.Separator)
	m := openProjectForm(t, launcherConfig(t), "", nil, 200, 30)
	fd := focusDir(t, m, projects+"BI")
	if !strings.Contains(screenText(m), "› "+projects+"BIlling-api"+string(filepath.Separator)) {
		t.Fatalf("no completion shown:\n%s", screenText(m))
	}
	if help := launcherScreen(m)[29]; !strings.Contains(help, "→  complete  ctrl+o  browse") {
		t.Errorf("help = %q", help)
	}
	m.Update(key("right"))
	if want := projects + "billing-api" + string(filepath.Separator); *fd.str != want {
		t.Fatalf("dir = %q, want %q", *fd.str, want)
	}

	// Hidden folders once a "." is typed; nothing for no match.
	fd = focusDir(t, m, projects)
	if got := (projectFormOf(t, m)).completion(); got != projects+"billing-api"+string(filepath.Separator) {
		t.Fatalf("completion = %q", got)
	}
	pasteDir(t, m, ".")
	if got := projectFormOf(t, m).completion(); got != projects+".hidden"+string(filepath.Separator) {
		t.Fatalf("completion = %q", got)
	}
	pasteDir(t, m, "x")
	if got := projectFormOf(t, m).completion(); got != "" {
		t.Fatalf("completion = %q", got)
	}

	// → away from the end moves the cursor instead.
	fd = focusDir(t, m, projects+"cu")
	m.Update(key("left"))
	if strings.Contains(screenText(m), "customer-portal") {
		t.Fatalf("completion shown with the cursor inside:\n%s", screenText(m))
	}
	m.Update(key("right"))
	if *fd.str != projects+"cu" {
		t.Fatalf("dir = %q", *fd.str)
	}
	m.Update(key("right")) // at the end now
	if *fd.str != projects+"customer-portal"+string(filepath.Separator) {
		t.Fatalf("dir = %q", *fd.str)
	}
}

func TestGhostView(t *testing.T) {
	f := newForm(newStyles(terminalTheme))
	for _, tt := range []struct {
		value, ghost string
		width        int
		want         string
	}{
		{"~/Pro", "jects/", 20, "~/Projects/"},
		{"~/a/very/long/path/b", "illing-api/", 16, "g/path/billing-a"}, // half the width for the value
		{"~/a/b", "cdefghijklmnop/", 10, "~/a/bcdefg"},
	} {
		if got := ansi.Strip(f.ghostView(tt.value, tt.ghost, tt.width)); got != tt.want {
			t.Errorf("%q + %q in %d: %q, want %q", tt.value, tt.ghost, tt.width, got, tt.want)
		}
	}
}

func TestDirBrowser(t *testing.T) {
	root := dirTree(t)
	projects := filepath.Join(root, "Projects")
	m := openProjectForm(t, launcherConfig(t), "", nil, 100, 30)
	fd := focusDir(t, m, filepath.Join(projects, "missing", "deeper"))

	// It starts at the nearest folder that exists, on the first folder in it.
	m.Update(key("ctrl+o"))
	if m.picker == nil {
		t.Fatal("no browser open")
	}
	text := screenText(m)
	for _, want := range []string{
		"choose directory", "ctrl+o use this folder", "5 folders",
		"  ..", "▌ billing-api" + string(filepath.Separator), "git", "Hidden", ".hidden",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "notes.txt") || strings.Contains(text, ".git"+string(filepath.Separator)) {
		t.Errorf("a file or .git listed:\n%s", text)
	}
	if !strings.Contains(launcherScreen(m)[28], "BROWSE") || !strings.Contains(launcherScreen(m)[29], "backspace  up") {
		t.Errorf("status bar and help:\n%s", strings.Join(launcherScreen(m)[28:], "\n"))
	}

	// Filter (a paste goes to the filter too) and open; backspace goes back
	// up, to the folder it came from.
	m.Update(tea.PasteMsg{Content: "cu"})
	typeInto(m, "st")
	if *fd.str != filepath.Join(projects, "missing", "deeper") || m.picker.input.Value() != "cust" {
		t.Fatalf("dir %q, filter %q", *fd.str, m.picker.input.Value())
	}
	m.Update(key("enter"))
	if !strings.Contains(screenText(m), config.DisplayPath(filepath.Join(projects, "customer-portal"))) {
		t.Fatalf("not in customer-portal:\n%s", screenText(m))
	}
	m.Update(key("backspace"))
	if it, _ := m.picker.selected(); it.name != filepath.Join(projects, "customer-portal") {
		t.Fatalf("cursor on %q", it.name)
	}

	// esc goes back to the form unchanged; ctrl+o uses the folder shown.
	m.Update(key("esc"))
	if m.picker != nil || m.form == nil || *fd.str != filepath.Join(projects, "missing", "deeper") {
		t.Fatalf("after esc: picker %v, form %v, dir %q", m.picker != nil, m.form != nil, *fd.str)
	}
	m.Update(key("ctrl+o"))
	m.Update(key("enter")) // billing-api
	m.Update(key("ctrl+o"))
	if m.picker != nil || *fd.str != config.DisplayPath(filepath.Join(projects, "billing-api")) {
		t.Fatalf("picker %v, dir %q", m.picker != nil, *fd.str)
	}
	if pf := projectFormOf(t, m); pf.wanted != pf.checkKey() {
		t.Error("no directory check scheduled")
	}
}
