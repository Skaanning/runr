package tui

import (
	"image/color"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

// stateIcon returns the sidebar icon for a state, styled.
func (s *styles) stateIcon(state supervisor.State) string {
	switch state {
	case supervisor.Waiting, supervisor.Starting:
		return s.warn.Render("◐")
	case supervisor.Stopping:
		return s.faint.Render("◐")
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

// hint renders a key hint like "enter start".
func (s *styles) hint(key, desc string) string {
	return s.bold.Render(key) + " " + s.faint.Render(desc)
}
