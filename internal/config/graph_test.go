package config

import (
	"reflect"
	"testing"
)

// graphConfig builds a config from a name → dependencies map.
func graphConfig(deps map[string][]string, groups map[string][]string) *Config {
	c := New()
	for name, d := range deps {
		c.Projects[name] = &Project{Dir: "/tmp", Cmd: "x", DependsOn: d}
	}
	for name, members := range groups {
		c.Groups[name] = members
	}
	return c
}

// Diamond: api and auth both depend on db; gateway depends on api and auth.
var diamond = map[string][]string{
	"db":      nil,
	"api":     {"db"},
	"auth":    {"db"},
	"gateway": {"api", "auth"},
	"web":     nil,
}

func TestResolve(t *testing.T) {
	c := graphConfig(diamond, map[string][]string{
		"backend":  {"api", "auth"},
		"frontend": {"web"},
		"empty":    {},
	})

	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{"single project, no deps", []string{"web"}, []string{"web"}},
		{"pulls in dependencies first", []string{"api"}, []string{"db", "api"}},
		{"diamond", []string{"gateway"}, []string{"db", "api", "auth", "gateway"}},
		{"group", []string{"backend"}, []string{"db", "api", "auth"}},
		{"group and project", []string{"frontend", "auth"}, []string{"web", "db", "auth"}},
		{"duplicates removed", []string{"api", "backend", "db"}, []string{"db", "api", "auth"}},
		{"keeps request order", []string{"web", "db"}, []string{"web", "db"}},
		{"empty group", []string{"empty"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Resolve(tt.names...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Resolve(%v) = %v, want %v", tt.names, got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name  string
		deps  map[string][]string
		names []string
		want  string
	}{
		{"unknown name", diamond, []string{"api", "nope"}, `unknown project or group "nope"`},
		{"several unknown", diamond, []string{"x", "api", "y"}, `unknown projects or groups "x", "y"`},
		{"unknown dependency", map[string][]string{"api": {"db"}}, []string{"api"}, `project "api" depends on unknown project "db"`},
		{"cycle", map[string][]string{"a": {"b"}, "b": {"a"}}, []string{"a"}, `dependency cycle involving "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := graphConfig(tt.deps, nil).Resolve(tt.names...)
			if err == nil || err.Error() != tt.want {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLevels(t *testing.T) {
	c := graphConfig(diamond, nil)

	tests := []struct {
		name     string
		projects []string
		want     [][]string
	}{
		{"diamond", []string{"gateway", "auth", "api", "db", "web"}, [][]string{{"db", "web"}, {"api", "auth"}, {"gateway"}}},
		{"deps outside set ignored", []string{"api", "gateway"}, [][]string{{"api"}, {"gateway"}}},
		{"no deps", []string{"web", "db"}, [][]string{{"db", "web"}}},
		{"empty", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Levels(tt.projects)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Levels(%v) = %v, want %v", tt.projects, got, tt.want)
			}
		})
	}
}

func TestLevelsUnevenDepth(t *testing.T) {
	// d depends on a (depth 0) and c (depth 2), so it must land at depth 3.
	c := graphConfig(map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
		"d": {"a", "c"},
	}, nil)
	got, err := c.Levels([]string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a"}, {"b"}, {"c"}, {"d"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Levels = %v, want %v", got, want)
	}
}

func TestLevelsErrors(t *testing.T) {
	c := graphConfig(map[string][]string{"a": {"b"}, "b": {"a"}}, nil)
	if _, err := c.Levels([]string{"a", "b"}); err == nil {
		t.Error("expected cycle error")
	}
	if _, err := c.Levels([]string{"zzz"}); err == nil {
		t.Error("expected unknown project error")
	}
}

func TestDependents(t *testing.T) {
	c := graphConfig(diamond, nil)
	if got := c.Dependents("db"); !reflect.DeepEqual(got, []string{"api", "auth"}) {
		t.Errorf("Dependents(db) = %v", got)
	}
	if got := c.Dependents("gateway"); got != nil {
		t.Errorf("Dependents(gateway) = %v", got)
	}
}
