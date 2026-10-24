package tui

import (
	"image/color"
	"strings"

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
	selection color.Color // background of the selected row and keycaps
	rule      color.Color // separator lines
	alert     color.Color // background of toasts about failures
	match     color.Color // background of search matches
	ansi      [16]color.Color
}

// theme is a palette for dark and one for light terminal backgrounds.
type theme struct {
	dark, light palette
	darkOnly    bool // no light palette: light terminals get the terminal theme's
}

// terminalANSI are the terminal's own 16 colors.
var terminalANSI = [16]color.Color{
	ansi.BasicColor(0), ansi.BasicColor(1), ansi.BasicColor(2), ansi.BasicColor(3),
	ansi.BasicColor(4), ansi.BasicColor(5), ansi.BasicColor(6), ansi.BasicColor(7),
	ansi.BasicColor(8), ansi.BasicColor(9), ansi.BasicColor(10), ansi.BasicColor(11),
	ansi.BasicColor(12), ansi.BasicColor(13), ansi.BasicColor(14), ansi.BasicColor(15),
}

// terminalTheme uses the terminal's own 16 colors. The selection and the
// rules (the accent mixed 40% into the background) are grays and teals from
// the 256-color palette.
var terminalTheme = theme{
	dark: palette{
		accent:    ansi.BasicColor(ansiCyan),
		selection: ansi.IndexedColor(236), // #303030
		rule:      ansi.IndexedColor(23),  // #005f5f
		alert:     ansi.IndexedColor(52),  // #5f0000
		ansi:      terminalANSI,
	},
	light: palette{
		accent:    ansi.BasicColor(ansiCyan),
		selection: ansi.IndexedColor(254), // #e4e4e4
		rule:      ansi.IndexedColor(152), // #afd7d7
		alert:     ansi.IndexedColor(224), // #ffd7d7
		ansi:      terminalANSI,
	},
}

// styles are the styles the UI is drawn with, built from a theme for the
// terminal's color profile and background. The launcher, the running view,
// pickers and forms share one *styles.
type styles struct {
	theme theme
	pal   palette // the theme's palette for the background
	// profile is the terminal's color profile, from tea.ColorProfileMsg
	// (TrueColor until it arrives). Below 256 colors, tints fall back to
	// reverse video, bold or faint text.
	profile colorprofile.Profile
	dark    bool // the terminal's background, from tea.BackgroundColorMsg (dark until it arrives)

	plain, bold, faint lipgloss.Style
	ok, warn, err      lipgloss.Style // green, yellow, red
	rule               lipgloss.Style
	accent             lipgloss.Style
	badge              string         // " runr " in the header
	onAccent           color.Color    // text on the accent and other pill colors
	keycap             lipgloss.Style // the key of a hint; zero: bold keys
	keycaps            bool
	hintGap            string // between hints

	// selBar is the SGR sequence for the selected row's background; empty
	// when the selected name is shown in reverse video instead.
	selBar string
	alert  string // SGR sequence for a failure toast's background, or empty

	// SGR sequences for log lines, which are styled as raw text.
	system    string // runr's own lines
	systemErr string // runr's lines about failures
	match     string // search matches
	current   string // the search match jumped to
}

func newStyles(t theme) *styles {
	s := &styles{theme: t, profile: colorprofile.TrueColor, dark: true}
	s.build()
	return s
}

// setProfile rebuilds the styles for the terminal's color profile.
func (s *styles) setProfile(p colorprofile.Profile) {
	s.profile = p
	s.build()
}

// setDark rebuilds the styles for a dark or light terminal background.
func (s *styles) setDark(dark bool) {
	s.dark = dark
	s.build()
}

func (s *styles) build() {
	t := s.theme
	tints := s.profile >= colorprofile.ANSI256 // background tints and subtle colors read well
	if !tints {
		// A theme's colors would be approximated badly on 16 colors; the
		// terminal's own read better.
		t = terminalTheme
	}
	p := t.variant(s.dark)
	s.pal = p

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
	if p.rule != nil && tints {
		s.rule = lipgloss.NewStyle().Foreground(p.rule)
	}

	s.accent = lipgloss.NewStyle().Foreground(p.accent)
	s.onAccent = p.bg
	if s.onAccent == nil {
		s.onAccent = p.ansi[ansiBlack]
	}

	s.keycaps = p.selection != nil && tints
	s.keycap, s.hintGap, s.selBar = s.bold, "  ", ""
	if s.keycaps {
		s.keycap = lipgloss.NewStyle().Bold(true).Background(p.selection)
		if p.fg != nil {
			s.keycap = s.keycap.Foreground(p.fg)
		}
		s.hintGap = " "
		s.selBar = ansi.Style{}.BackgroundColor(p.selection).String()
	}
	s.alert = ""
	if p.alert != nil && tints {
		s.alert = ansi.Style{}.BackgroundColor(p.alert).String()
	}

	s.badge = s.pill(" runr ", p.accent)

	if p.dim != nil {
		s.system = ansi.Style{}.ForegroundColor(p.dim).Italic(true).String()
	} else {
		s.system = ansi.Style{}.Faint().Italic(true).String()
	}
	s.systemErr = ansi.Style{}.ForegroundColor(p.ansi[ansiRed]).Italic(true).String()
	s.match = ansi.Style{}.Reverse(true).String()
	if p.match != nil {
		s.match = ansi.Style{}.BackgroundColor(p.match).String()
	}
	s.current = ansi.Style{}.ForegroundColor(s.onAccent).BackgroundColor(p.ansi[ansiYellow]).String()
}

// variant returns the palette for a dark or light background.
func (t theme) variant(dark bool) palette {
	switch {
	case dark:
		return t.dark
	case t.darkOnly:
		return terminalTheme.light
	}
	return t.light
}

// color resolves a valid project color: a named color through the palette,
// an ANSI index or hex code as is.
func (s *styles) color(c string) color.Color {
	return s.pal.color(c)
}

func (p palette) color(c string) color.Color {
	for i, name := range config.ColorNames {
		if c == name {
			return p.ansi[i]
		}
	}
	return lipgloss.Color(c)
}

// ProjectColor resolves a valid project color in the named theme, for a dark
// or light terminal background. It's for output outside the TUI.
func ProjectColor(themeName, c string, dark bool) color.Color {
	return themeNamed(themeName).variant(dark).color(c)
}

// onBackground gives styled text the background set by the SGR sequence
// bg, also after the resets inside it.
func onBackground(text, bg string) string {
	text = strings.ReplaceAll(text, "\x1b[m", "\x1b[m"+bg)
	text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bg)
	return bg + text + "\x1b[m"
}
