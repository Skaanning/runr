package tui

import (
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"
)

// Search (/) and error jumps (e) in the running view's logs.

// handleSearchKey handles keys while the search input is open. Matches are
// highlighted as the query is typed.
func (m *runningModel) handleSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.interrupt()
	case "esc":
		m.editing = false
		m.input.Blur()
		m.setSearch(m.prevSearch)
		return nil
	case "enter":
		m.editing = false
		m.input.Blur()
		if m.search.active() {
			m.jump(-1) // the newest match up to the bottom of the screen
		}
		return nil
	}
	return m.updateSearchInput(msg)
}

// updateSearchInput passes a key or a paste to the search input, and
// searches for its new value.
func (m *runningModel) updateSearchInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if q := m.input.Value(); q != m.search.query {
		m.setSearch(newSearch(q))
	}
	return cmd
}

func (m *runningModel) setSearch(s search) {
	m.search, m.matchInfo = s, ""
	for _, v := range []*logView{m.combined, m.single} {
		if v != nil {
			v.current = 0
			v.invalidate()
		}
	}
}

// jump moves to the next (dir > 0, newer) or previous (dir < 0, older) line
// matching the search, wrapping around at either end. Without a current
// match it starts from the edge of the screen.
func (m *runningModel) jump(dir int) {
	if !m.search.active() {
		return
	}
	v := m.view()
	idx := v.matching(m.search)
	if len(idx) == 0 {
		v.current, m.matchInfo = 0, ""
		v.invalidate()
		m.notice = fmt.Sprintf("no matches for %q", m.search.query)
		return
	}
	k := m.step(v, idx, dir, "search")
	m.matchInfo = fmt.Sprintf("%d/%d", k+1, len(idx))
}

// jumpError moves to the next (dir > 0) or previous (dir < 0) error line in
// the view shown, like jump, and stops following.
func (m *runningModel) jumpError(dir int) {
	v := m.view()
	var idx []int
	for i, l := range v.lines {
		if l.Error {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		m.notice = "no errors in this view"
		return
	}
	if v.following() {
		v.toggleFollow()
	}
	k := m.step(v, idx, dir, "errors")
	m.errorSeq, m.errorInfo = v.current, fmt.Sprintf("%d/%d", k+1, len(idx))
}

// clearErrorJump forgets the error line jumped to.
func (m *runningModel) clearErrorJump() {
	for _, v := range []*logView{m.combined, m.single} {
		if v != nil && m.errorSeq != 0 && v.current == m.errorSeq {
			v.current = 0
			v.invalidate()
		}
	}
	m.errorSeq, m.errorInfo = 0, ""
}

// errorJumpShown reports whether the view shown is at the error line jumped
// to, so the help line shows its position.
func (m *runningModel) errorJumpShown() bool {
	return m.errorSeq != 0 && m.view().current == m.errorSeq
}

// step moves v to the next (dir > 0) or previous (dir < 0) of the lines
// idx (not empty), wrapping around at either end with a notice that starts
// with what, and returns its position in idx. Without a current line it
// starts from the edge of the screen.
func (m *runningModel) step(v *logView, idx []int, dir int, what string) int {
	v.invalidate()
	v.render()
	cur := -1
	if v.current != 0 {
		cur = v.indexOf(v.current)
	}
	var k int // position in idx
	switch {
	case cur < 0 && dir < 0:
		k = sort.SearchInts(idx, v.last+1) - 1
	case cur < 0:
		k = sort.SearchInts(idx, v.first)
	case dir < 0:
		k = sort.SearchInts(idx, cur) - 1
	default:
		k = sort.SearchInts(idx, cur+1)
	}
	switch {
	case k < 0:
		k = len(idx) - 1
		if cur >= 0 {
			m.notice = what + " wrapped to the bottom"
		}
	case k >= len(idx):
		k = 0
		if cur >= 0 {
			m.notice = what + " wrapped to the top"
		}
	}
	v.current = v.lines[idx[k]].Seq
	v.invalidate()
	v.reveal(idx[k])
	return k
}

// hasErrors reports whether the processes in the view shown counted error
// lines in their latest runs.
func (m *runningModel) hasErrors() bool {
	if m.single != nil {
		e := m.byName[m.singleFor]
		return e != nil && e.info.Errors > 0
	}
	for _, e := range m.entries {
		if e.info.Errors > 0 {
			return true
		}
	}
	return false
}
