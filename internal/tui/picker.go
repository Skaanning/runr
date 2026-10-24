package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// pickerItem is a group or project that can be started.
type pickerItem struct {
	name  string
	label string // shown name
	group bool
	info  string // details shown after the name
}

// picker is a filterable list of items, shown in place of the log.
type picker struct {
	input    textinput.Model
	items    []pickerItem
	filtered []int // indexes into items
	cursor   int   // index into filtered
	top      int   // first filtered item shown
}

func newPicker(items []pickerItem) (*picker, tea.Cmd) {
	p := &picker{items: items, input: textinput.New()}
	p.input.Prompt = ""
	styles := textinput.DefaultStyles(true)
	styles.Cursor.Blink = false
	p.input.SetStyles(styles)
	p.filter()
	return p, p.input.Focus()
}

// update handles a key other than enter and esc.
func (p *picker) update(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "down":
		p.cursor = max(min(p.cursor+1, len(p.filtered)-1), 0)
		return nil
	}
	var cmd tea.Cmd
	before := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
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

// view returns exactly height rows of at most width cells.
func (p *picker) view(width, height int) []string {
	p.input.SetWidth(max(width-8, 1))
	rows := []string{boldStyle.Render("start") + " " + p.input.View(), ""}

	listHeight := max(height-len(rows), 0)
	switch {
	case p.cursor < p.top:
		p.top = p.cursor
	case p.cursor >= p.top+listHeight:
		p.top = p.cursor - listHeight + 1
	}
	nameWidth := 0
	for _, i := range p.filtered {
		nameWidth = max(nameWidth, ansi.StringWidth(p.items[i].label))
	}
	nameWidth = min(nameWidth, 24)

	switch {
	case len(p.items) == 0:
		rows = append(rows, faintStyle.Render("everything is running"))
	case len(p.filtered) == 0:
		rows = append(rows, faintStyle.Render("no matches"))
	}
	for k := p.top; k < len(p.filtered) && len(rows) < height; k++ {
		it := p.items[p.filtered[k]]
		marker, style := "  ", plainStyle
		if k == p.cursor {
			marker, style = "▸ ", boldStyle
		}
		name := pad(ansi.Truncate(it.label, nameWidth, "…"), nameWidth)
		rows = append(rows, marker+style.Render(name)+"  "+faintStyle.Render(it.info))
	}
	for i, r := range rows {
		rows[i] = ansi.Truncate(r, width, "…")
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows[:height]
}
