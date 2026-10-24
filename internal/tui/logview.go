package tui

import "runr/internal/supervisor"

// formatFunc renders one log line as rows at most width cells wide.
type formatFunc func(line supervisor.LogLine, width int) []string

// logView is a scrollable, wrapping viewport over log lines.
//
// Only the visible lines are formatted. The scroll position is kept as a
// line index anchored to the top or bottom edge of the viewport rather than
// as a row offset, so wrapping never has to be computed for the whole buffer
// and resizing keeps the same lines in view.
type logView struct {
	lines  []supervisor.LogLine
	limit  int // most lines kept; older lines are dropped
	format formatFunc

	follow    bool // keep the newest line at the bottom
	anchor    int  // when not following: the line kept at an edge
	anchorTop bool // whether anchor is at the top edge (else the bottom)

	width, height int

	// Cached rendering, valid unless dirty.
	dirty       bool
	rows        []string
	first, last int  // lines visible in rows, partially at the edges; -1 if none
	atTop       bool // the first row is the start of the first line
}

func newLogView(limit int, format formatFunc) *logView {
	return &logView{limit: max(limit, 1), format: format, follow: true, dirty: true}
}

func (v *logView) append(lines ...supervisor.LogLine) {
	v.lines = append(v.lines, lines...)
	if over := len(v.lines) - v.limit; over > 0 {
		v.lines = v.lines[over:]
		v.anchor = max(v.anchor-over, 0)
	}
	v.dirty = true
}

func (v *logView) setSize(width, height int) {
	if width != v.width || height != v.height {
		v.width, v.height = width, height
		v.dirty = true
	}
}

// invalidate forces a new rendering, e.g. after the format changed.
func (v *logView) invalidate() {
	v.dirty = true
}

func (v *logView) pageUp() {
	v.render()
	if v.atTop || v.first < 0 {
		return
	}
	// The old top line moves to the bottom edge, so one line stays in view.
	anchor := v.first
	if anchor == v.last {
		anchor-- // a single line fills the screen; skip past it
	}
	v.follow, v.anchorTop, v.anchor = false, false, max(anchor, 0)
	v.dirty = true
}

func (v *logView) pageDown() {
	v.render()
	if v.follow || v.first < 0 {
		return
	}
	// The old bottom line moves to the top edge.
	anchor := v.last
	if anchor == v.first {
		anchor++
	}
	v.anchorTop, v.anchor = true, min(anchor, len(v.lines)-1)
	v.dirty = true
}

func (v *logView) scrollTop() {
	v.follow, v.anchorTop, v.anchor = false, true, 0
	v.dirty = true
}

func (v *logView) scrollBottom() {
	v.follow = true
	v.dirty = true
}

// toggleFollow switches follow mode; turning it off keeps the current view.
func (v *logView) toggleFollow() {
	if !v.follow {
		v.scrollBottom()
		return
	}
	v.render()
	v.follow, v.anchorTop, v.anchor = false, false, max(v.last, 0)
	v.dirty = true
}

// following reports whether new lines scroll into view.
func (v *logView) following() bool {
	v.render()
	return v.follow
}

// below returns how many lines are after the last visible one.
func (v *logView) below() int {
	v.render()
	if v.last < 0 {
		return 0
	}
	return len(v.lines) - 1 - v.last
}

// view returns exactly height rows.
func (v *logView) view() []string {
	v.render()
	return v.rows
}

func (v *logView) render() {
	if !v.dirty {
		return
	}
	v.dirty = false
	v.rows = v.rows[:0]
	v.first, v.last, v.atTop = -1, -1, true
	if len(v.lines) > 0 && v.height > 0 {
		if !v.follow && v.anchorTop && !v.renderFromTop(min(v.anchor, len(v.lines)-1)) {
			// Everything after the anchor fits: show the newest lines instead.
			v.follow = true
		}
		if v.follow || !v.anchorTop {
			bottom := len(v.lines) - 1
			if !v.follow {
				bottom = min(v.anchor, bottom)
			}
			v.renderFromBottom(bottom)
		}
	}
	for len(v.rows) < v.height {
		v.rows = append(v.rows, "")
	}
}

// renderFromTop fills the viewport with lines starting at top. It reports
// false, leaving rows empty, if the lines run out before the viewport is full.
func (v *logView) renderFromTop(top int) bool {
	for i := top; i < len(v.lines); i++ {
		v.rows = append(v.rows, v.format(v.lines[i], v.width)...)
		if len(v.rows) >= v.height {
			v.rows = v.rows[:v.height]
			v.first, v.last, v.atTop = top, i, top == 0
			return true
		}
	}
	v.rows = v.rows[:0]
	return false
}

// renderFromBottom fills the viewport with lines ending at bottom. If the
// lines before it don't fill the viewport, the first line goes at the top and
// lines after bottom fill the rest.
func (v *logView) renderFromBottom(bottom int) {
	var chunks [][]string // lines bottom, bottom-1, …
	n := 0
	i := bottom
	for ; i >= 0 && n < v.height; i-- {
		rows := v.format(v.lines[i], v.width)
		chunks = append(chunks, rows)
		n += len(rows)
	}
	v.first, v.last, v.atTop = i+1, bottom, i < 0
	for j := len(chunks) - 1; j >= 0; j-- {
		v.rows = append(v.rows, chunks[j]...)
	}
	if over := len(v.rows) - v.height; over > 0 {
		v.rows = v.rows[over:]
		v.atTop = false
		return
	}
	for i := bottom + 1; len(v.rows) < v.height && i < len(v.lines); i++ {
		v.rows = append(v.rows, v.format(v.lines[i], v.width)...)
		v.last = i
	}
	v.rows = v.rows[:min(len(v.rows), v.height)]
}
