package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// paneTop renders the top border of a rounded pane of width cells, with a
// title on the left and an optional note on the right:
// "╭─ logs · combined ───── following ─╮". The note is dropped, then the
// title truncated, when they don't fit.
func paneTop(width int, title, note string, border lipgloss.Style) string {
	left := border.Render("╭─ ") + title + " "
	right := border.Render("╮")
	if note != "" {
		right = " " + note + border.Render(" ─╮")
	}
	fill := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if fill < 1 && note != "" {
		right = border.Render("╮")
		fill = width - ansi.StringWidth(left) - 1
	}
	if fill < 0 {
		left = ansi.Truncate(left, max(width-2, 0), "…")
		fill = width - ansi.StringWidth(left) - 1
	}
	return left + border.Render(strings.Repeat("─", max(fill, 0))) + right
}

// framed draws rows in a rounded pane of width cells with an accent border
// and title, with a space on either side of the rows.
func framed(st *styles, title string, rows []string, width int) []string {
	out := []string{paneTop(width, title, "", st.accent)}
	for _, r := range rows {
		out = append(out, st.accent.Render("│")+" "+pad(ansi.Truncate(r, width-4, "…"), width-4)+" "+st.accent.Render("│"))
	}
	return append(out, paneBottom(width, st.accent))
}

// paneBottom renders the bottom border of a pane of width cells.
func paneBottom(width int, border lipgloss.Style) string {
	return border.Render("╰" + strings.Repeat("─", max(width-2, 0)) + "╯")
}

// statusBar is the line above the help line: a mode pill and the session
// on the left, the selected project and a clock on the right.
type statusBar struct {
	mode    string      // e.g. "FOLLOW"
	color   color.Color // of the mode pill
	title   string      // e.g. "backend + web", bold
	info    string      // styled, after the title, e.g. the state counts
	project string      // styled, e.g. "api · feat/login *"
	clock   string      // e.g. "14:32"
}

// statusBar renders b in width cells. When narrow, the project goes first,
// then the title, then the clock; the pill and info stay.
func (s *styles) statusBar(b statusBar, width int) string {
	for drop := 0; ; drop++ {
		project, title, clock := b.project, b.title, b.clock
		if drop > 0 {
			project = ""
		}
		if drop > 1 {
			title = ""
		}
		if drop > 2 {
			clock = ""
		}
		left := s.pill(" "+b.mode+" ", b.color)
		var seg []string
		if title != "" {
			seg = append(seg, s.bold.Render(title))
		}
		if b.info != "" {
			seg = append(seg, b.info)
		}
		if len(seg) > 0 {
			left += s.segment(" " + strings.Join(seg, "  ") + " ")
		}
		var right string
		if project != "" {
			right += s.segment(" " + project + " ")
		}
		if clock != "" {
			right += s.pill(" "+clock+" ", s.pal.accent)
		}
		if line, ok := spread(" "+left, right+" ", width); ok || drop > 2 {
			if !ok {
				line = ansi.Truncate(" "+left, width, "…")
			}
			return line
		}
	}
}

// pill renders text on a colored background, like the status bar's mode.
func (s *styles) pill(text string, bg color.Color) string {
	if s.profile < colorprofile.ANSI {
		return s.bold.Reverse(true).Render(text)
	}
	return lipgloss.NewStyle().Bold(true).Background(bg).Foreground(s.onAccent).Render(text)
}

// segment renders styled text on the selection background, if the terminal
// has the colors for it.
func (s *styles) segment(text string) string {
	if s.selBar == "" {
		return text
	}
	return onBackground(text, s.selBar)
}

// clockMsg updates the clock in the status bar.
type clockMsg struct{ owner any }

// clockTick returns a command that sends owner a clockMsg at the start of
// the next minute.
func clockTick(owner any, now time.Time) tea.Cmd {
	next := now.Truncate(time.Minute).Add(time.Minute)
	return tea.Tick(next.Sub(now), func(time.Time) tea.Msg { return clockMsg{owner: owner} })
}
