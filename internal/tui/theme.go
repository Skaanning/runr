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
	darkOnly    bool   // no light palette: light terminals get the terminal theme's
	about       string // its variants, shown in the theme picker and runr theme
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
	about: "the terminal's own colors",
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
	// paint is the theme_background setting; the theme's colors are painted
	// once the terminal has told its background (bgKnown).
	paint, bgKnown bool

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

// setDark rebuilds the styles for a dark or light terminal background, as
// the terminal reported it.
func (s *styles) setDark(dark bool) {
	s.dark, s.bgKnown = dark, true
	s.build()
}

// screen returns the colors to set as the terminal's default foreground and
// background, or nils to leave the terminal's own. Program output that resets
// its colors then falls back to the theme's, so painting leaves no holes.
// Nothing is painted before the terminal has answered the background query:
// it would answer with the painted color.
func (s *styles) screen() (fg, bg color.Color) {
	if !s.paint || !s.bgKnown || s.pal.bg == nil {
		return nil, nil
	}
	return s.pal.fg, s.pal.bg
}

// setTheme rebuilds the styles with another theme.
func (s *styles) setTheme(t theme) {
	s.theme = t
	s.build()
}

// tints reports whether the terminal shows background tints and subtle
// colors well, and so the themes' own colors.
func (s *styles) tints() bool {
	return s.profile >= colorprofile.ANSI256
}

func (s *styles) build() {
	t := s.theme
	tints := s.tints()
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

// swatch shows a theme's accent and its six colors as dots, for the
// terminal's background.
func (s *styles) swatch(t theme) string {
	p := t.variant(s.dark)
	var b strings.Builder
	for _, c := range []color.Color{
		p.accent, p.ansi[ansiRed], p.ansi[ansiGreen], p.ansi[ansiYellow],
		p.ansi[ansiBlue], p.ansi[ansiMagenta], p.ansi[ansiCyan],
	} {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("●"))
	}
	return b.String()
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
