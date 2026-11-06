package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Skaanning/runr/internal/config"
)

// newArgsForm returns the form that asks for extra arguments before
// starting names: a field per project that starts, dependencies included,
// in start order, pre-filled from last. Arguments are appended to the
// project's command as typed. When the form is submitted, start is called
// with every project's arguments, trimmed; blank ones clear earlier ones.
func newArgsForm(st *styles, cfg *config.Config, names []string, last map[string]string, start func(args map[string]string) tea.Cmd) (*editForm, error) {
	projects, err := cfg.Resolve(names...)
	if err != nil {
		return nil, err
	}
	values := make([]string, len(projects))
	fields := make([]*formField, len(projects))
	for i, name := range projects {
		values[i] = last[name]
		fields[i] = newInputField("", name, &values[i])
		fields[i].desc = cfg.Projects[name].Cmd + " …"
	}
	f := newForm(st, &formSection{title: "Extra arguments", fields: fields})
	f.action, f.enterSubmits = "start", true
	return &editForm{
		title: "start with arguments",
		form:  f,
		submit: func() tea.Cmd {
			args := make(map[string]string, len(projects))
			for i, name := range projects {
				args[name] = strings.TrimSpace(values[i])
			}
			return start(args)
		},
		context: func() string { return st.faint.Render(count(len(projects), "project")) },
	}, nil
}
