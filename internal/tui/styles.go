package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/tori/internal/theme"
)

// styles follows Vigiles' chrome (title bar, full-height frame, pinned
// footer, ▐ selection) with colors taken from the Omarchy theme.
type styles struct {
	frame        lipgloss.Style
	title        lipgloss.Style
	tab          lipgloss.Style
	tabActive    lipgloss.Style
	section      lipgloss.Style
	primary      lipgloss.Style
	secondary    lipgloss.Style
	muted        lipgloss.Style
	accent       lipgloss.Style
	success      lipgloss.Style
	warning      lipgloss.Style
	danger       lipgloss.Style
	row          lipgloss.Style
	hintKey      lipgloss.Style
	tabKey       lipgloss.Style
	faint        lipgloss.Style
	divider      lipgloss.Style
	hintLabel    lipgloss.Style
	overlay      lipgloss.Style
	overlayWarn  lipgloss.Style
	barFull      lipgloss.Style
	barEmpty     lipgloss.Style
	rule         lipgloss.Style
	ruleFocus    lipgloss.Style
	footerDivide string
	pal          theme.Palette
}

func newStyles(p theme.Palette) styles {
	s := styles{pal: p}
	s.frame = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Padding(0, 1)
	// Lowercase, like the command you type: capitals would be the loudest
	// thing on a screen whose job is to show other people's file names.
	s.title = lipgloss.NewStyle().Bold(true).Foreground(p.Brand)
	s.tab = lipgloss.NewStyle().Foreground(p.Secondary)
	// The underline is what survives a monochrome terminal or a
	// colourblind eye, where bold plus hue does not.
	s.tabActive = lipgloss.NewStyle().Foreground(p.Accent).Bold(true).Underline(true)
	s.tabKey = lipgloss.NewStyle().Foreground(p.Accent)
	s.faint = lipgloss.NewStyle().Foreground(p.Faint)
	s.divider = lipgloss.NewStyle().Foreground(p.Border)
	s.section = lipgloss.NewStyle().Foreground(p.Secondary)
	s.primary = lipgloss.NewStyle().Foreground(p.Primary)
	s.secondary = lipgloss.NewStyle().Foreground(p.Secondary)
	s.muted = lipgloss.NewStyle().Foreground(p.Muted)
	s.accent = lipgloss.NewStyle().Foreground(p.Accent)
	s.success = lipgloss.NewStyle().Foreground(p.Success)
	s.warning = lipgloss.NewStyle().Foreground(p.Warning)
	s.danger = lipgloss.NewStyle().Foreground(p.Danger)
	s.row = lipgloss.NewStyle().PaddingLeft(2)
	s.hintKey = lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	s.hintLabel = lipgloss.NewStyle().Foreground(p.Secondary)
	s.overlay = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Padding(1, 3)
	s.overlayWarn = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(p.Warning).
		Padding(1, 2)
	s.barFull = lipgloss.NewStyle().Foreground(p.Accent)
	s.barEmpty = lipgloss.NewStyle().Foreground(p.Border)
	s.rule = lipgloss.NewStyle().Foreground(p.Border)
	s.ruleFocus = lipgloss.NewStyle().Foreground(p.Accent)
	s.footerDivide = s.muted.Render(" │ ")
	return s
}

// Status dots: ● ready/cached, ◐ in progress, ○ idle or missing, ✕ failed.
func (s styles) dotReady() string    { return s.success.Render("●") }
func (s styles) dotProgress() string { return s.warning.Render("◐") }
func (s styles) dotIdle() string     { return s.faint.Render("○") }
func (s styles) dotFailed() string   { return s.danger.Render("✕") }
