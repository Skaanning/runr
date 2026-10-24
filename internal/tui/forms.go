package tui

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"charm.land/huh/v2"

	"github.com/Skaanning/runr/internal/config"
)

// SaveFunc validates and writes a changed config.
type SaveFunc func(*config.Config) error

// editForm is an open project or group form.
type editForm struct {
	title    string       // shown in the header, e.g. "new project"
	original launcherItem // the item being edited; empty name for a new one
	form     *huh.Form
	// result returns the changed config and the item to put the cursor on.
	result func() (cfg *config.Config, focus launcherItem, err error)
	// rebuild recreates the form with the same values, after a failed save.
	rebuild func() *huh.Form
	save    *bool // the final Save/Cancel choice
}

func (f *editForm) confirmed() bool { return *f.save }

// projectValues holds the project form's fields.
type projectValues struct {
	original string // name of the project being edited; "" for a new one
	name     string
	cmd      string
	dir      string
	deps     []string
	readyLog string
	env      string // KEY=VALUE lines
	envFile  string
	color    string
	label    string // display name
	save     bool
}

func newProjectValues(cfg *config.Config, name string) *projectValues {
	p := cfg.Projects[name]
	if p == nil {
		dir, _ := os.Getwd()
		return &projectValues{dir: config.DisplayPath(dir), save: true}
	}
	var env []string
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		env = append(env, k+"="+p.Env[k])
	}
	return &projectValues{
		original: name, name: name, cmd: p.Cmd, dir: p.Dir, deps: slices.Clone(p.DependsOn),
		readyLog: p.ReadyLog, env: strings.Join(env, "\n"), envFile: p.EnvFile,
		color: p.Color, label: p.DisplayName, save: true,
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
	c.Projects[v.projectName()] = &config.Project{
		Dir:         dir,
		Cmd:         strings.TrimSpace(v.cmd),
		Env:         env,
		EnvFile:     strings.TrimSpace(v.envFile),
		DependsOn:   deps,
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
			return errors.New(is.Message)
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

func newProjectForm(cfg *config.Config, name string) *editForm {
	v := newProjectValues(cfg, name)
	title := "new project"
	if v.original != "" {
		title = "edit project " + v.original
	}
	build := func() *huh.Form {
		check := func(field string) func(string) error {
			return func(string) error { return v.check(cfg, field) }
		}
		fields := []huh.Field{
			huh.NewInput().Title("Name").Value(&v.name).Validate(check("")),
		}
		if v.original != "" {
			fields[0].(*huh.Input).Description("renaming also updates groups and depends_on")
		}
		fields = append(fields,
			huh.NewInput().Title("Command").Description("run with the system shell in the directory").
				Value(&v.cmd).Validate(check("cmd")),
			huh.NewInput().Title("Directory").Value(&v.dir).Validate(check("dir")),
		)
		var others []huh.Option[string]
		for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
			if p != v.original {
				others = append(others, huh.NewOption(p, p).Selected(slices.Contains(v.deps, p)))
			}
		}
		if len(others) > 0 {
			fields = append(fields, huh.NewMultiSelect[string]().Title("Depends on").
				Description("started first; this project waits until they are ready").
				Options(others...).Value(&v.deps).Filterable(false).Height(len(others)+2). // + title, description
				Validate(func([]string) error { return v.check(cfg, "depends_on") }))
		}
		fields = append(fields,
			huh.NewInput().Title("Ready log").
				Description("regex; ready once an output line matches (empty: ready when started)").
				Value(&v.readyLog).Validate(check("ready_log")),
			huh.NewText().Title("Environment").Description("KEY=VALUE, one per line").Lines(3).
				Value(&v.env).Validate(check("env")),
			huh.NewInput().Title("Env file").Description("relative to the directory").
				Value(&v.envFile).Validate(check("env_file")),
			huh.NewInput().Title("Color").Description("e.g. cyan, bright-red, 208 or #ff8800; empty: automatic").
				Value(&v.color).Validate(check("color")),
			huh.NewInput().Title("Display name").Description("shown in the running view; empty: the name").
				Value(&v.label),
			huh.NewConfirm().Title("Save project?").Affirmative("Save").Negative("Cancel").Value(&v.save),
		)
		return newForm(fields...)
	}
	return &editForm{
		title:    title,
		original: launcherItem{name: v.original},
		form:     build(),
		result: func() (*config.Config, launcherItem, error) {
			c, err := v.candidate(cfg)
			return c, launcherItem{name: v.projectName()}, err
		},
		rebuild: build,
		save:    &v.save,
	}
}

// groupValues holds the group form's fields.
type groupValues struct {
	original string // name of the group being edited; "" for a new one
	name     string
	members  []string
	save     bool
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
func newGroupForm(cfg *config.Config, name string, preselect []string) *editForm {
	v := &groupValues{original: name, name: name, members: slices.Clone(preselect), save: true}
	title := "new group"
	if name != "" {
		v.members = slices.Clone(cfg.Groups[name])
		title = "edit group " + name
	}
	build := func() *huh.Form {
		fields := []huh.Field{
			huh.NewInput().Title("Name").Value(&v.name).Validate(func(string) error { return v.checkName(cfg) }),
		}
		var options []huh.Option[string]
		for _, p := range slices.Sorted(maps.Keys(cfg.Projects)) {
			options = append(options, huh.NewOption(p, p).Selected(slices.Contains(v.members, p)))
		}
		fields = append(fields,
			huh.NewMultiSelect[string]().Title("Projects").
				Description("dependencies are started too, even if not listed").
				Options(options...).Value(&v.members).Filterable(false).Height(len(options)+2),
			huh.NewConfirm().Title("Save group?").Affirmative("Save").Negative("Cancel").Value(&v.save),
		)
		return newForm(fields...)
	}
	return &editForm{
		title:    title,
		original: launcherItem{name: name, group: true},
		form:     build(),
		result: func() (*config.Config, launcherItem, error) {
			c, err := v.candidate(cfg)
			return c, launcherItem{name: v.groupName(), group: true}, err
		},
		rebuild: build,
		save:    &v.save,
	}
}

func newForm(fields ...huh.Field) *huh.Form {
	// Errors are shown in the launcher's help line: huh would put them below
	// the form, which is cut off when the form fills the screen.
	return huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeFunc(huh.ThemeBase)).
		WithShowHelp(false).WithShowErrors(false)
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
