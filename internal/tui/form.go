package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// formColumnsMinWidth is the narrowest form, inside its pane, that shows
	// its sections in two columns.
	formColumnsMinWidth = 88
	formColumnGap       = 2
	envLines            = 3 // rows of a text field
)

// fieldKind is what a form field edits.
type fieldKind int

const (
	inputField  fieldKind = iota // one line of text
	textField                    // several lines of text
	choiceField                  // a set of options, any number of them chosen
)

// formOption is one of a choice field's options.
type formOption struct {
	value string
	color string // project color of the dot before it; "" for none
}

// formField is one field of a form, bound to the value it edits.
type formField struct {
	kind  fieldKind
	key   string // the config field it edits, e.g. "cmd"
	title string
	desc  string // under the title, faint

	input textinput.Model // inputField
	area  textarea.Model  // textField
	str   *string         // the value of an input or text field

	options []formOption // choiceField
	list    *[]string    // the chosen options' values
	option  int          // the option under the cursor

	check func() error  // validates the field; nil: always valid
	info  func() string // styled line under the field while it's valid, or ""
	adorn func() string // styled, before an input's value, e.g. a color swatch
	note  string        // faint, after an input, e.g. "↑↓ cycle"
	width int           // of an input's value, if narrower than the column
	// keys, if set, handles a key before the field does, and reports
	// whether it did; hint is its key hint for the help line.
	keys func(key string) bool
	hint [2]string
	// display, if set, renders a text field's value while it isn't focused.
	display func(width int) []string

	err     error // the latest result of check
	touched bool  // edited or left once: its error is shown
}

func newInputField(key, title string, value *string) *formField {
	in := textinput.New()
	in.Prompt = ""
	in.SetValue(*value)
	return &formField{kind: inputField, key: key, title: title, input: in, str: value}
}

func newTextField(key, title string, value *string) *formField {
	a := textarea.New()
	a.ShowLineNumbers = false
	a.Prompt = "│ "
	a.SetHeight(envLines)
	a.SetValue(*value)
	a.MoveToBegin()
	return &formField{kind: textField, key: key, title: title, area: a, str: value}
}

func newChoiceField(key, title string, options []formOption, chosen *[]string) *formField {
	return &formField{kind: choiceField, key: key, title: title, options: options, list: chosen}
}

// setValue changes an input field's value, as if typed.
func (fd *formField) setValue(s string) {
	fd.input.SetValue(s)
	fd.input.CursorEnd()
	*fd.str = s
}

// formSection is a titled part of a form.
type formSection struct {
	title  string
	right  bool // in the right column, when the form shows two
	fields []*formField
	// view, if set, renders the section's rows instead of fields, e.g. a
	// preview.
	view func(width int) []string
}

// form is a page of fields in sections, side by side when wide, ending in
// Save and Cancel buttons. Errors show under the fields that have them once
// a field was edited or left, and for every field once a save was tried.
type form struct {
	st       *styles
	sections []*formSection
	fields   []*formField // in focus order
	focus    int          // index into fields; then the Save and Cancel buttons
	top      int          // first row shown
	tried    bool         // a save was tried
	done     bool         // closed, with saved or cancelled
	saved    bool
}

func newForm(st *styles, sections ...*formSection) *form {
	f := &form{st: st, sections: sections}
	for _, s := range sections {
		f.fields = append(f.fields, s.fields...)
	}
	f.validate()
	return f
}

func (f *form) init() tea.Cmd {
	return f.focusOn(0)
}

func (f *form) saveButton() int   { return len(f.fields) }
func (f *form) cancelButton() int { return len(f.fields) + 1 }

// focused returns the focused field, or nil on the buttons.
func (f *form) focused() *formField {
	if f.focus < len(f.fields) {
		return f.fields[f.focus]
	}
	return nil
}

// focusOn moves the focus to field (or button) i. The field left counts as
// touched.
func (f *form) focusOn(i int) tea.Cmd {
	if fd := f.focused(); fd != nil && i != f.focus {
		fd.touched = true
		fd.input.Blur()
		fd.area.Blur()
	}
	f.focus = i
	fd := f.focused()
	if fd == nil {
		return nil
	}
	// Styled first: the styles turn off blinking, which would start a timer.
	switch fd.kind {
	case inputField:
		f.styleInput(&fd.input)
		return fd.input.Focus()
	case textField:
		f.styleArea(&fd.area)
		return fd.area.Focus()
	}
	return nil
}

// move moves the focus by d fields, wrapping around through the buttons if
// wrap is set.
func (f *form) move(d int, wrap bool) tea.Cmd {
	n := len(f.fields) + 2
	i := f.focus + d
	if wrap {
		i = (i + n) % n
	}
	return f.focusOn(max(min(i, n-1), 0))
}

// validate checks every field.
func (f *form) validate() {
	for _, fd := range f.fields {
		fd.err = nil
		if fd.check != nil {
			fd.err = fd.check()
		}
	}
}

// shownErr returns a field's error if it's shown.
func (f *form) shownErr(fd *formField) error {
	if fd.touched || f.tried {
		return fd.err
	}
	return nil
}

// errors returns the fields whose errors are shown.
func (f *form) errors() []*formField {
	var out []*formField
	for _, fd := range f.fields {
		if f.shownErr(fd) != nil {
			out = append(out, fd)
		}
	}
	return out
}

// submit closes the form as saved, or shows every error and moves to the
// first field with one.
func (f *form) submit() tea.Cmd {
	f.tried = true
	f.validate()
	for i, fd := range f.fields {
		if fd.err != nil {
			return f.focusOn(i)
		}
	}
	f.done, f.saved = true, true
	return nil
}

func (f *form) update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	key := k.String()
	fd := f.focused()
	if fd != nil && fd.keys != nil && fd.keys(key) {
		fd.touched = true
		f.validate()
		return nil
	}
	switch key {
	case "ctrl+s":
		return f.submit()
	case "tab":
		return f.move(1, true)
	case "shift+tab":
		return f.move(-1, true)
	case "enter":
		switch {
		case f.focus == f.saveButton():
			return f.submit()
		case f.focus == f.cancelButton():
			f.done = true
			return nil
		case fd.kind != textField: // a text field takes enter as a new line
			return f.move(1, false)
		}
	case "up", "down":
		d := 1
		if key == "up" {
			d = -1
		}
		switch {
		case fd == nil && d < 0:
			return f.focusOn(len(f.fields) - 1)
		case fd == nil:
			return nil
		case fd.kind != textField:
			return f.move(d, false)
		}
	case "left", "right":
		switch {
		case fd == nil:
			f.focus = f.saveButton() + f.cancelButton() - f.focus
			return nil
		case fd.kind == choiceField:
			if key == "left" {
				fd.option = max(fd.option-1, 0)
			} else {
				fd.option = max(min(fd.option+1, len(fd.options)-1), 0)
			}
			return nil
		}
	case "space":
		if fd != nil && fd.kind == choiceField && fd.option < len(fd.options) {
			v := fd.options[fd.option].value
			if i := slices.Index(*fd.list, v); i >= 0 {
				*fd.list = slices.Delete(*fd.list, i, i+1)
			} else {
				*fd.list = append(*fd.list, v)
			}
			fd.touched = true
			f.validate()
			return nil
		}
	}
	if fd == nil {
		return nil
	}
	var cmd tea.Cmd
	switch fd.kind {
	case inputField:
		fd.input, cmd = fd.input.Update(msg)
		if v := fd.input.Value(); v != *fd.str {
			*fd.str, fd.touched = v, true
		}
	case textField:
		fd.area, cmd = fd.area.Update(msg)
		if v := fd.area.Value(); v != *fd.str {
			*fd.str, fd.touched = v, true
		}
	}
	f.validate()
	return cmd
}

// keyHints returns the help line's hints for the focused field.
func (f *form) keyHints() [][2]string {
	keys := [][2]string{{"tab", "next"}, {"shift+tab", "back"}}
	fd := f.focused()
	switch {
	case fd == nil:
		action := "save"
		if f.focus == f.cancelButton() {
			action = "cancel"
		}
		keys = append(keys, [2]string{"←→", "choose"}, [2]string{"enter", action})
	case fd.hint[0] != "":
		keys = append(keys, fd.hint)
	case fd.kind == choiceField:
		keys = append(keys, [2]string{"←→", "move"}, [2]string{"space", "toggle"})
	case fd.kind == textField:
		keys = append(keys, [2]string{"enter", "new line"})
	}
	return append(keys, [2]string{"ctrl+s", "save"}, [2]string{"esc", "cancel"})
}

// view renders at most height rows of width cells, scrolled so the focused
// field is in view.
func (f *form) view(width, height int) []string {
	two := width >= formColumnsMinWidth && slices.ContainsFunc(f.sections, func(s *formSection) bool { return s.right })
	colWidth := width
	if two {
		colWidth = (width - formColumnGap) / 2
	}
	left, lFirst, lLast := f.column(func(s *formSection) bool { return !two || !s.right }, colWidth)
	right, rFirst, rLast := f.column(func(s *formSection) bool { return two && s.right }, colWidth)

	rows := []string{""}
	for i := range max(len(left), len(right)) {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		row := l
		if r != "" {
			row = pad(l, colWidth) + strings.Repeat(" ", formColumnGap) + r
		}
		rows = append(rows, row)
	}
	first, last := lFirst, lLast
	if rFirst >= 0 {
		first, last = rFirst, rLast
	}
	first, last = first+1, last+1 // the blank row on top
	rows = append(rows, "", "  "+f.button("Save", f.saveButton())+"  "+f.button("Cancel", f.cancelButton()))
	if f.focused() == nil {
		first, last = len(rows)-1, len(rows)-1
	}

	switch {
	case first < f.top:
		f.top = first
	case last >= f.top+height:
		f.top = max(last-height+1, first)
	}
	f.top = max(min(f.top, len(rows)-height), 0)
	return rows[f.top:min(f.top+height, len(rows))]
}

// column renders the sections that in reports, and the first and last row of
// the focused field if it's among them (-1 if not).
func (f *form) column(in func(*formSection) bool, width int) (rows []string, first, last int) {
	first, last = -1, -1
	focused := f.focused()
	for _, s := range f.sections {
		if !in(s) {
			continue
		}
		rows = append(rows, f.st.heading.Render(s.title), "")
		if s.view != nil {
			rows = append(rows, s.view(width)...)
			rows = append(rows, "")
			continue
		}
		for _, fd := range s.fields {
			fr := f.fieldRows(fd, fd == focused, width)
			if fd == focused {
				first, last = len(rows), len(rows)+len(fr)-1
			}
			rows = append(rows, fr...)
			rows = append(rows, "")
		}
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows, first, last
}

// fieldRows renders a field: title, description, value, and its error or
// info line, with a bar in the accent on the left while focused.
func (f *form) fieldRows(fd *formField, focused bool, width int) []string {
	st := f.st
	bar := "  "
	if focused {
		bar = st.accent.Render("┃") + " "
	}
	inner := max(width-2, 1)
	err := f.shownErr(fd)
	title := st.bold
	switch {
	case focused:
		title = st.heading
	case err != nil:
		title = st.err.Bold(true)
	}
	rows := []string{bar + title.Render(ansi.Truncate(fd.title, inner, "…"))}
	if fd.desc != "" {
		rows = append(rows, bar+st.faint.Render(ansi.Truncate(fd.desc, inner, "…")))
	}

	switch fd.kind {
	case inputField:
		f.styleInput(&fd.input)
		var adorn, note string
		if fd.adorn != nil {
			adorn = fd.adorn()
		}
		if fd.note != "" {
			note = "   " + st.faint.Render(fd.note)
		}
		w := inner - 2 - ansi.StringWidth(adorn) - ansi.StringWidth(note)
		if w < 8 {
			note, w = "", inner-2-ansi.StringWidth(adorn)
		}
		if fd.width > 0 {
			w = min(w, fd.width)
		}
		fd.input.SetWidth(max(w-1, 1)) // a cell for the cursor
		value := pad(ansi.Truncate(fd.input.View(), max(w, 1), "…"), w)
		rows = append(rows, bar+st.faint.Render("›")+" "+adorn+value+note)
	case textField:
		if focused || fd.display == nil {
			f.styleArea(&fd.area)
			fd.area.SetWidth(inner)
			for _, l := range strings.Split(fd.area.View(), "\n") {
				rows = append(rows, bar+l)
			}
		} else {
			for _, l := range fd.display(inner) {
				rows = append(rows, bar+l)
			}
		}
	case choiceField:
		for _, l := range f.choiceRows(fd, focused, inner) {
			rows = append(rows, bar+l)
		}
	}

	switch {
	case err != nil:
		// Two rows at most, the second indented under the message.
		lines := strings.Split(ansi.Wordwrap(err.Error(), max(inner-2, 1), " "), "\n")
		if len(lines) > 2 {
			lines[1] = strings.Join(lines[1:], " ")
			lines = lines[:2]
		}
		for i, l := range lines {
			prefix := "✗ "
			if i > 0 {
				prefix = "  "
			}
			rows = append(rows, bar+st.err.Render(ansi.Truncate(prefix+l, inner, "…")))
		}
	case fd.info != nil:
		if info := fd.info(); info != "" {
			rows = append(rows, bar+ansi.Truncate(info, inner, "…"))
		}
	}
	return rows
}

// choiceRows renders a choice field's options, "[x] ● db  [ ] ● api", wrapped
// into rows of width cells. The option under the cursor is highlighted while
// the field is focused.
func (f *form) choiceRows(fd *formField, focused bool, width int) []string {
	st := f.st
	if len(fd.options) == 0 {
		return []string{st.faint.Render("(none)")}
	}
	var rows []string
	row, rowWidth := "", 0
	for i, o := range fd.options {
		box := st.faint.Render("[ ]")
		if slices.Contains(*fd.list, o.value) {
			box = st.accent.Render("[x]")
		}
		item := box + " "
		if o.color != "" {
			item += lipgloss.NewStyle().Foreground(st.color(o.color)).Render("●") + " "
		}
		if focused && i == fd.option {
			label := st.bold.Render(o.value)
			if st.selBar == "" {
				label = st.bold.Reverse(true).Render(o.value)
			}
			item = st.segment(item + label)
		} else {
			item += o.value
		}
		w := ansi.StringWidth(item)
		if rowWidth > 0 && rowWidth+2+w > width {
			rows = append(rows, row)
			row, rowWidth = "", 0
		}
		if rowWidth > 0 {
			row += "  "
			rowWidth += 2
		}
		row += item
		rowWidth += w
	}
	return append(rows, row)
}

// button renders the Save or Cancel button, highlighted while focused.
func (f *form) button(label string, i int) string {
	if f.focus == i {
		return f.st.pill(" "+label+" ", f.st.pal.accent)
	}
	return f.st.segment(" " + label + " ")
}

// styleInput applies the current styles to a text input, so it follows
// theme changes.
func (f *form) styleInput(in *textinput.Model) {
	s := textinput.DefaultStyles(f.st.dark)
	s.Cursor.Blink = false
	s.Cursor.Color = f.st.pal.accent
	for _, st := range []*textinput.StyleState{&s.Focused, &s.Blurred} {
		st.Text, st.Placeholder, st.Prompt = f.st.plain, f.st.faint, f.st.plain
	}
	in.SetStyles(s)
}

func (f *form) styleArea(a *textarea.Model) {
	s := textarea.DefaultStyles(f.st.dark)
	s.Cursor.Blink = false
	s.Cursor.Color = f.st.pal.accent
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.Text, st.CursorLine, st.Placeholder = f.st.plain, f.st.plain, f.st.faint
		st.Prompt, st.EndOfBuffer = f.st.faint, f.st.plain
	}
	a.SetStyles(s)
}
