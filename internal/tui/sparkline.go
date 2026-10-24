package tui

import (
	"math/bits"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	rateWindow   = 32 // seconds of output counted per project
	sparkCells   = 8  // cells of a sparkline, each the sum of cellDuration seconds
	cellDuration = rateWindow / sparkCells
)

// sparkBars are a sparkline's levels: no output, then 1, 2–3, 4–7, … and
// 64 or more lines per cell.
var sparkBars = []rune("▁▂▃▄▅▆▇█")

// outputRate counts a project's output lines per second over the last
// rateWindow seconds.
type outputRate struct {
	counts [rateWindow]int
	secs   [rateWindow]int64 // the second each count is for
	last   int64             // second of the newest line, 0 if none
}

func (r *outputRate) slot(sec int64) int {
	return int((sec%rateWindow + rateWindow) % rateWindow)
}

// add counts n lines in second sec (Unix time).
func (r *outputRate) add(sec int64, n int) {
	i := r.slot(sec)
	if r.secs[i] != sec {
		r.secs[i], r.counts[i] = sec, 0
	}
	r.counts[i] += n
	r.last = max(r.last, sec)
}

// count returns the lines counted in second sec.
func (r *outputRate) count(sec int64) int {
	if i := r.slot(sec); r.secs[i] == sec {
		return r.counts[i]
	}
	return 0
}

// recent reports whether there were lines in the window ending at second now.
func (r *outputRate) recent(now int64) bool {
	return r.last > 0 && now-r.last < rateWindow
}

// cells returns the lines per cell of the window ending at second now,
// oldest first.
func (r *outputRate) cells(now int64) [sparkCells]int {
	var out [sparkCells]int
	start := now - rateWindow + 1
	for s := range int64(rateWindow) {
		out[s/cellDuration] += r.count(start + s)
	}
	return out
}

// sparkLevel returns the bar for n lines: 0 for none, then one level per
// doubling, capped at the highest bar.
func sparkLevel(n int) int {
	return min(bits.Len(uint(max(n, 0))), len(sparkBars)-1)
}

// sparkline renders r's window ending at second now: bars with output in
// style, empty cells faint.
func (s *styles) sparkline(r *outputRate, now int64, style lipgloss.Style) string {
	var b strings.Builder
	var run []rune
	runFaint := false
	flush := func() {
		if len(run) > 0 {
			if runFaint {
				b.WriteString(s.faint.Render(string(run)))
			} else {
				b.WriteString(style.Render(string(run)))
			}
			run = run[:0]
		}
	}
	for _, n := range r.cells(now) {
		level := sparkLevel(n)
		if faint := level == 0; faint != runFaint {
			flush()
			runFaint = faint
		}
		run = append(run, sparkBars[level])
	}
	flush()
	return b.String()
}
