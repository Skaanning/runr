package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Issue is a single validation finding about a project, group or setting.
type Issue struct {
	Kind    string // "project", "group" or "settings"
	Name    string // project or group name; empty for settings
	Field   string // offending field; may be empty
	Message string
}

func (i Issue) String() string {
	var b strings.Builder
	b.WriteString(i.Kind)
	if i.Name != "" {
		fmt.Fprintf(&b, " %q", i.Name)
	}
	if i.Field != "" {
		b.WriteString(": ")
		b.WriteString(i.Field)
	}
	b.WriteString(": ")
	b.WriteString(i.Message)
	return b.String()
}

// ValidationError lists every error found by Validate.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 1 {
		return "invalid config: " + e.Issues[0].String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid config (%d problems):", len(e.Issues))
	for _, i := range e.Issues {
		b.WriteString("\n  - ")
		b.WriteString(i.String())
	}
	return b.String()
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate checks the config for problems. Errors (unknown references, cycles,
// bad regexes, ...) are returned as a *ValidationError. Warnings are problems
// that only matter when a project is started, such as a missing directory.
func (c *Config) Validate() (warnings []Issue, err error) {
	var errs []Issue
	addErr := func(kind, name, field, format string, args ...any) {
		errs = append(errs, Issue{kind, name, field, fmt.Sprintf(format, args...)})
	}
	addWarn := func(kind, name, field, format string, args ...any) {
		warnings = append(warnings, Issue{kind, name, field, fmt.Sprintf(format, args...)})
	}

	if c.Settings.ShutdownTimeoutValue.Duration < 0 {
		addErr("settings", "", "shutdown_timeout", "must not be negative")
	}
	if c.Settings.ScrollbackLinesValue < 0 {
		addErr("settings", "", "scrollback_lines", "must not be negative")
	}

	for _, name := range sortedKeys(c.Projects) {
		p := c.Projects[name]
		if !namePattern.MatchString(name) {
			addErr("project", name, "", "invalid name (use letters, digits, '.', '_' and '-', starting with a letter or digit)")
		}
		if _, ok := c.Groups[name]; ok {
			addErr("project", name, "", "a group has the same name; project and group names must be unique")
		}
		if strings.TrimSpace(p.Cmd) == "" {
			addErr("project", name, "cmd", "is required")
		}

		if strings.TrimSpace(p.Dir) == "" {
			addErr("project", name, "dir", "is required")
		} else if dir, err := p.ResolvedDir(); err != nil {
			addErr("project", name, "dir", "%v", err)
		} else if fi, err := os.Stat(dir); err != nil {
			addWarn("project", name, "dir", "%s does not exist", dir)
		} else if !fi.IsDir() {
			addWarn("project", name, "dir", "%s is not a directory", dir)
		} else if envFile, err := p.ResolvedEnvFile(); err != nil {
			addErr("project", name, "env_file", "%v", err)
		} else if envFile != "" {
			if _, err := os.Stat(envFile); err != nil {
				addWarn("project", name, "env_file", "%s does not exist", envFile)
			}
		}

		for _, key := range sortedKeys(p.Env) {
			if key == "" || strings.ContainsAny(key, "=\x00") {
				addErr("project", name, "env", "invalid variable name %q", key)
			}
		}

		seen := map[string]bool{}
		for _, dep := range p.DependsOn {
			switch {
			case dep == name:
				addErr("project", name, "depends_on", "a project cannot depend on itself")
			case seen[dep]:
				addErr("project", name, "depends_on", "%q is listed more than once", dep)
			case c.Projects[dep] == nil:
				if _, isGroup := c.Groups[dep]; isGroup {
					addErr("project", name, "depends_on", "%q is a group; only projects can be dependencies", dep)
				} else {
					addErr("project", name, "depends_on", "unknown project %q", dep)
				}
			}
			seen[dep] = true
		}

		if p.ReadyLog != "" {
			if _, err := regexp.Compile(p.ReadyLog); err != nil {
				addErr("project", name, "ready_log", "invalid regex: %v", err)
			}
		}
		if p.Color != "" && !ValidColor(p.Color) {
			addErr("project", name, "color", "unknown color %q (use a name like \"cyan\", \"bright-red\", an ANSI number 0-255 or a hex code like \"#ff8800\")", p.Color)
		}
	}

	for _, cycle := range c.findCycles() {
		addErr("project", cycle[0], "depends_on", "dependency cycle: %s", strings.Join(cycle, " → "))
	}

	for _, name := range sortedKeys(c.Groups) {
		members := c.Groups[name]
		if !namePattern.MatchString(name) {
			addErr("group", name, "", "invalid name (use letters, digits, '.', '_' and '-', starting with a letter or digit)")
		}
		if len(members) == 0 {
			addWarn("group", name, "", "group is empty")
		}
		seen := map[string]bool{}
		for _, m := range members {
			switch {
			case seen[m]:
				addErr("group", name, "", "%q is listed more than once", m)
			case c.Projects[m] == nil:
				if _, isGroup := c.Groups[m]; isGroup {
					addErr("group", name, "", "%q is a group; groups can only contain projects", m)
				} else {
					addErr("group", name, "", "unknown project %q", m)
				}
			}
			seen[m] = true
		}
	}

	if len(errs) > 0 {
		return warnings, &ValidationError{Issues: errs}
	}
	return warnings, nil
}

// ColorNames are the named colors accepted in a project's color field.
var ColorNames = []string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright-black", "bright-red", "bright-green", "bright-yellow",
	"bright-blue", "bright-magenta", "bright-cyan", "bright-white",
}

var hexColorPattern = regexp.MustCompile(`^#([0-9A-Fa-f]{3}|[0-9A-Fa-f]{6})$`)

// ValidColor reports whether s is a named color, an ANSI color number (0-255)
// or a hex color (#rgb or #rrggbb).
func ValidColor(s string) bool {
	for _, n := range ColorNames {
		if s == n {
			return true
		}
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n >= 0 && n <= 255
	}
	return hexColorPattern.MatchString(s)
}

// ColorCode converts a valid project color to the form terminal styling
// libraries understand: an ANSI color index ("cyan" → "6", "bright-red" → "9",
// "208" → "208") or a hex code, returned unchanged.
func ColorCode(color string) string {
	for i, n := range ColorNames {
		if color == n {
			return strconv.Itoa(i)
		}
	}
	return color
}
