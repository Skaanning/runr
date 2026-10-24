package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/Skaanning/runr/internal/config"
	"github.com/Skaanning/runr/internal/supervisor"
)

var (
	green  = lipgloss.Color("2")
	yellow = lipgloss.Color("3")
	red    = lipgloss.Color("1")

	plainStyle = lipgloss.NewStyle()
	boldStyle  = lipgloss.NewStyle().Bold(true)
	faintStyle = lipgloss.NewStyle().Faint(true)
	warnStyle  = lipgloss.NewStyle().Foreground(yellow)
	errStyle   = lipgloss.NewStyle().Foreground(red)
	ruleStyle  = lipgloss.NewStyle().Faint(true)
)

// stateIcon returns the sidebar icon for a state, styled.
func stateIcon(s supervisor.State) string {
	switch s {
	case supervisor.Waiting, supervisor.Starting:
		return warnStyle.Render("◐")
	case supervisor.Stopping:
		return faintStyle.Render("◐")
	case supervisor.Running:
		return lipgloss.NewStyle().Foreground(green).Render("●")
	case supervisor.Exited:
		return lipgloss.NewStyle().Foreground(green).Render("✓")
	case supervisor.Crashed:
		return lipgloss.NewStyle().Foreground(red).Render("✗")
	case supervisor.Stopped:
		return faintStyle.Render("■")
	case supervisor.Blocked:
		return lipgloss.NewStyle().Foreground(red).Render("⊘")
	default: // Pending
		return faintStyle.Render("○")
	}
}

// projectColor returns a project's configured color, or the next default
// color for projects without one.
func projectColor(p *config.Project, next *int) color.Color {
	c := p.Color
	if c == "" {
		c = config.DefaultColors[*next%len(config.DefaultColors)]
		*next++
	}
	return lipgloss.Color(config.ColorCode(c))
}
