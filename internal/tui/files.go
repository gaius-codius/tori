package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/tori/internal/torbox"
)

type filesState struct {
	item   torbox.Item
	cursor int
	picked map[int64]bool
}

func (m Model) handleFilesKey(key string) (tea.Model, tea.Cmd) {
	fs := m.files.item.Files
	n := len(fs)
	switch key {
	case "esc", "h", "left", "q":
		m.overlay = overlayNone
		return m, nil
	case "j", "down":
		m.files.cursor = clamp(m.files.cursor+1, n)
	case "k", "up":
		m.files.cursor = clamp(m.files.cursor-1, n)
	case "g", "home":
		m.files.cursor = 0
	case "G", "end":
		m.files.cursor = clamp(n-1, n)
	case "space", " ":
		if n > 0 {
			id := fs[m.files.cursor].ID
			m.files.picked[id] = !m.files.picked[id]
			m.files.cursor = clamp(m.files.cursor+1, n)
		}
	case "A":
		all := len(m.files.picked) < n || hasFalse(m.files.picked)
		for _, f := range fs {
			m.files.picked[f.ID] = all
		}
	case "enter", "d":
		if n == 0 {
			return m, nil
		}
		if !m.files.item.Ready() {
			return m, m.setStatus("not ready on TorBox yet", true)
		}
		var sel []torbox.File
		for _, f := range fs {
			if m.files.picked[f.ID] {
				sel = append(sel, f)
			}
		}
		if len(sel) == 0 {
			sel = []torbox.File{fs[m.files.cursor]}
		}
		m.overlay = overlayNone
		return m, m.queueFiles(m.files.item, sel)
	case "y":
		if n == 0 || !m.files.item.Ready() {
			return m, nil
		}
		it, f := m.files.item, fs[m.files.cursor]
		api := m.api
		copyFn := m.clipboard()
		return m, func() tea.Msg {
			ctx, cancel := ctxTimeout()
			defer cancel()
			link, err := api.DownloadURL(ctx, it, f.ID)
			if err != nil {
				return actionMsg{text: "link failed", err: err}
			}
			if err := copyFn(link); err != nil {
				return actionMsg{text: "copy failed", err: err}
			}
			return actionMsg{text: "link copied (valid for about an hour)"}
		}
	}
	return m, nil
}

func hasFalse(p map[int64]bool) bool {
	for _, v := range p {
		if !v {
			return true
		}
	}
	return false
}

// filesFooter is the file picker's footer.
func (m Model) filesFooter(lo layout) string {
	it := m.files.item
	n := len(it.Files) > 0
	ready := it.Ready()
	picked := 0
	for _, f := range it.Files {
		if m.files.picked[f.ID] {
			picked++
		}
	}
	dlLabel := "download this"
	if picked > 0 {
		dlLabel = fmt.Sprintf("download %d picked", picked)
	}
	return m.footer(lo,
		m.hints(hIf(n, "space", "pick"), hIf(n, "A", "pick all"), hIf(n && ready, "enter", dlLabel)),
		m.hints(hIf(n && ready, "y", "copy link"), h("esc", "back")),
	)
}

// filesRows is where file rows are drawn, below the name, meta and a gap.
func (m Model) filesRows(lo layout) (top, start, end int) {
	nameLines := lipgloss.Height(wrapLines(m.files.item.Name, lo.ContentWidth, 2))
	start, end = window(len(m.files.item.Files), m.files.cursor, m.bodyHeight(lo, "", m.filesFooter(lo))-4)
	return bodyTop + nameLines + 2, start, end
}

func (m Model) viewFiles(lo layout) string {
	it := m.files.item
	n := len(it.Files) > 0
	ready := it.Ready()
	picked := 0
	var pickedSize int64
	for _, f := range it.Files {
		if m.files.picked[f.ID] {
			picked++
			pickedSize += int64(f.Size)
		}
	}
	footer := m.filesFooter(lo)
	var b strings.Builder
	b.WriteString(m.st.primary.Bold(true).Render(wrapLines(it.Name, lo.ContentWidth, 2)) + "\n")
	meta := fmt.Sprintf("%s · %d file%s · %s", it.Kind, len(it.Files), plural(len(it.Files)), humanBytes(int64(it.Size)))
	if picked > 0 {
		meta += fmt.Sprintf(" · %d picked (%s)", picked, humanBytes(pickedSize))
	}
	if !ready {
		meta += " · " + m.itemState(it)
	}
	b.WriteString(m.st.secondary.Render(meta) + "\n\n")
	if !n {
		b.WriteString(m.st.secondary.Render("  TorBox has not listed files for this item yet"))
	}
	nameW := lo.ContentWidth - 2 - 4 - 10
	_, start, end := m.filesRows(lo)
	for i := start; i < end; i++ {
		f := it.Files[i]
		box := m.st.faint.Render("[ ] ")
		if m.files.picked[f.ID] {
			box = m.st.success.Render("[x] ")
		}
		line := box + m.st.primary.Render(padRight(f.DisplayName(), nameW)) +
			m.st.secondary.Render(padLeft(humanBytes(int64(f.Size)), 10))
		b.WriteString(m.row(lo, line, i == m.files.cursor) + "\n")
	}
	return m.page(lo, strings.TrimRight(b.String(), "\n"), "", footer)
}
