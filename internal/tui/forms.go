package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

// SaveFunc validates and writes a changed config.
type SaveFunc func(*config.Config) error

// editForm is an open project or group form.
type editForm struct {
	title    string       // the pane's title, e.g. "new project"
	original launcherItem // the item being edited; empty name for a new one
	form     *form
	// result returns the changed config and the item to put the cursor on.
	result func() (cfg *config.Config, focus launcherItem, err error)
	// start, if set, returns the form's first background work; handle, if
	// set, handles the messages of that work and is called after every
	// update, returning what to do next.
	start  func() tea.Cmd
	handle func(tea.Msg) tea.Cmd
	// context, if set, returns the styled right side of the status bar.
	context func() string
	state   any // the form's own state, e.g. a *projectForm
}

func (f *editForm) init() tea.Cmd {
	cmd := f.form.init()
	if f.start != nil {
		cmd = tea.Batch(cmd, f.start())
	}
	return cmd
}

func (f *editForm) update(msg tea.Msg) tea.Cmd {
	cmd := f.form.update(msg)
	if f.handle != nil {
		cmd = tea.Batch(cmd, f.handle(msg))
	}
	return cmd
}

// statusInfo returns the status bar's count of errors, with the first one:
// "✗ 1 error · ready log: invalid regex …".
func (f *editForm) statusInfo(st *styles) string {
	errs := f.form.errors()
	if len(errs) == 0 {
		return ""
	}
	first := errs[0]
	return st.err.Bold(true).Render("✗ "+count(len(errs), "error")) +
		st.err.Render(" · "+strings.ToLower(first.title)+": "+first.err.Error())
}

// oneShotOption is the one option of the form's one-shot field.
const oneShotOption = "yes"

// projectValues holds the project form's fields.
type projectValues struct {
	original string // name of the project being edited; "" for a new one
	name     string
	cmd      string
	dir      string
	deps     []string
	oneShot  []string // oneShotOption when ready = "exit"
	readyLog string
	env      string // KEY=VALUE lines
	envFile  string
	color    string
	label    string // display name
}

func newProjectValues(cfg *config.Config, name string) *projectValues {
	p := cfg.Projects[name]
	if p == nil {
		dir, _ := os.Getwd()
		return &projectValues{dir: config.DisplayPath(dir)}
	}
	var env []string
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		env = append(env, k+"="+p.Env[k])
	}
	var oneShot []string
	if p.OneShot() {
		oneShot = []string{oneShotOption}
	}
	return &projectValues{
		original: name, name: name, cmd: p.Cmd, dir: p.Dir, deps: slices.Clone(p.DependsOn),
		oneShot: oneShot, readyLog: p.ReadyLog, env: strings.Join(env, "\n"), envFile: p.EnvFile,
		color: p.Color, label: p.DisplayName,
	}
}

func (v *projectValues) projectName() string {
	return strings.TrimSpace(v.name)
}

// fieldError is a problem with the form's input that the config validator
// can't report, because the input can't be applied in the first place.
type fieldError struct {
	field string // as in config.Issue; "" for the name
	err   error
}

func (e *fieldError) Error() string { return e.err.Error() }

// fieldErr returns err if it is a fieldError for field.
func fieldErr(err error, field string) error {
	var fe *fieldError
	if errors.As(err, &fe) && fe.field == field {
		return fe.err
	}
	return nil
}

// candidate returns cfg with the form's project applied: renamed if its
// name changed, then with the new field values.
func (v *projectValues) candidate(cfg *config.Config) (*config.Config, error) {
	env, err := parseEnv(v.env)
	if err != nil {
		return nil, &fieldError{"env", err}
	}
	dir, err := config.NormalizeDir(strings.TrimSpace(v.dir))
	if err != nil {
		return nil, &fieldError{"dir", err}
	}
	c := cfg.Clone()
	if c.Projects == nil {
		c.Projects = map[string]*config.Project{}
	}
	if v.original != "" {
		if _, _, err := c.Rename(v.original, v.projectName()); err != nil {
			return nil, &fieldError{"", err}
		}
	}
	var deps []string
	if len(v.deps) > 0 {
		deps = slices.Clone(v.deps)
	}
	ready := ""
	if len(v.oneShot) > 0 {
		ready = config.ReadyExit
	}
	c.Projects[v.projectName()] = &config.Project{
		Dir:         dir,
		Cmd:         strings.TrimSpace(v.cmd),
		Env:         env,
		EnvFile:     strings.TrimSpace(v.envFile),
		DependsOn:   deps,
		Ready:       ready,
		ReadyLog:    v.readyLog,
		Color:       strings.TrimSpace(v.color),
		DisplayName: strings.TrimSpace(v.label),
	}
	return c, nil
}

// check validates one field ("" for the name) by validating the whole
// config with the form applied, using the same rules as the CLI.
func (v *projectValues) check(cfg *config.Config, field string) error {
	name := v.projectName()
	if field == "" {
		if err := checkNewName(cfg, v.original, name); err != nil {
			return err
		}
	}
	c, err := v.candidate(cfg)
	if err != nil {
		return fieldErr(err, field) // nil if it belongs to another field
	}
	return issueFor(c, "project", name, field)
}

// checkNewName checks a name typed into a form for an item currently named
// original ("" for a new item).
func checkNewName(cfg *config.Config, original, name string) error {
	switch {
	case name == "":
		return errors.New("is required")
	case name != original && (cfg.Projects[name] != nil || cfg.Groups[name] != nil):
		return errors.New("a project or group with this name already exists")
	}
	return nil
}

// issueFor returns the validation error for a project or group field, if any.
func issueFor(c *config.Config, kind, name, field string) error {
	_, err := c.Validate()
	var verr *config.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	for _, is := range verr.Issues {
		cycle := field == "depends_on" && is.Field == field && strings.HasPrefix(is.Message, "dependency cycle")
		if is.Kind == kind && (is.Name == name && is.Field == field || cycle) {
			// Go's regexp errors repeat that they're about a regexp.
			return errors.New(strings.Replace(is.Message, "error parsing regexp: ", "", 1))
		}
	}
	return nil
}

// parseEnv parses KEY=VALUE lines; blank lines and # comments are skipped.
func parseEnv(s string) (map[string]string, error) {
	var env map[string]string
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", i+1)
		}
		if env == nil {
			env = map[string]string{}
		}
		env[strings.TrimSpace(key)] = value
	}
	return env, nil
}

// dirCheckDelay is how long the directory must stay unchanged while typing
// before it's checked.
const dirCheckDelay = 250 * time.Millisecond

// dirCheck is what the project form found about a directory and env file.
type dirCheck struct {
	err    error       // the directory can't be used: missing, or not a directory
	notDir bool        // it's a file
	git    *projectGit // nil: not in a repository, or git is not installed
	env    bool        // an env file was checked
	vars   int         // variables in the env file
	envErr error       // reading the env file failed
}

type (
	dirCheckMsg struct { // the directory stayed unchanged for dirCheckDelay
		owner *projectForm
		gen   int
	}
	dirCheckedMsg struct { // a check finished
		owner *projectForm
		gen   int
		check dirCheck
	}
)

// projectForm is the state of an open project form beyond its fields: the
// directory check and the preview.
type projectForm struct {
	st      *styles
	cfg     *config.Config
	v       *projectValues
	git     GitReader // nil: git is not installed
	label   *formField
	wanted  string    // directory and env file of the latest check asked for
	gen     int       // generation of that check; older results are dropped
	checked *dirCheck // the latest result, or nil before the first one
}

// checkKey identifies what the directory check depends on.
func (pf *projectForm) checkKey() string {
	return strings.TrimSpace(pf.v.dir) + "\x00" + strings.TrimSpace(pf.v.envFile)
}

// startCheck checks the directory now.
func (pf *projectForm) startCheck() tea.Cmd {
	pf.wanted = pf.checkKey()
	pf.gen++
	return pf.runCheck(pf.gen)
}

func (pf *projectForm) runCheck(gen int) tea.Cmd {
	git, name, dir, envFile := pf.git, pf.v.projectName(), pf.v.dir, pf.v.envFile
	return func() tea.Msg {
		return dirCheckedMsg{owner: pf, gen: gen, check: checkDir(git, name, dir, envFile)}
	}
}

// handle runs the directory check once the directory or env file stopped
// changing, and keeps the display name's placeholder on the name.
func (pf *projectForm) handle(msg tea.Msg) tea.Cmd {
	pf.label.input.Placeholder = cmp.Or(pf.v.projectName(), "the name")
	switch msg := msg.(type) {
	case dirCheckMsg:
		if msg.owner == pf && msg.gen == pf.gen {
			return pf.runCheck(msg.gen)
		}
		return nil
	case dirCheckedMsg:
		if msg.owner == pf && msg.gen == pf.gen {
			pf.checked = &msg.check
		}
		return nil
	}
	if key := pf.checkKey(); key != pf.wanted {
		pf.wanted = key
		pf.gen++
		gen := pf.gen
		return tea.Tick(dirCheckDelay, func(time.Time) tea.Msg { return dirCheckMsg{owner: pf, gen: gen} })
	}
	return nil
}

// checkDir checks that a project directory exists, reads its git state and
// counts the variables in its env file. name is the project's, for git.
func checkDir(git GitReader, name, dir, envFile string) dirCheck {
	var c dirCheck
	dir, err := config.NormalizeDir(strings.TrimSpace(dir))
	if err == nil {
		dir, err = config.ExpandHome(dir)
	}
	if err != nil {
		c.err = err
		return c
	}
	fi, err := os.Stat(dir)
	switch {
	case err != nil:
		c.err = err
		return c
	case !fi.IsDir():
		c.notDir = true
		return c
	}
	if git != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		repos := git.FindAll(ctx, map[string]string{name: dir})
		if err := repos.Errors[name]; err != nil {
			c.git = &projectGit{err: err}
		} else if root, ok := repos.Of[name]; ok {
			status, err := git.Status(ctx, repos.ByRoot[root])
			c.git = &projectGit{root: root, status: status, err: err}
		}
	}
	if envFile = strings.TrimSpace(envFile); envFile != "" {
		c.env = true
		p := config.Project{Dir: dir, EnvFile: envFile}
		path, err := p.ResolvedEnvFile()
		if err == nil {
			var vars map[string]string
			vars, err = proc.LoadEnvFile(path)
			c.vars = len(vars)
		}
		c.envErr = err
	}
	return c
}

// dirInfo is the line under the directory: "✓ exists · git: main ↑1".
func (pf *projectForm) dirInfo() string {
	st, c := pf.st, pf.checked
	switch {
	case c == nil || strings.TrimSpace(pf.v.dir) == "":
		return ""
	case c.notDir:
		return st.warn.Render("not a directory (only a warning)")
	case errors.Is(c.err, fs.ErrNotExist):
		return st.warn.Render("does not exist yet (only a warning)")
	case c.err != nil:
		return st.warn.Render(c.err.Error())
	}
	info := st.ok.Render("✓") + " " + st.faint.Render("exists")
	if c.git != nil {
		info += st.faint.Render(" · git: ") + gitLabel(st, *c.git, maxGitWidth)
	}
	return info
}

// envFileInfo is the line under the env file: "✓ found · 4 variables".
func (pf *projectForm) envFileInfo() string {
	st, c := pf.st, pf.checked
	switch {
	case c == nil || !c.env || strings.TrimSpace(pf.v.envFile) == "":
		return ""
	case errors.Is(c.envErr, fs.ErrNotExist):
		return st.warn.Render("does not exist (starting the project fails)")
	case c.envErr != nil:
		return st.warn.Render(c.envErr.Error())
	}
	return st.ok.Render("✓") + " " + st.faint.Render("found · "+count(c.vars, "variable"))
}

// colorName returns the color the project gets: the one typed if it's
// valid, else its default color.
func (pf *projectForm) colorName() string {
	if c := strings.TrimSpace(pf.v.color); config.ValidColor(c) {
		return c
	}
	c := pf.cfg.Clone()
	if c.Projects == nil {
		c.Projects = map[string]*config.Project{}
	}
	if pf.v.original != "" {
		delete(c.Projects, pf.v.original)
	}
	name := cmp.Or(pf.v.projectName(), pf.v.original, "\x00")
	c.Projects[name] = &config.Project{}
	return c.ColorOf(name)
}

// swatch shows the project's color before the color field's value.
func (pf *projectForm) swatch() string {
	return lipgloss.NewStyle().Foreground(pf.st.color(pf.colorName())).Render("██") + " "
}

// cycleColor moves through the named colors with ↑↓, starting from the
// automatic color ("").
func (pf *projectForm) cycleColor(fd *formField) func(string) bool {
	colors := append([]string{""}, config.ColorNames...)
	return func(key string) bool {
		d := 1
		switch key {
		case "up":
			d = -1
		case "down":
		default:
			return false
		}
		i := slices.Index(colors, strings.TrimSpace(pf.v.color))
		if i < 0 { // a number or hex code: start over from the ends
			i = 0
			if d < 0 {
				i = len(colors)
			}
		}
		fd.setValue(colors[(i+d+len(colors))%len(colors)])
		return true
	}
}

// envRows shows the environment while its field isn't focused, the keys
// highlighted.
func (pf *projectForm) envRows(width int) []string {
	st := pf.st
	lines := strings.Split(pf.v.env, "\n")
	rows := make([]string, envLines)
	for i := range rows {
		rows[i] = st.faint.Render("│")
		if i >= len(lines) {
			continue
		}
		line := lines[i]
		if i == envLines-1 && len(lines) > envLines {
			rows[i] += " " + st.faint.Render(fmt.Sprintf("… %d more", len(lines)-i))
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			line = st.warn.Render(key) + "=" + value
		}
		rows[i] += " " + ansi.Truncate(line, width-2, "…")
	}
	return rows
}

// preview shows the project's row as the running view's sidebar will:
// state, display name in its color and the directory's git label.
func (pf *projectForm) preview(width int) []string {
	st := pf.st
	inner := max(min(width-2, 40)-4, 4)
	color := lipgloss.NewStyle().Foreground(st.color(pf.colorName()))
	label := cmp.Or(strings.TrimSpace(pf.v.label), pf.v.projectName())
	name := color.Bold(true).Render(label)
	if label == "" {
		name = st.faint.Render("name")
	}
	row := color.Render("▌") + st.stateIcon(supervisor.Starting, "◐") + " " + name
	if c := pf.checked; c != nil && c.git != nil && strings.TrimSpace(pf.v.dir) != "" {
		row = pad(row, max(ansi.StringWidth(row)+2, 16)) + gitLabel(st, *c.git, maxGitWidth)
	}
	row = pad(ansi.Truncate(row, inner, "…"), inner)
	if st.selBar != "" {
		row = onBackground(row, st.selBar)
	}
	return []string{
		"  " + st.faint.Render("╭"+strings.Repeat("─", inner+2)+"╮"),
		"  " + st.faint.Render("│") + " " + row + " " + st.faint.Render("│"),
		"  " + st.faint.Render("╰"+strings.Repeat("─", inner+2)+"╯"),
	}
}

// newProjectForm opens a form for an existing project, or a new one if name
// is "". git checks the directory as it's typed; nil if git isn't installed.
func newProjectForm(st *styles, cfg *config.Config, name string, git GitReader) *editForm {
	v := newProjectValues(cfg, name)
	pf := &projectForm{st: st, cfg: cfg, v: v, git: git}
	check := func(field string) func() error {
		return func() error { return v.check(cfg, field) }
	}

	nameField := newInputField("", "Name", &v.name)
	nameField.check = check("")
	title := "new project"
	if v.original != "" {
		title = "edit " + v.original
		nameField.desc = "renaming also updates groups and depends_on"
	}
	cmdField := newInputField("cmd", "Command", &v.cmd)
	cmdField.desc = "run with the system shell in the directory"
	cmdField.check = check("cmd")
	dirField := newInputField("dir", "Directory", &v.dir)
	dirField.check, dirField.info = check("dir"), pf.dirInfo
	basics := &formSection{title: "Basics", fields: []*formField{nameField, cmdField, dirField}}

	startup := &formSection{title: "Startup"}
	var others []formOption
	for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
		if p != v.original {
			others = append(others, formOption{value: p, color: cfg.ColorOf(p)})
		}
	}
	if len(others) > 0 {
		deps := newChoiceField("depends_on", "Depends on", others, &v.deps)
		deps.desc = "started first; waits until they are ready"
		deps.check = check("depends_on")
		startup.fields = append(startup.fields, deps)
	}
	oneShot := newChoiceField("ready", "One-shot task", []formOption{{value: oneShotOption}}, &v.oneShot)
	oneShot.desc = "dependents wait until it exits with code 0"
	oneShot.check = check("ready")
	startup.fields = append(startup.fields, oneShot)
	readyLog := newInputField("ready_log", "Ready log", &v.readyLog)
	readyLog.input.Placeholder = "regex; empty: ready once started"
	readyLog.check = check("ready_log")
	startup.fields = append(startup.fields, readyLog)

	env := newTextField("env", "Variables", &v.env)
	env.desc = "KEY=VALUE, one per line"
	env.check, env.display = check("env"), pf.envRows
	envFile := newInputField("env_file", "Env file", &v.envFile)
	envFile.input.Placeholder = "relative to the directory"
	envFile.check, envFile.info = check("env_file"), pf.envFileInfo
	environment := &formSection{title: "Environment", right: true, fields: []*formField{env, envFile}}

	color := newInputField("color", "Color", &v.color)
	color.input.Placeholder = "auto"
	color.check, color.adorn, color.note, color.width = check("color"), pf.swatch, "↑↓ cycle", 16
	color.keys, color.hint = pf.cycleColor(color), [2]string{"↑↓", "color"}
	pf.label = newInputField("display_name", "Display name", &v.label)
	look := &formSection{title: "Look", right: true, fields: []*formField{color, pf.label}}
	preview := &formSection{title: "Preview", right: true, view: pf.preview}

	pf.handle(nil) // placeholders
	return &editForm{
		title:    title,
		original: launcherItem{name: v.original},
		form:     newForm(st, basics, startup, environment, look, preview),
		result: func() (*config.Config, launcherItem, error) {
			c, err := v.candidate(cfg)
			return c, launcherItem{name: v.projectName()}, err
		},
		start:  pf.startCheck,
		handle: pf.handle,
		state:  pf,
		context: func() string {
			return st.faint.Render(config.DisplayPath(strings.TrimSpace(v.dir)))
		},
	}
}

// groupValues holds the group form's fields.
type groupValues struct {
	original string // name of the group being edited; "" for a new one
	name     string
	members  []string
}

func (v *groupValues) groupName() string {
	return strings.TrimSpace(v.name)
}

// candidate returns cfg with the form's group applied: renamed if its name
// changed, then with the new members.
func (v *groupValues) candidate(cfg *config.Config) (*config.Config, error) {
	c := cfg.Clone()
	if c.Groups == nil {
		c.Groups = map[string][]string{}
	}
	if v.original != "" {
		if _, _, err := c.Rename(v.original, v.groupName()); err != nil {
			return nil, err
		}
	}
	// Keep the existing order of members that stay, then add new ones.
	var members []string
	for _, m := range cfg.Groups[v.original] {
		if slices.Contains(v.members, m) {
			members = append(members, m)
		}
	}
	for _, m := range v.members {
		if !slices.Contains(members, m) {
			members = append(members, m)
		}
	}
	if members == nil {
		members = []string{}
	}
	c.Groups[v.groupName()] = members
	return c, nil
}

func (v *groupValues) checkName(cfg *config.Config) error {
	name := v.groupName()
	if err := checkNewName(cfg, v.original, name); err != nil {
		return err
	}
	c, err := v.candidate(cfg)
	if err != nil {
		return err
	}
	return issueFor(c, "group", name, "")
}

// newGroupForm opens a form for an existing group, or for a new one with
// the given members preselected.
func newGroupForm(st *styles, cfg *config.Config, name string, preselect []string) *editForm {
	v := &groupValues{original: name, name: name, members: slices.Clone(preselect)}
	title := "new group"
	if name != "" {
		v.members = slices.Clone(cfg.Groups[name])
		title = "edit " + name
	}
	nameField := newInputField("", "Name", &v.name)
	nameField.check = func() error { return v.checkName(cfg) }
	var options []formOption
	for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
		options = append(options, formOption{value: p, color: cfg.ColorOf(p)})
	}
	members := newChoiceField("", "Projects", options, &v.members)
	members.desc = "dependencies are started too, even if not listed"
	return &editForm{
		title:    title,
		original: launcherItem{name: name, group: true},
		form: newForm(st,
			&formSection{title: "Group", fields: []*formField{nameField}},
			&formSection{title: "Members", fields: []*formField{members}},
		),
		result: func() (*config.Config, launcherItem, error) {
			c, err := v.candidate(cfg)
			return c, launcherItem{name: v.groupName(), group: true}, err
		},
		context: func() string { return st.faint.Render(count(len(v.members), "project")) },
	}
}

// confirmation is an open delete confirmation.
type confirmation struct {
	lines  []string
	result func() *config.Config // the config after deleting
}

func newDeleteConfirmation(cfg *config.Config, it launcherItem) *confirmation {
	if it.group {
		return &confirmation{
			lines: []string{fmt.Sprintf("Delete group %q? Its projects are kept.", it.name)},
			result: func() *config.Config {
				c := cfg.Clone()
				delete(c.Groups, it.name)
				return c
			},
		}
	}
	c := cfg.Clone()
	groups, dependents := c.RemoveProject(it.name)
	lines := []string{fmt.Sprintf("Delete project %q?", it.name)}
	if len(groups) > 0 {
		lines = append(lines, "It is also removed from: "+strings.Join(groups, ", "))
	}
	if len(dependents) > 0 {
		lines = append(lines, fmt.Sprintf("Required by %s; it is removed from their depends_on.", strings.Join(dependents, ", ")))
	}
	return &confirmation{lines: lines, result: func() *config.Config { return c }}
}
