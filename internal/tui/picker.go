package tui

import (
	"cmp"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// pickerItem is something to pick: a group or project to start, a branch or
// a theme. Items of a section follow each other.
type pickerItem struct {
	name    string // identifies the item
	label   string // shown name; the filter's matches are highlighted in it
	group   bool
	section string // heading of the item's section, e.g. "Local"; empty: none
	mark    string // styled, before the label, e.g. a color dot
	track   string // after the label, e.g. "↑2 ↓1"
	swatch  string // styled text shown between the label and info, if set
	info    string // details after the label, faint; dropped first when narrow
	pill    string // right-aligned, e.g. "current"
	right   string // right-aligned, faint, e.g. "3 days ago"
}

// picker is a filterable list of items, shown in its own pane in place of
// the log (running view) or the list (launcher).
type picker struct {
	title  string // in the pane's border, e.g. "switch branch · api"
	action string // what enter does, e.g. "start"
	mode   string // status bar pill, if not the action upper-cased
	// context is a styled line above the filter, e.g. the project and its
	// branch; chip is text shown at its right, e.g. "also switches: web".
	context, chip string
	// contextFor, if set, renders the context line for a width instead.
	contextFor func(width int) string
	note       string                   // shown under the filter, if set
	empty      string                   // shown when there are no items
	noun       string                   // what the items are, for the count: "branch"
	pick       func(pickerItem) tea.Cmd // called with the chosen item
	// footer, if set, returns a styled line about the item under the cursor,
	// e.g. the command enter runs.
	footer func(pickerItem) string
	// preview, if set, is called with the item under the cursor whenever it
	// changes; cancel, if set, when the picker is closed with esc.
	preview func(pickerItem)
	cancel  func()
	// keys, if set, handles a key before the picker does, and reports
	// whether it did; hints, if set, replace the help line's key hints.
	keys       func(key string) (bool, tea.Cmd)
	hints      [][2]string
	labelWidth int // widest label shown; 0 for 24

	st       *styles
	input    textinput.Model
	items    []pickerItem
	filtered []int // indexes into items
	cursor   int   // index into filtered
	top      int   // first list row shown
}

func newPicker(st *styles, p *picker) (*picker, tea.Cmd) {
	p.st = st
	p.input = textinput.New()
	p.input.Prompt = ""
	styles := textinput.DefaultStyles(true)
	styles.Cursor.Blink = false
	p.input.SetStyles(styles)
	p.filter()
	return p, p.input.Focus()
}

// modeName is the status bar pill shown while the picker is open.
func (p *picker) modeName() string {
	if p.mode != "" {
		return strings.ToUpper(p.mode)
	}
	return strings.ToUpper(p.action)
}

// titleText renders the title: the part before " · " in the accent.
func (p *picker) titleText() string {
	head, rest, found := strings.Cut(p.title, " · ")
	title := p.st.heading.Render(head)
	if found {
		title += p.st.faint.Render(" · ") + rest
	}
	return title
}

// update handles a key other than enter and esc, or a paste into the filter.
func (p *picker) update(msg tea.Msg) tea.Cmd {
	before, _ := p.selected()
	defer func() {
		if after, ok := p.selected(); ok && after.name != before.name && p.preview != nil {
			p.preview(after)
		}
	}()
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "up":
			p.cursor = max(p.cursor-1, 0)
			return nil
		case "down":
			p.cursor = max(min(p.cursor+1, len(p.filtered)-1), 0)
			return nil
		}
	}
	var cmd tea.Cmd
	query := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != query {
		p.filter()
	}
	return cmd
}

// filter keeps the items whose name or label contains the input, ignoring case.
func (p *picker) filter() {
	q := strings.ToLower(p.input.Value())
	p.filtered = p.filtered[:0]
	for i, it := range p.items {
		if strings.Contains(strings.ToLower(it.name), q) || strings.Contains(strings.ToLower(it.label), q) {
			p.filtered = append(p.filtered, i)
		}
	}
	p.cursor, p.top = 0, 0
}

// selected returns the item under the cursor, if any.
func (p *picker) selected() (pickerItem, bool) {
	if p.cursor < len(p.filtered) {
		return p.items[p.filtered[p.cursor]], true
	}
	return pickerItem{}, false
}

// view returns exactly height rows of at most width cells: the context line,
// the filter with the match count, the list in sections, and the footer.
func (p *picker) view(width, height int) []string {
	p.input.SetWidth(max(width-20, 1))
	// The input pads its view to its width.
	filter := p.st.accent.Bold(true).Render("›") + " " + strings.TrimRight(p.input.View(), " ")
	if p.input.Value() == "" {
		filter += p.st.faint.Italic(true).Render(" type to filter")
	}
	countText := count(len(p.items), p.noun)
	if p.input.Value() != "" {
		countText = fmt.Sprintf("%d/%d", len(p.filtered), len(p.items))
	}
	filter = spreadOr(filter, p.st.faint.Render(countText), width)

	var context, note, footer []string
	if p.contextFor != nil {
		p.context = p.contextFor(width)
	}
	if p.context != "" {
		line := p.context
		if p.chip != "" {
			line = spreadOr(line, p.st.segment(" "+p.chip+" "), width)
		}
		context = []string{line, ""}
	}
	if p.note != "" {
		note = []string{p.st.warn.Render(p.note), ""}
	}
	if it, ok := p.selected(); ok && p.footer != nil {
		if f := p.footer(it); f != "" {
			footer = []string{"", f}
		}
	}

	// On short panes, drop the blank lines, then the footer, the context and
	// the note, so that at least one item shows.
	list, cursorFirst, cursorLast := p.listRows(width)
	blank := []string{""} // under the filter
	fixed := func() int { return len(context) + 1 + len(blank) + len(note) + len(footer) }
	if height-fixed() < min(len(list), 3) {
		context, blank, note, footer = trimBlank(context), nil, trimBlank(note), trimBlank(footer)
	}
	for _, drop := range []*[]string{&footer, &context, &note} {
		if height-fixed() >= 1 {
			break
		}
		*drop = nil
	}
	rows := append(append(append(context, filter), blank...), note...)
	listHeight := max(height-len(rows)-len(footer), 0)

	// The cursor's heading comes into view when there's room; the cursor's
	// row always does.
	if cursorLast >= p.top+listHeight {
		p.top = max(cursorLast-listHeight+1, cursorFirst)
	}
	if cursorFirst < p.top {
		p.top = max(cursorFirst, cursorLast-listHeight+1)
	}
	p.top = max(min(p.top, len(list)-listHeight), 0)
	for k := p.top; k < len(list) && k < p.top+listHeight; k++ {
		rows = append(rows, list[k])
	}
	for len(rows) < height-len(footer) {
		rows = append(rows, "")
	}
	rows = append(rows, footer...)
	for i, r := range rows {
		rows[i] = ansi.Truncate(r, width, "…")
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows[:height]
}

// trimBlank removes the blank lines from lines.
func trimBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// spreadOr puts left and right at the edges of width cells, or returns left
// alone if they don't fit.
func spreadOr(left, right string, width int) string {
	if line, ok := spread(left, right, width); ok {
		return line
	}
	return left
}

// listRows renders the filtered items with their section headings, and
// returns the first and last row to keep in view for the cursor: its row,
// and its heading if it's the first in its section.
func (p *picker) listRows(width int) (rows []string, first, last int) {
	switch {
	case len(p.items) == 0:
		return []string{p.st.faint.Render(p.empty)}, 0, 0
	case len(p.filtered) == 0:
		return []string{p.st.faint.Render("no matches")}, 0, 0
	}
	nameWidth, markWidth, trackWidth := 0, 0, 0
	for _, i := range p.filtered {
		it := p.items[i]
		nameWidth = max(nameWidth, ansi.StringWidth(it.label))
		markWidth = max(markWidth, ansi.StringWidth(it.mark))
		trackWidth = max(trackWidth, ansi.StringWidth(it.track))
	}
	nameWidth = min(nameWidth, cmp.Or(p.labelWidth, 24))

	section := ""
	for k, i := range p.filtered {
		it := p.items[i]
		heading := -1
		if k == 0 || it.section != section {
			if it.section != "" {
				if k > 0 {
					rows = append(rows, "")
				}
				heading = len(rows)
				rows = append(rows, p.st.heading.Render(it.section))
			}
			section = it.section
		}
		if k == p.cursor {
			first, last = len(rows), len(rows)
			if heading >= 0 {
				first = heading
			}
		}
		rows = append(rows, p.itemRow(it, k == p.cursor, width, nameWidth, markWidth, trackWidth))
	}
	return rows, first, last
}

// itemRow renders an item: selection marker, mark, label with the filter's
// match highlighted, track, swatch and info on the left; pill and right on
// the right. The info is cut first when the row doesn't fit.
func (p *picker) itemRow(it pickerItem, selected bool, width, nameWidth, markWidth, trackWidth int) string {
	nameStyle := p.st.plain
	marker := " "
	if selected {
		marker = p.st.accent.Render("▌")
		nameStyle = p.st.bold
		if p.st.selBar == "" {
			nameStyle = nameStyle.Reverse(true)
		}
	}
	left := marker + " "
	if markWidth > 0 {
		left = marker + pad(it.mark, markWidth) + " "
	}
	left += pad(ansi.Truncate(p.highlight(it.label, nameStyle), nameWidth, "…"), nameWidth) + "  "
	if trackWidth > 0 {
		left += pad(it.track, trackWidth) + "  "
	}
	if it.swatch != "" {
		left += it.swatch + "  "
	}

	var parts []string
	if it.pill != "" {
		parts = append(parts, p.st.pill(" "+it.pill+" ", p.st.pal.accent))
	}
	if it.right != "" {
		parts = append(parts, p.st.faint.Render(it.right))
	}
	right := strings.Join(parts, " ")
	room := width - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if it.info != "" && room >= 4 {
		left += p.st.faint.Render(ansi.Truncate(it.info, room, "…"))
	}
	row := spreadOr(left, right, width)
	if selected && p.st.selBar != "" {
		row = onBackground(pad(ansi.Truncate(row, width, "…"), width), p.st.selBar)
	}
	return row
}

// highlight renders label in style, with the part matching the filter in
// the accent and bold.
func (p *picker) highlight(label string, style lipgloss.Style) string {
	q := p.input.Value()
	lower := strings.ToLower(label)
	i := strings.Index(lower, strings.ToLower(q))
	if q == "" || i < 0 || len(lower) != len(label) {
		return style.Render(label)
	}
	j := i + len(q)
	return style.Render(label[:i]) + p.st.accent.Bold(true).Render(label[i:j]) + style.Render(label[j:])
}
