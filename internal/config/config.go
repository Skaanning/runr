package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// EnvVar names the environment variable that overrides the config file location.
const EnvVar = "RUNR_CONFIG"

const (
	DefaultShutdownTimeout = 5 * time.Second
	DefaultScrollbackLines = 10000
)

// Config is the global registry of projects and groups.
type Config struct {
	Settings Settings            `toml:"settings,omitempty"`
	Projects map[string]*Project `toml:"projects,omitempty"`
	Groups   map[string][]string `toml:"groups,omitempty"`
}

// Settings holds global options. Zero values mean "use the default"; use the
// accessor methods rather than reading the fields directly.
type Settings struct {
	ShutdownTimeoutValue Duration `toml:"shutdown_timeout,omitempty"`
	ScrollbackLinesValue int      `toml:"scrollback_lines,omitempty"`
	ShowAllValue         *bool    `toml:"show_all,omitempty"`
	ThemeValue           string   `toml:"theme,omitempty"`
	ThemeBackgroundValue bool     `toml:"theme_background,omitempty"`
	LogDirValue          string   `toml:"log_dir,omitempty"`
}

// LogDir is the directory where runr up appends each project's output to
// <project>.log, with ~ expanded; "" when output isn't saved.
func (s Settings) LogDir() (string, error) {
	return ExpandHome(s.LogDirValue)
}

// Theme is the name of the UI's color theme, one of ThemeNames.
func (s Settings) Theme() string {
	if s.ThemeValue == "" {
		return ThemeNames[0]
	}
	return s.ThemeValue
}

// ThemeBackground reports whether the UI paints the theme's background
// instead of letting the terminal's show through (default false).
func (s Settings) ThemeBackground() bool {
	return s.ThemeBackgroundValue
}

// ShutdownTimeout is the graceful stop window before processes are force-killed.
func (s Settings) ShutdownTimeout() time.Duration {
	if s.ShutdownTimeoutValue.Duration <= 0 {
		return DefaultShutdownTimeout
	}
	return s.ShutdownTimeoutValue.Duration
}

// ScrollbackLines is the per-process log ring buffer size.
func (s Settings) ScrollbackLines() int {
	if s.ScrollbackLinesValue <= 0 {
		return DefaultScrollbackLines
	}
	return s.ScrollbackLinesValue
}

// ShowAll reports whether the running view's sidebar also lists the
// registered projects that haven't been started (default true).
func (s Settings) ShowAll() bool {
	return s.ShowAllValue == nil || *s.ShowAllValue
}

// Project is a single runnable program. Its name is the key in Config.Projects.
type Project struct {
	Dir         string            `toml:"dir"`
	Cmd         string            `toml:"cmd"`
	Env         map[string]string `toml:"env,omitempty,inline"`
	EnvFile     string            `toml:"env_file,omitempty"`
	DependsOn   []string          `toml:"depends_on,omitempty"`
	Ready       string            `toml:"ready,omitempty"` // ReadyExit or ""
	ReadyLog    string            `toml:"ready_log,omitempty"`
	ReadyPort   int               `toml:"ready_port,omitempty"`
	ReadyURL    string            `toml:"ready_url,omitempty"`
	Color       string            `toml:"color,omitempty"`
	DisplayName string            `toml:"display_name,omitempty"`
}

// ReadyExit is the value of Project.Ready for one-shot tasks: the project is
// ready once it exits with code 0, so dependents wait until it is done.
const ReadyExit = "exit"

// OneShot reports whether the project is a one-shot task (ready = "exit").
func (p *Project) OneShot() bool {
	return p.Ready == ReadyExit
}

// ReadyConditions returns the readiness fields that are set, in the order
// ready_log, ready_port, ready_url, ready. A valid project has at most one;
// with none it is ready as soon as it is started.
func (p *Project) ReadyConditions() []string {
	var set []string
	for _, c := range []struct {
		field string
		on    bool
	}{
		{"ready_log", p.ReadyLog != ""},
		{"ready_port", p.ReadyPort != 0},
		{"ready_url", p.ReadyURL != ""},
		{"ready", p.Ready != ""},
	} {
		if c.on {
			set = append(set, c.field)
		}
	}
	return set
}

// ResolvedDir returns Dir with a leading ~ expanded to the user's home directory.
func (p *Project) ResolvedDir() (string, error) {
	return ExpandHome(p.Dir)
}

// ResolvedEnvFile returns the absolute path of EnvFile (resolved relative to the
// project dir), or "" if no env file is configured.
func (p *Project) ResolvedEnvFile() (string, error) {
	if p.EnvFile == "" {
		return "", nil
	}
	envFile, err := ExpandHome(p.EnvFile)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(envFile) {
		return envFile, nil
	}
	dir, err := p.ResolvedDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, envFile), nil
}

// Label is the name shown in the TUI.
func (p *Project) Label(name string) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return name
}

// Duration is a time.Duration that is stored in TOML as a string like "5s".
type Duration struct {
	time.Duration
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. \"5s\" or \"1m30s\")", b)
	}
	d.Duration = v
	return nil
}

// New returns an empty config.
func New() *Config {
	c := &Config{}
	c.init()
	return c
}

func (c *Config) init() {
	if c.Projects == nil {
		c.Projects = map[string]*Project{}
	}
	if c.Groups == nil {
		c.Groups = map[string][]string{}
	}
}

// Path returns the config file location: $RUNR_CONFIG if set, otherwise
// runr/config.toml under the OS user config directory.
func Path() (string, error) {
	if p := os.Getenv(EnvVar); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(dir, "runr", "config.toml"), nil
}

// Load reads the config at path. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	c := &Config{}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("config %s: unknown field\n%s", path, strict.String())
		}
		var decErr *toml.DecodeError
		if errors.As(err, &decErr) {
			row, col := decErr.Position()
			return nil, fmt.Errorf("config %s:%d:%d: %s", path, row, col, strings.TrimPrefix(decErr.Error(), "toml: "))
		}
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.init()
	for name, p := range c.Projects {
		if p == nil {
			c.Projects[name] = &Project{}
		}
	}
	return c, nil
}

// Save writes the config to path atomically, creating parent directories as needed.
// The file is private (0600) since env values may contain secrets.
func Save(path string, c *Config) error {
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding %q: %w", path, err)
	}
	return filepath.Join(home, path[1:]), nil
}

// DisplayPath shortens paths under the home directory to ~/... for output.
func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}

// NormalizeDir makes dir absolute, keeping a leading ~ so the config stays
// portable between machines.
func NormalizeDir(dir string) (string, error) {
	if dir == "" || strings.HasPrefix(dir, "~") {
		return dir, nil
	}
	return filepath.Abs(dir)
}

// Clone returns a deep copy of the config.
func (c *Config) Clone() *Config {
	out := &Config{Settings: c.Settings}
	if c.Projects != nil {
		out.Projects = make(map[string]*Project, len(c.Projects))
		for name, p := range c.Projects {
			cp := *p
			cp.Env = maps.Clone(p.Env)
			cp.DependsOn = slices.Clone(p.DependsOn)
			out.Projects[name] = &cp
		}
	}
	if c.Groups != nil {
		out.Groups = make(map[string][]string, len(c.Groups))
		for name, members := range c.Groups {
			out.Groups[name] = slices.Clone(members)
		}
	}
	return out
}

// RemoveProject deletes a project and every reference to it: it is removed
// from groups and from the depends_on of other projects. It returns the
// groups and the dependents that were changed, sorted.
func (c *Config) RemoveProject(name string) (groups, dependents []string) {
	dependents = c.Dependents(name)
	delete(c.Projects, name)
	for _, dep := range dependents {
		p := c.Projects[dep]
		p.DependsOn = slices.DeleteFunc(p.DependsOn, func(d string) bool { return d == name })
	}
	for _, group := range slices.Sorted(maps.Keys(c.Groups)) {
		members := c.Groups[group]
		if slices.Contains(members, name) {
			c.Groups[group] = slices.DeleteFunc(members, func(m string) bool { return m == name })
			groups = append(groups, group)
		}
	}
	return groups, dependents
}

// ColorOf returns the color of a project: its configured one, or else one of
// DefaultColors. The projects without a color get them in name order, so a
// project has the same color in the launcher, every session and --plain
// output, until projects without a color are added or removed.
func (c *Config) ColorOf(name string) string {
	if p := c.Projects[name]; p != nil && p.Color != "" {
		return p.Color
	}
	i := 0
	for other, p := range c.Projects {
		if p.Color == "" && other < name {
			i++
		}
	}
	return DefaultColors[i%len(DefaultColors)]
}
