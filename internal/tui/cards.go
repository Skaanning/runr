package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

const (
	cardWidth  = 40 // of a group card, borders included
	cardGap    = 2  // between cards in a row
	listIndent = 3  // before cards and project rows; the cursor marker sits in it

	// wordmarkMinHeight is the terminal height from which the launcher shows
	// the wordmark instead of the header.
	wordmarkMinHeight = 20
)

// wordmark is "runr" in box-drawing characters, three cells per letter.
var wordmark = [3]string{
	"┏━┓╻ ╻┏┓╻┏━┓",
	"┣┳┛┃ ┃┃┗┫┣┳┛",
	"╹┗╸┗━┛╹ ╹╹┗╸",
}

// showWordmark reports whether the launcher shows the wordmark: on tall
// terminals, while the list is shown.
func (m *launcherModel) showWordmark() bool {
	return m.height >= wordmarkMinHeight && m.form == nil && m.confirm == nil && m.picker == nil && m.output == nil
}

// wordmarkRows renders the wordmark with the size of the registry next to
// it, between blank rows.
func (m *launcherModel) wordmarkRows() []string {
	rows := []string{""}
	for i, line := range wordmark {
		letters := []rune(line)
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", listIndent))
		for k, style := range m.st.wordmark {
			b.WriteString(style.Render(string(letters[k*3 : k*3+3])))
		}
		switch {
		case i == 1:
			b.WriteString("   " + m.st.faint.Render(m.registrySize()))
		case i == 2:
			if names := m.lastNames(); len(names) > 0 {
				b.WriteString("   " + m.st.hint("l", "last session: "+strings.Join(names, ", ")))
			}
		}
		rows = append(rows, b.String())
	}
	return append(rows, "")
}

// registrySize returns e.g. "4 projects · 2 groups".
func (m *launcherModel) registrySize() string {
	return count(len(m.cfg.Projects), "project") + " · " + count(len(m.cfg.Groups), "group")
}

// groupCount is the number of groups, which come first in items.
func (m *launcherModel) groupCount() int {
	n := 0
	for n < len(m.items) && m.items[n].group {
		n++
	}
	return n
}

// cardWidth is the width of a group card, narrower on narrow terminals.
func (m *launcherModel) cardWidth() int {
	return max(min(cardWidth, m.width-listIndent-1), 12)
}

// cardsPerRow is how many group cards fit next to each other.
func (m *launcherModel) cardsPerRow() int {
	return max((m.width-listIndent+cardGap)/(m.cardWidth()+cardGap), 1)
}

// move moves the cursor: through the cards row by row and then into the
// project list with up and down, between the cards of a row with left and
// right.
func (m *launcherModel) move(key string) {
	if sessions := len(m.bg.sessions()); m.sessCursor >= 0 {
		// The running sessions, one per row above the groups.
		switch {
		case key == "up":
			m.selectSession(max(m.sessCursor-1, 0))
		case key == "down" && m.sessCursor < sessions-1:
			m.selectSession(m.sessCursor + 1)
		case key == "down" && len(m.items) > 0:
			m.selectSession(-1)
			m.cursor = 0
		}
		return
	} else if key == "up" && sessions > 0 && m.cursor < max(min(m.cardsPerRow(), m.groupCount()), 1) {
		m.selectSession(sessions - 1) // from the top row of cards, or the first project
		return
	}
	groups, per := m.groupCount(), m.cardsPerRow()
	onCard := m.cursor < groups
	switch key {
	case "up":
		switch {
		case onCard && m.cursor >= per:
			m.cursor -= per
		case onCard:
		case m.cursor == groups && groups > 0:
			// From the first project to the last row of cards, in the column
			// the cursor left them from.
			m.cursor = min((groups-1)/per*per+m.cardCol, groups-1)
		default:
			m.cursor = max(m.cursor-1, 0)
		}
	case "down":
		switch {
		case onCard && m.cursor/per < (groups-1)/per:
			m.cursor = min(m.cursor+per, groups-1)
		case onCard && groups < len(m.items):
			m.cardCol = m.cursor % per
			m.cursor = groups
		case onCard:
		default:
			m.cursor = max(min(m.cursor+1, len(m.items)-1), 0)
		}
	case "left":
		if onCard && m.cursor%per > 0 {
			m.cursor--
		}
	case "right":
		if onCard && m.cursor%per < per-1 && m.cursor+1 < groups {
			m.cursor++
		}
	}
}

// cardRows renders the groups as cards, wrapped into rows, and returns the
// first and last row of the card under the cursor (-1 if it's not on a card).
// The cards of a row are as tall as the tallest of them.
func (m *launcherModel) cardRows() (rows []string, first, last int) {
	groups, per, width := m.groupCount(), m.cardsPerRow(), m.cardWidth()
	first, last = -1, -1
	for start := 0; start < groups; start += per {
		var contents [][]string
		height := 0
		for i := start; i < min(start+per, groups); i++ {
			lines := m.cardLines(m.items[i], width)
			contents = append(contents, lines)
			height = max(height, len(lines))
		}
		var cards [][]string
		for k, lines := range contents {
			i := start + k
			if m.isCursor(i) {
				first, last = len(rows), len(rows)+height+1
			}
			cards = append(cards, m.card(m.items[i], m.jumpLabelAt(i), m.isCursor(i), width, lines, height))
		}
		for line := range height + 2 {
			parts := make([]string, len(cards))
			for k, c := range cards {
				parts[k] = c[line]
			}
			rows = append(rows, strings.Repeat(" ", listIndent)+strings.Join(parts, strings.Repeat(" ", cardGap)))
		}
	}
	return rows, first, last
}

// cardBorder are the characters of a card's border.
type cardBorder struct{ tl, tr, bl, br, h, v string }

var (
	roundBorder = cardBorder{"╭", "╮", "╰", "╯", "─", "│"}
	thickBorder = cardBorder{"┏", "┓", "┗", "┛", "━", "┃"}
)

// card renders a group as a box of the given width: its name and member
// count in the top border, and its content lines (from cardLines) inside,
// with blank lines below them up to height. The card under the cursor has
// an accent border (without colors, a thick one). A jump mode letter (label)
// goes before the name.
func (m *launcherModel) card(it launcherItem, label string, current bool, width int, lines []string, height int) []string {
	b, border, title := roundBorder, m.st.faint, m.st.plain
	if current {
		border, title = m.st.accent, m.st.bold
		if m.st.profile < colorprofile.ANSI {
			b = thickBorder
		}
	}

	right := fmt.Sprintf(" %d %s%s", len(m.members(it)), b.h, b.tr)
	prefix, prefixWidth := "", 0
	if label != "" {
		prefix, prefixWidth = m.st.accent.Bold(true).Render(label)+" ", ansi.StringWidth(label)+1
	}
	name := ansi.Truncate(it.name, max(width-ansi.StringWidth(right)-5-prefixWidth, 1), "…")
	fill := width - 3 - prefixWidth - ansi.StringWidth(name) - 1 - ansi.StringWidth(right)
	rows := []string{border.Render(b.tl+b.h+" ") + prefix + title.Render(name) + border.Render(" "+strings.Repeat(b.h, max(fill, 0))+right)}

	for i := range height {
		inner := ""
		if i < len(lines) {
			inner = lines[i]
		}
		inner = pad(ansi.Truncate(inner, width-3, "…"), width-2)
		rows = append(rows, border.Render(b.v)+inner+border.Render(b.v))
	}
	return append(rows, border.Render(b.bl+strings.Repeat(b.h, width-2)+b.br))
}

// cardLines returns the inside of a group's card of the given width: the
// checkbox and a colored dot and name per member, wrapped into lines, the
// later ones indented under the first member.
func (m *launcherModel) cardLines(it launcherItem, width int) []string {
	members := m.members(it)
	room := width - 3 // inside the borders, a cell kept free on the right
	line := " " + m.checkbox(members) + " "
	indent := strings.Repeat(" ", ansi.StringWidth(line))
	if len(members) == 0 {
		return []string{line + m.st.faint.Render("(empty)")}
	}
	var lines []string
	for i, p := range members {
		item := lipgloss.NewStyle().Foreground(m.st.color(m.cfg.ColorOf(p))).Render("●") + " " + p
		switch {
		case i == 0:
		case ansi.StringWidth(line)+2+ansi.StringWidth(item) <= room:
			line += "  "
		default:
			lines = append(lines, line)
			line = indent
		}
		line += item
	}
	return append(lines, line)
}

// checkbox renders whether all, some or none of the projects are selected,
// in the accent unless none are.
func (m *launcherModel) checkbox(projects []string) string {
	switch n := m.countSelected(projects); {
	case len(projects) > 0 && n == len(projects):
		return m.st.accent.Render("[x]")
	case n > 0:
		return m.st.accent.Render("[-]")
	}
	return m.st.faint.Render("[ ]")
}
