package tui

import (
	"image/color"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/proc"
	"github.com/Skaanning/runr/internal/supervisor"
)

func TestThemesMatchConfig(t *testing.T) {
	if got := slices.Sorted(maps.Keys(themes)); !slices.Equal(got, slices.Sorted(slices.Values(config.ThemeNames))) {
		t.Errorf("themes %v, config.ThemeNames %v", got, config.ThemeNames)
	}
	if !reflect.DeepEqual(themeNamed("bogus"), terminalTheme) || !reflect.DeepEqual(themeNamed("nord"), nord) {
		t.Error("themeNamed doesn't look themes up by name")
	}
}

// TestThemesDefineEveryColor checks every palette field of every theme but
// the terminal's, which leaves colors to the terminal on purpose.
func TestThemesDefineEveryColor(t *testing.T) {
	for name, th := range themes {
		if name == "terminal" {
			continue
		}
		variants := map[string]palette{"dark": th.dark}
		if !th.darkOnly {
			variants["light"] = th.light
		}
		for variant, p := range variants {
			v := reflect.ValueOf(p)
			for i := range v.NumField() {
				f, field := v.Field(i), v.Type().Field(i).Name
				if f.Kind() == reflect.Array {
					for j := range f.Len() {
						if f.Index(j).IsNil() {
							t.Errorf("%s %s: %s[%d] is not set", name, variant, field, j)
						}
					}
				} else if f.IsNil() {
					t.Errorf("%s %s: %s is not set", name, variant, field)
				}
			}
		}
	}
}

func TestThemeVariants(t *testing.T) {
	st := newStyles(catppuccin)
	if st.pal.accent != catppuccin.dark.accent {
		t.Error("dark is not assumed before the terminal answers")
	}
	st.setDark(false)
	if st.pal.accent != catppuccin.light.accent || st.color("red") != hex("#d20f39") {
		t.Error("light terminal doesn't get the light palette")
	}

	// Without a light variant, light terminals get the terminal theme's.
	st = newStyles(nord)
	st.setDark(false)
	if !reflect.DeepEqual(st.pal, terminalTheme.light) {
		t.Error("nord on a light terminal doesn't fall back to the terminal theme")
	}

	// 16 colors: the terminal's own.
	st = newStyles(tokyoNight)
	st.setProfile(colorprofile.ANSI)
	if !reflect.DeepEqual(st.pal, terminalTheme.dark) {
		t.Error("16 colors don't fall back to the terminal theme")
	}
	st.setProfile(colorprofile.ANSI256)
	if st.pal.accent != tokyoNight.dark.accent {
		t.Error("256 colors don't use the theme")
	}
}

func TestThemeTints(t *testing.T) {
	st := newStyles(gruvbox)
	bg := func(c color.Color) string { return ansi.Style{}.BackgroundColor(c).String() }
	if st.match != bg(mix(gruvbox.dark.ansi[ansiYellow], gruvbox.dark.bg, 0.35)) {
		t.Errorf("match = %q, want a yellow tint", st.match)
	}
	current := ansi.Style{}.ForegroundColor(hex("#282828")).BackgroundColor(hex("#d79921")).String()
	if st.current != current {
		t.Errorf("current = %q, want %q", st.current, current)
	}
	gray := color.RGBA{R: 102, G: 102, B: 102, A: 255}
	if got := mix(hex("#ffffff"), hex("#000000"), 0.4); got != color.Color(gray) {
		t.Errorf("mix = %v", got)
	}
}

// TestThemesRender draws the running view and the launcher in every theme,
// dark and light, in true color and 256 colors.
func TestThemesRender(t *testing.T) {
	for _, name := range config.ThemeNames {
		for _, dark := range []bool{true, false} {
			for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256} {
				st := newStyles(themeNamed(name))
				st.setProfile(profile)
				st.setDark(dark)
				sess := newFakeSession("db", "api", "worker")
				m := newRunningModel(sess, testConfig(), []string{"backend"}, st)
				m.now = testNow
				m.Update(tea.WindowSizeMsg{Width: 96, Height: 16})
				m.Update(eventsMsg{events: []supervisor.Event{
					state("db", supervisor.Running), state("api", supervisor.Starting),
					supervisor.StateEvent{Info: supervisor.ProcessInfo{Name: "worker", State: supervisor.Crashed, Exit: proc.ExitStatus{Code: 1}}},
					line("api", "listening on :8080"),
				}})
				out := m.render()
				accent := strings.TrimSuffix(ansi.Style{}.ForegroundColor(st.pal.accent).String(), "m")
				if !strings.Contains(out, accent) {
					t.Errorf("%s dark=%v %v: no accent color in the running view", name, dark, profile)
				}
				for i, r := range strings.Split(out, "\n") {
					if w := ansi.StringWidth(r); w > 96 {
						t.Errorf("%s dark=%v %v: row %d is %d cells", name, dark, profile, i, w)
					}
				}
				l := newLauncherModel(launcherConfig(t), nil, st)
				l.Update(tea.WindowSizeMsg{Width: 96, Height: 14})
				if !strings.Contains(l.render(), " runr ") {
					t.Errorf("%s: launcher didn't render", name)
				}
			}
		}
	}
}

func TestProjectColorsFollowBackground(t *testing.T) {
	m := newRunningModel(newFakeSession("api"), testConfig(), []string{"api"}, newStyles(catppuccin))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m.Update(tea.BackgroundColorMsg{Color: hex("#ffffff")})
	if got, want := m.byName["api"].style.GetForeground(), catppuccin.light.ansi[ansiCyan]; got != want {
		t.Errorf("api's color = %v after a light background, want latte cyan %v", got, want)
	}
}

func TestFormTheme(t *testing.T) {
	st := newStyles(tokyoNight)
	st.setDark(false)

	// The launcher's forms follow the styles.
	// The launcher's forms use it.
	l := newLauncherModel(launcherConfig(t), nil, st)
	l.Update(tea.WindowSizeMsg{Width: 96, Height: 30})
	l.Update(key("n"))
	if l.form == nil {
		t.Fatal("no form")
	}
	if out := l.render(); !strings.Contains(out, strings.TrimSuffix(ansi.Style{}.ForegroundColor(tokyoNight.light.accent).String(), "m")) {
		t.Error("form isn't drawn in the theme's accent")
	}
}

func TestThemeBackground(t *testing.T) {
	cfg := launcherConfig(t)
	cfg.Settings.ThemeValue = "catppuccin"
	m := newLauncherModel(cfg, nil, settingsStyles(cfg.Settings))
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 14})
	if v := m.View(); v.BackgroundColor != nil || v.ForegroundColor != nil {
		t.Fatal("painted without theme_background")
	}

	cfg.Settings.ThemeBackgroundValue = true
	m = newLauncherModel(cfg, nil, settingsStyles(cfg.Settings))
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 14})
	if v := m.View(); v.BackgroundColor != nil {
		t.Fatal("painted before the terminal told its background")
	}
	m.Update(tea.BackgroundColorMsg{Color: hex("#ffffff")})
	if v := m.View(); v.BackgroundColor != catppuccin.light.bg || v.ForegroundColor != catppuccin.light.fg {
		t.Fatalf("colors = %v on %v, want latte's", v.ForegroundColor, v.BackgroundColor)
	}

	// The theme picker's preview paints too; the terminal theme has no
	// background of its own.
	m.Update(key("t"))
	m.Update(key("down")) // tokyo-night
	if v := m.View(); v.BackgroundColor != tokyoNight.light.bg {
		t.Fatalf("preview background = %v, want tokyo-night day's", v.BackgroundColor)
	}
	m.Update(key("up"))
	m.Update(key("up")) // terminal
	if v := m.View(); v.BackgroundColor != nil || v.ForegroundColor != nil {
		t.Fatal("the terminal theme painted a background")
	}
	m.Update(key("esc"))

	// The running view shares the styles.
	r := newRunningModel(newFakeSession("api"), testConfig(), []string{"api"}, m.st)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	if v := r.View(); v.BackgroundColor != catppuccin.light.bg {
		t.Fatalf("running view background = %v, want latte's", v.BackgroundColor)
	}
	// Below 256 colors the terminal theme is used, so nothing is painted.
	r.Update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	if v := r.View(); v.BackgroundColor != nil {
		t.Fatal("painted on 16 colors")
	}
}
