package tui

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// spinnerFrames are the frames of the spinner shown while a process starts,
// waits or stops.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner returns the spinner's frame at a time, so all spinners on screen
// move in step.
func spinner(now time.Time) string {
	return spinnerFrames[int(now.UnixMilli()/animateEvery.Milliseconds())%len(spinnerFrames)]
}

// stateIcon returns the sidebar icon for a state, styled; spin is the
// spinner's current frame, for states in progress.
func (s *styles) stateIcon(state supervisor.State, spin string) string {
	switch state {
	case supervisor.Waiting, supervisor.Starting:
		return s.warn.Render(spin)
	case supervisor.Stopping:
		return s.faint.Render(spin)
	case supervisor.Running:
		return s.ok.Render("●")
	case supervisor.Exited:
		return s.ok.Render("✓")
	case supervisor.Crashed:
		return s.err.Render("✗")
	case supervisor.Stopped:
		return s.faint.Render("■")
	case supervisor.Blocked:
		return s.err.Render("⊘")
	default: // Pending
		return s.faint.Render("○")
	}
}

// projectColor returns a project's configured color, or the next default
// color for projects without one.
func (s *styles) projectColor(p *config.Project, next *int) color.Color {
	c := p.Color
	if c == "" {
		c = config.DefaultColors[*next%len(config.DefaultColors)]
		*next++
	}
	return s.color(c)
}

// hint renders a key hint like "enter start", with the key as a keycap
// when the terminal has the colors for it.
func (s *styles) hint(key, desc string) string {
	if s.keycaps {
		return s.keycap.Render(" "+key+" ") + " " + s.faint.Render(desc)
	}
	return s.bold.Render(key) + " " + s.faint.Render(desc)
}

// hintList renders key hints like "enter start  esc cancel".
func (s *styles) hintList(keys [][2]string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = s.hint(k[0], k[1])
	}
	return strings.Join(parts, s.hintGap)
}

// fitHints joins help line parts. When narrow, it drops hints from the end
// of the middle so the last one (quitting) stays visible.
func (s *styles) fitHints(parts []string, width int) string {
	line := " " + strings.Join(parts, s.hintGap)
	for len(parts) > 2 && ansi.StringWidth(line) > width {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1])
		line = " " + strings.Join(parts, s.hintGap)
	}
	return ansi.Truncate(line, width, "…")
}

// stateCounts renders the counts of running (or done), starting or
// stopping, and failed processes, leaving out zeros: "● 2  ⠋ 1  ✗ 1" with
// sep "  " and the spinner frame spin.
func (s *styles) stateCounts(states []supervisor.State, sep, spin string) string {
	var ok, busy, failed int
	for _, st := range states {
		switch st {
		case supervisor.Running, supervisor.Exited:
			ok++
		case supervisor.Pending, supervisor.Waiting, supervisor.Starting, supervisor.Stopping:
			busy++
		case supervisor.Crashed, supervisor.Blocked:
			failed++
		}
	}
	var parts []string
	for _, c := range []struct {
		n     int
		icon  string
		style func(...string) string
	}{{ok, "●", s.ok.Render}, {busy, spin, s.warn.Render}, {failed, "✗", s.err.Render}} {
		if c.n > 0 {
			parts = append(parts, c.style(c.icon+" "+strconv.Itoa(c.n)))
		}
	}
	return strings.Join(parts, sep)
}
