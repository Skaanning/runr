package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// themes are the themes by their name in the config (config.ThemeNames).
var themes = map[string]theme{
	"terminal":    terminalTheme,
	"catppuccin":  catppuccin,
	"tokyo-night": tokyoNight,
	"gruvbox":     gruvbox,
	"nord":        nord,
}

// themeNamed returns the theme with a name from the config, or the terminal
// theme for an unknown one.
func themeNamed(name string) theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return terminalTheme
}

// ThemeAbout describes the variants of the theme with a name from the
// config, e.g. "mocha · latte".
func ThemeAbout(name string) string {
	return themeNamed(name).about
}

// truecolor builds a palette from hex colors. The tints are mixed from them
// as in the mock-ups: rules are the accent mixed 40% into the background,
// toasts 18% of red, search matches 35% of yellow.
func truecolor(fg, bg, dim, accent, selection string, ansi [16]string) palette {
	p := palette{
		fg: hex(fg), bg: hex(bg), dim: hex(dim), accent: hex(accent), selection: hex(selection),
	}
	for i, c := range ansi {
		p.ansi[i] = hex(c)
	}
	p.rule = mix(p.accent, p.bg, 0.4)
	p.alert = mix(p.ansi[ansiRed], p.bg, 0.18)
	p.match = mix(p.ansi[ansiYellow], p.bg, 0.35)
	return p
}

func hex(s string) color.Color { return lipgloss.Color(s) }

// mix returns a mixed with b, t parts a.
func mix(a, b color.Color, t float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	ch := func(x, y uint32) uint8 { return uint8((t*float64(x>>8) + (1-t)*float64(y>>8)) + 0.5) }
	return color.RGBA{R: ch(ar, br), G: ch(ag, bg), B: ch(ab, bb), A: 0xff}
}

// The palettes follow each theme's own terminal colors.
var (
	catppuccin = theme{
		dark: truecolor("#cdd6f4", "#1e1e2e", "#7f849c", "#cba6f7", "#313244", [16]string{ // mocha
			"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
			"#585b70", "#f37799", "#89d88b", "#ebd391", "#74a8fc", "#f2aede", "#6bd7ca", "#a6adc8",
		}),
		light: truecolor("#4c4f69", "#eff1f5", "#8c8fa1", "#8839ef", "#ccd0da", [16]string{ // latte
			"#5c5f77", "#d20f39", "#40a02b", "#df8e1d", "#1e66f5", "#ea76cb", "#179299", "#acb0be",
			"#6c6f85", "#de293e", "#49af3d", "#eea02d", "#456eff", "#fe85d8", "#2d9fa8", "#bcc0cc",
		}),
		about: "mocha · latte",
	}
	tokyoNight = theme{
		dark: truecolor("#c0caf5", "#1a1b26", "#737aa2", "#7aa2f7", "#283457", [16]string{ // night
			"#15161e", "#f7768e", "#9ece6a", "#e0af68", "#7aa2f7", "#bb9af7", "#7dcfff", "#a9b1d6",
			"#414868", "#ff899d", "#9fe044", "#faba4a", "#8db0ff", "#c7a9ff", "#a4daff", "#c0caf5",
		}),
		light: truecolor("#3760bf", "#e1e2e7", "#848cb5", "#2e7de9", "#c4c8da", [16]string{ // day
			"#b4b5b9", "#f52a65", "#587539", "#8c6c3e", "#2e7de9", "#9854f1", "#007197", "#6172b0",
			"#a1a6c5", "#ff4774", "#5c8524", "#a27629", "#358aff", "#a463ff", "#007ea8", "#3760bf",
		}),
		about: "night · day",
	}
	gruvbox = theme{
		dark: truecolor("#ebdbb2", "#282828", "#928374", "#fe8019", "#3c3836", [16]string{
			"#282828", "#cc241d", "#98971a", "#d79921", "#458588", "#b16286", "#689d6a", "#a89984",
			"#928374", "#fb4934", "#b8bb26", "#fabd2f", "#83a598", "#d3869b", "#8ec07c", "#ebdbb2",
		}),
		light: truecolor("#3c3836", "#fbf1c7", "#928374", "#af3a03", "#ebdbb2", [16]string{
			"#fbf1c7", "#cc241d", "#98971a", "#d79921", "#458588", "#b16286", "#689d6a", "#7c6f64",
			"#928374", "#9d0006", "#79740e", "#b57614", "#076678", "#8f3f71", "#427b58", "#3c3836",
		}),
		about: "dark · light",
	}
	// nord has no light variant: on a light background, runr uses the
	// terminal theme instead.
	nord = theme{
		dark: truecolor("#d8dee9", "#2e3440", "#616e88", "#88c0d0", "#434c5e", [16]string{
			"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
			"#4c566a", "#bf616a", "#a3be8c", "#ebcb8b", "#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
		}),
		darkOnly: true,
		about:    "dark only",
	}
)
