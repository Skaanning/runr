package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
)

// Indexes into palette.ansi, in the order of config.ColorNames.
const (
	ansiBlack = iota
	ansiRed
	ansiGreen
	ansiYellow
	ansiBlue
	ansiMagenta
	ansiCyan
	ansiWhite
	ansiBrightBlack
	ansiBrightRed
	ansiBrightGreen
	ansiBrightYellow
	ansiBrightBlue
	ansiBrightMagenta
	ansiBrightCyan
	ansiBrightWhite
)

// palette is a theme's colors. A nil color leaves it to the terminal: its
// default foreground and background, faint text for dim and rule, and
// reverse video for selection.
type palette struct {
	fg, bg    color.Color
	dim       color.Color // secondary text
	accent    color.Color
	selection color.Color // background of the selected row
	rule      color.Color // separator lines
	ansi      [16]color.Color
}

// terminalPalette uses the terminal's own 16 colors.
var terminalPalette = palette{
	accent: ansi.BasicColor(ansiCyan),
	ansi: [16]color.Color{
		ansi.BasicColor(0), ansi.BasicColor(1), ansi.BasicColor(2), ansi.BasicColor(3),
		ansi.BasicColor(4), ansi.BasicColor(5), ansi.BasicColor(6), ansi.BasicColor(7),
		ansi.BasicColor(8), ansi.BasicColor(9), ansi.BasicColor(10), ansi.BasicColor(11),
		ansi.BasicColor(12), ansi.BasicColor(13), ansi.BasicColor(14), ansi.BasicColor(15),
	},
}

// styles are the styles the UI is drawn with, built from a palette. The
// launcher, the running view, pickers and forms share one *styles.
type styles struct {
	pal palette
	// profile is the terminal's color profile, from tea.ColorProfileMsg
	// (TrueColor until it arrives), for fallbacks where colors are scarce.
	profile colorprofile.Profile

	plain, bold, faint lipgloss.Style
	ok, warn, err      lipgloss.Style // green, yellow, red
	rule               lipgloss.Style

	// SGR sequences for log lines, which are styled as raw text.
	system  string // runr's own lines
	match   string // search matches
	current string // the search match jumped to
}

func newStyles(p palette) *styles {
	s := &styles{pal: p, profile: colorprofile.TrueColor}
	s.plain = lipgloss.NewStyle()
	s.bold = lipgloss.NewStyle().Bold(true)
	s.faint = lipgloss.NewStyle().Faint(true)
	if p.dim != nil {
		s.faint = lipgloss.NewStyle().Foreground(p.dim)
	}
	s.ok = lipgloss.NewStyle().Foreground(p.ansi[ansiGreen])
	s.warn = lipgloss.NewStyle().Foreground(p.ansi[ansiYellow])
	s.err = lipgloss.NewStyle().Foreground(p.ansi[ansiRed])
	s.rule = lipgloss.NewStyle().Faint(true)
	if p.rule != nil {
		s.rule = lipgloss.NewStyle().Foreground(p.rule)
	}

	if p.dim != nil {
		s.system = ansi.Style{}.ForegroundColor(p.dim).String()
	} else {
		s.system = ansi.Style{}.Faint().String()
	}
	s.match = ansi.Style{}.Reverse(true).String()
	s.current = ansi.Style{}.ForegroundColor(p.ansi[ansiBlack]).BackgroundColor(p.ansi[ansiYellow]).String()
	return s
}

// color resolves a valid project color: a named color through the palette,
// an ANSI index or hex code as is.
func (s *styles) color(c string) color.Color {
	for i, name := range config.ColorNames {
		if c == name {
			return s.pal.ansi[i]
		}
	}
	return lipgloss.Color(c)
}
