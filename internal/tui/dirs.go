package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

// subdir is a directory in another one.
type subdir struct {
	name string
	git  bool // it's a git repository; only looked up if asked for
}

// subdirs returns the directories in dir, symlinks to directories included,
// sorted ignoring case. repos looks up which are git repositories.
func subdirs(dir string, repos bool) ([]subdir, error) {
	entries, err := os.ReadDir(dir)
	var out []subdir
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			fi, err := os.Stat(path)
			isDir = err == nil && fi.IsDir()
		}
		if !isDir {
			continue
		}
		d := subdir{name: e.Name()}
		if repos {
			_, err := os.Stat(filepath.Join(path, ".git"))
			d.git = err == nil
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b subdir) int {
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})
	return out, err
}

// cleanPath trims a path typed or pasted into a form, and drops quotes
// around it, as Explorer's "Copy as path" adds: "C:\code\api".
func cleanPath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

func isPathSep(r rune) bool { return r == '/' || r == filepath.Separator }

// typedParent splits a directory being typed into the part up to its last
// separator, "~/Projects/", and that separator; with none, the parent is ""
// and the separator the system's.
func typedParent(value string) (parent, sep string) {
	i := strings.LastIndexFunc(value, isPathSep)
	if i < 0 {
		return "", string(filepath.Separator)
	}
	return value[:i+1], value[i : i+1]
}

// expandDir turns a directory as typed into one to read: ~ expanded, ""
// the working directory.
func expandDir(dir string) string {
	if dir == "" {
		return "."
	}
	if d, err := config.ExpandDir(dir); err == nil {
		return d
	}
	return dir
}

// dirListMsg reports the directories in the parent of the directory being
// typed, for its completion.
type dirListMsg struct {
	owner  *projectForm
	gen    int
	parent string // as typed
	dirs   []subdir
}

// listParent lists the parent of the directory being typed in the
// background, if it changed, while the directory field is focused.
func (pf *projectForm) listParent() tea.Cmd {
	if !pf.dirField.input.Focused() {
		return nil
	}
	parent, _ := typedParent(pf.v.dir)
	if pf.listAsked && parent == pf.listWanted {
		return nil
	}
	pf.listAsked, pf.listWanted = true, parent
	pf.listGen++
	gen := pf.listGen
	return func() tea.Msg {
		dirs, _ := subdirs(expandDir(parent), false)
		return dirListMsg{owner: pf, gen: gen, parent: parent, dirs: dirs}
	}
}

// completion returns the first directory next to the one being typed whose
// name starts with what's typed of it, ignoring case: "~/Projects/runr/" for
// "~/Projects/ru". Hidden ones only once a "." is typed. "" if there's none.
func (pf *projectForm) completion() string {
	value := pf.v.dir
	parent, sep := typedParent(value)
	if pf.listed == nil || *pf.listed != parent {
		return ""
	}
	typed := value[len(parent):]
	for _, d := range pf.listing {
		if strings.HasPrefix(d.name, ".") && !strings.HasPrefix(typed, ".") {
			continue
		}
		if c := parent + d.name + sep; len(c) > len(value) && strings.EqualFold(c[:len(value)], value) {
			return c
		}
	}
	return ""
}

// dirGhost returns the rest of the completion, shown after the cursor.
func (pf *projectForm) dirGhost() string {
	if c := pf.completion(); c != "" {
		return c[len(pf.v.dir):]
	}
	return ""
}

// completeDir is the directory field's key handler: → at the end of the
// value accepts the completion.
func (pf *projectForm) completeDir(key string) bool {
	in := &pf.dirField.input
	if key != "right" || in.Position() < len([]rune(in.Value())) {
		return false
	}
	c := pf.completion()
	if c == "" {
		return false
	}
	pf.dirField.setValue(c)
	return true
}

// dirBrowser browses the file system for a project directory, in a picker
// over the project form.
type dirBrowser struct {
	st  *styles
	p   *picker
	dir string // the directory shown, absolute
}

// browseStart returns where to start browsing for the directory typed in a
// form: it, or the nearest of its parents that exists.
func browseStart(value string) string {
	dir, err := filepath.Abs(expandDir(cleanPath(value)))
	if err != nil {
		dir, _ = os.UserHomeDir()
	}
	for {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// newDirBrowser opens a browser at start. choose is called with the
// directory picked with ctrl+o.
func newDirBrowser(st *styles, start string, choose func(dir string) tea.Cmd) (*picker, tea.Cmd) {
	b := &dirBrowser{st: st}
	p, cmd := newPicker(st, &picker{
		title: "choose directory", action: "open", mode: "browse", noun: "folder",
		empty: "no folders here", labelWidth: 40,
		hints: [][2]string{
			{"type", "to filter"}, {"↑↓", "select"}, {"enter", "open"},
			{"backspace", "up"}, {"ctrl+o", "use this folder"}, {"esc", "back"},
		},
	})
	b.p = p
	p.keys = func(key string) (bool, tea.Cmd) {
		switch key {
		case "enter":
			if it, ok := p.selected(); ok {
				b.load(it.name, b.dir)
			}
			return true, nil
		case "backspace":
			if p.input.Value() == "" {
				b.load(filepath.Dir(b.dir), b.dir)
				return true, nil
			}
		case "ctrl+o":
			return true, choose(b.dir)
		}
		return false, nil
	}
	b.load(start, "")
	return p, cmd
}

// load shows the folders in dir, with the cursor on from if it's among them.
func (b *dirBrowser) load(dir, from string) {
	p, st := b.p, b.st
	b.dir = dir
	dirs, err := subdirs(dir, true)
	p.items = p.items[:0]
	if parent := filepath.Dir(dir); parent != dir {
		p.items = append(p.items, pickerItem{name: parent, label: "..", info: "parent folder"})
	}
	var hidden []pickerItem
	for _, d := range dirs {
		if d.name == ".git" {
			continue
		}
		it := pickerItem{name: filepath.Join(dir, d.name), label: d.name + string(filepath.Separator)}
		if d.git {
			it.right = "git"
		}
		if strings.HasPrefix(d.name, ".") {
			it.section = "Hidden"
			hidden = append(hidden, it)
			continue
		}
		p.items = append(p.items, it)
	}
	p.items = append(p.items, hidden...)
	p.note = ""
	if err != nil {
		p.note = "can't read this folder: " + err.Error()
	}
	p.chip = "ctrl+o use this folder"
	p.contextFor = func(width int) string {
		// Room for the chip, unless the path alone doesn't fit.
		path := config.DisplayPath(dir)
		if room := width - len(p.chip) - 3; ansi.StringWidth(path) > room {
			path = shortenPath(path, max(room, width/2))
		}
		return st.bold.Render(path)
	}
	p.input.SetValue("")
	p.filter()
	if len(p.items) > 1 && p.items[0].label == ".." {
		p.cursor = 1 // on the first folder
	}
	for k, i := range p.filtered {
		if p.items[i].name == from {
			p.cursor = k
		}
	}
}
