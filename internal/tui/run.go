package tui

import tea "charm.land/bubbletea/v2"

// Run starts the Bubble Tea program. cmd/tori must not import Charm packages.
func Run(opt Options) error {
	_, err := tea.NewProgram(New(opt)).Run()
	return err
}
