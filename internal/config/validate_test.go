package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// validConfig returns a config that passes validation with no warnings.
func validConfig(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New()
	c.Projects["db"] = &Project{Dir: dir, Cmd: "run-db", ReadyLog: `ready to accept`}
	c.Projects["api"] = &Project{Dir: dir, Cmd: "run-api", EnvFile: ".env", Env: map[string]string{"PORT": "8080"}, DependsOn: []string{"db"}, Color: "cyan"}
	c.Projects["auth"] = &Project{Dir: dir, Cmd: "run-auth", DependsOn: []string{"db"}, Color: "#ff8800"}
	c.Groups["backend"] = []string{"db", "api", "auth"}
	return c
}

func TestValidateOK(t *testing.T) {
	warnings, err := validConfig(t).Validate()
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Config)
		want   []string // expected Issue strings, in order
	}{
		{
			"missing cmd and dir",
			func(c *Config) { c.Projects["x"] = &Project{} },
			[]string{`project "x": cmd: is required`, `project "x": dir: is required`},
		},
		{
			"invalid project name",
			func(c *Config) { c.Projects["my api"] = &Project{Dir: c.Projects["db"].Dir, Cmd: "x"} },
			[]string{`project "my api": invalid name (use letters, digits, '.', '_' and '-', starting with a letter or digit)`},
		},
		{
			"name clash",
			func(c *Config) { c.Groups["api"] = []string{"db"} },
			[]string{`project "api": a group has the same name; project and group names must be unique`},
		},
		{
			"unknown dependency",
			func(c *Config) { c.Projects["api"].DependsOn = []string{"dbb"} },
			[]string{`project "api": depends_on: unknown project "dbb"`},
		},
		{
			"group as dependency",
			func(c *Config) { c.Projects["api"].DependsOn = []string{"backend"} },
			[]string{`project "api": depends_on: "backend" is a group; only projects can be dependencies`},
		},
		{
			"self dependency",
			func(c *Config) { c.Projects["db"].DependsOn = []string{"db"} },
			[]string{`project "db": depends_on: a project cannot depend on itself`},
		},
		{
			"duplicate dependency",
			func(c *Config) { c.Projects["api"].DependsOn = []string{"db", "db"} },
			[]string{`project "api": depends_on: "db" is listed more than once`},
		},
		{
			"cycle",
			func(c *Config) { c.Projects["db"].DependsOn = []string{"auth"} },
			[]string{`project "db": depends_on: dependency cycle: db → auth → db`},
		},
		{
			"bad regex",
			func(c *Config) { c.Projects["db"].ReadyLog = "ready (" },
			[]string{"project \"db\": ready_log: invalid regex: error parsing regexp: missing closing ): `ready (`"},
		},
		{
			"bad color",
			func(c *Config) { c.Projects["api"].Color = "teal" },
			[]string{`project "api": color: unknown color "teal" (use a name like "cyan", "bright-red", an ANSI number 0-255 or a hex code like "#ff8800")`},
		},
		{
			"bad env key",
			func(c *Config) { c.Projects["api"].Env["A=B"] = "x" },
			[]string{`project "api": env: invalid variable name "A=B"`},
		},
		{
			"unknown group member",
			func(c *Config) { c.Groups["backend"] = append(c.Groups["backend"], "worker") },
			[]string{`group "backend": unknown project "worker"`},
		},
		{
			"nested group",
			func(c *Config) { c.Groups["all"] = []string{"backend"} },
			[]string{`group "all": "backend" is a group; groups can only contain projects`},
		},
		{
			"duplicate group member",
			func(c *Config) { c.Groups["backend"] = []string{"db", "db"} },
			[]string{`group "backend": "db" is listed more than once`},
		},
		{
			"negative settings",
			func(c *Config) {
				c.Settings.ShutdownTimeoutValue.Duration = -time.Second
				c.Settings.ScrollbackLinesValue = -1
			},
			[]string{`settings: shutdown_timeout: must not be negative`, `settings: scrollback_lines: must not be negative`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig(t)
			tt.mutate(c)
			_, err := c.Validate()

			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected *ValidationError, got %v", err)
			}
			var got []string
			for _, i := range verr.Issues {
				got = append(got, i.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("issues:\n got  %q\n want %q", got, tt.want)
			}
		})
	}
}

func TestValidateWarnings(t *testing.T) {
	c := validConfig(t)
	missing := filepath.Join(t.TempDir(), "gone")
	c.Projects["api"].EnvFile = "missing.env"
	c.Projects["auth"].Dir = missing
	c.Groups["empty"] = nil

	warnings, err := c.Validate()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range warnings {
		got = append(got, w.String())
	}
	want := []string{
		`project "api": env_file: ` + filepath.Join(c.Projects["api"].Dir, "missing.env") + ` does not exist`,
		`project "auth": dir: ` + missing + ` does not exist`,
		`group "empty": group is empty`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings:\n got  %q\n want %q", got, want)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	one := &ValidationError{Issues: []Issue{{"project", "api", "cmd", "is required"}}}
	if got := one.Error(); got != `invalid config: project "api": cmd: is required` {
		t.Errorf("single: %q", got)
	}

	two := &ValidationError{Issues: []Issue{
		{"project", "api", "cmd", "is required"},
		{"group", "backend", "", `unknown project "x"`},
	}}
	want := "invalid config (2 problems):\n" +
		"  - project \"api\": cmd: is required\n" +
		"  - group \"backend\": unknown project \"x\""
	if got := two.Error(); got != want {
		t.Errorf("multiple:\n%s\nwant\n%s", got, want)
	}
}

func TestFindCycles(t *testing.T) {
	c := New()
	add := func(name string, deps ...string) { c.Projects[name] = &Project{DependsOn: deps} }
	add("a", "b")
	add("b", "c")
	add("c", "a")
	add("d", "d") // self-dependency: reported by Validate, not as a cycle
	add("e", "f")
	add("f", "e")
	add("g", "a") // depends into a cycle but is not part of it

	var got []string
	for _, cycle := range c.findCycles() {
		got = append(got, strings.Join(cycle, ">"))
	}
	want := []string{"a>b>c>a", "e>f>e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cycles = %v, want %v", got, want)
	}
}

func TestValidColor(t *testing.T) {
	for _, s := range []string{"cyan", "bright-red", "0", "255", "#fff", "#FF8800"} {
		if !ValidColor(s) {
			t.Errorf("ValidColor(%q) = false", s)
		}
	}
	for _, s := range []string{"", "teal", "Cyan", "256", "-1", "#ff88", "ff8800"} {
		if ValidColor(s) {
			t.Errorf("ValidColor(%q) = true", s)
		}
	}
}

func TestColorCode(t *testing.T) {
	tests := map[string]string{
		"black":        "0",
		"cyan":         "6",
		"bright-red":   "9",
		"bright-white": "15",
		"208":          "208",
		"#ff8800":      "#ff8800",
	}
	for in, want := range tests {
		if got := ColorCode(in); got != want {
			t.Errorf("ColorCode(%q) = %q, want %q", in, got, want)
		}
	}
}
