package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// doubleClick is how close two clicks on the same row must be to act on it.
const doubleClick = 400 * time.Millisecond

// lastClick remembers the previous click, to detect double-clicks.
type lastClick struct {
	target string // "search:3", "lib:0", "files:1"
	at     time.Time
}

// isDouble records a click on target and reports whether it completes a
// double-click. A double-click consumes the pair, so a third click starts over.
func (m *Model) isDouble(target string) bool {
	now := time.Now()
	if m.clicked.target == target && now.Sub(m.clicked.at) <= doubleClick {
		m.clicked = lastClick{}
		return true
	}
	m.clicked = lastClick{target: target, at: now}
	return false
}

// Mouse support: click a tab to switch, click a row to select it,
// double-click a row to act on it (like enter), and scroll lists with the
// wheel. Row positions come from the same helpers the views draw with.

// contentX is the screen column where frame content starts (border + pad).
const contentX = 2

func (m Model) handleMouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.width < minWidth || m.height < minHeight {
		return m, nil
	}
	lo := newLayout(m.width, m.height)
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.wheel(-1)
		case tea.MouseWheelDown:
			return m.wheel(1)
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m.click(lo, msg.X, msg.Y)
		}
	}
	return m, nil
}

// wheel moves the selection in whatever list is on screen.
func (m Model) wheel(delta int) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayFiles:
		m.files.cursor = clamp(m.files.cursor+delta, len(m.files.item.Files))
		return m, nil
	case overlayNone:
	default:
		return m, nil
	}
	switch m.tab {
	case viewSearch:
		m.search.input.Blur()
		m.search.cursor = clamp(m.search.cursor+delta, len(m.search.results))
	case viewLibrary:
		m.lib.input.Blur()
		m.lib.cursor = clamp(m.lib.cursor+delta, len(m.visibleItems()))
	case viewDownloads:
		m.dl.cursor = clamp(m.dl.cursor+delta, len(m.dl.jobs))
	}
	return m, nil
}

func (m Model) click(lo layout, x, y int) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case overlayHelp:
		m.overlay = m.prevOv
		return m, nil
	case overlayFiles:
		top, start, end := m.filesRows(lo)
		if i, ok := rowAt(y, top, start, end); ok {
			m.files.cursor = i
			if m.isDouble(fmt.Sprintf("files:%d", i)) {
				return m.handleFilesKey("space")
			}
		}
		return m, nil
	case overlayNone:
	default:
		// Key entry, add link and confirm stay keyboard-only.
		return m, nil
	}

	if y == 0 {
		for _, t := range m.tabItems() {
			if x >= t.x0 && x < t.x1 {
				return m.switchTab(t.v)
			}
		}
		return m, nil
	}

	switch m.tab {
	case viewSearch:
		return m.clickSearch(lo, x, y)
	case viewLibrary:
		if y == bodyTop { // the headline doubles as the filter box
			m.lib.input.SetValue(m.lib.query)
			return m, m.lib.input.Focus()
		}
		top, start, end := m.libRows(lo)
		if i, ok := rowAt(y, top, start, end); ok {
			m.lib.cursor = i
			if m.isDouble(fmt.Sprintf("lib:%d", i)) {
				return m.handleLibraryKey("enter")
			}
		}
	case viewDownloads:
		top, lines := m.dlLines(lo)
		if k := y - top; k >= 0 && k < len(lines) && lines[k].job >= 0 {
			m.dl.cursor = lines[k].job
		}
	}
	return m, nil
}

func (m Model) clickSearch(lo layout, x, y int) (tea.Model, tea.Cmd) {
	switch y {
	case bodyTop: // the search box
		return m, m.search.input.Focus()
	case bodyTop + 1: // the filter toggles
		cx := x - contentX
		cachedEnd := 2 + lipgloss.Width(m.toggle("c", "cached only", m.search.cachedOnly))
		usenetStart := cachedEnd + 3
		usenetEnd := usenetStart + lipgloss.Width(m.toggle("u", "usenet", m.search.usenet))
		switch {
		case cx >= 2 && cx < cachedEnd:
			m.search.input.Blur()
			return m.handleSearchKey("c")
		case cx >= usenetStart && cx < usenetEnd:
			m.search.input.Blur()
			return m.handleSearchKey("u")
		}
		return m, nil
	}
	if len(m.search.results) == 0 || m.search.loading || m.search.err != "" {
		return m, nil
	}
	top, start, end := m.searchRows(lo)
	i, ok := rowAt(y, top, start, end)
	if !ok {
		return m, nil
	}
	m.search.input.Blur()
	m.search.cursor = i
	if m.isDouble(fmt.Sprintf("search:%d", i)) {
		return m.handleSearchKey("enter")
	}
	return m, nil
}

// rowAt maps screen row y to a list index for rows drawn one per line from
// top, showing indexes [start, end).
func rowAt(y, top, start, end int) (int, bool) {
	i := start + (y - top)
	if y < top || i >= end {
		return 0, false
	}
	return i, true
}
