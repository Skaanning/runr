package tui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Skaanning/runr/internal/config"
)

func TestNamedColorsResolveThroughPalette(t *testing.T) {
	var p palette
	for i := range p.ansi {
		p.ansi[i] = color.RGBA{R: uint8(i), A: 0xff} // distinct from any terminal color
	}
	st := newStyles(p)
	if len(config.ColorNames) != len(p.ansi) {
		t.Fatalf("%d color names for %d palette colors", len(config.ColorNames), len(p.ansi))
	}
	for i, name := range config.ColorNames {
		if got := st.color(name); got != p.ansi[i] {
			t.Errorf("color(%q) = %v, want palette color %d", name, got, i)
		}
	}
	// Numbers and hex codes are used as is.
	for _, c := range []string{"3", "208", "#ff8800", "#f80"} {
		if got, want := st.color(c), lipgloss.Color(c); got != want {
			t.Errorf("color(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestTerminalPaletteKeepsANSIColors(t *testing.T) {
	st := newStyles(terminalPalette)
	for _, name := range config.ColorNames {
		if got, want := st.color(name), lipgloss.Color(config.ColorCode(name)); got != want {
			t.Errorf("color(%q) = %v, want %v", name, got, want)
		}
	}
	for _, tt := range []struct{ name, got, want string }{
		{"system", st.system, "\x1b[2m"},
		{"match", st.match, "\x1b[7m"},
		{"current", st.current, "\x1b[30;43m"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}
