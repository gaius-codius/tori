package tui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func (m Model) clipboard() func(string) error {
	if m.opt.Clipboard != nil {
		return m.opt.Clipboard
	}
	return func(string) error { return errors.New("no clipboard available") }
}

func (m Model) copy(text, done string) tea.Cmd {
	copyFn := m.clipboard()
	return func() tea.Msg {
		if err := copyFn(text); err != nil {
			return actionMsg{text: "copy failed", err: err}
		}
		return actionMsg{text: done}
	}
}

// --- API key entry ---

func (m Model) viewKeyEntry(lo layout) string {
	in := m.keyInput
	in.SetWidth(48)
	var b strings.Builder
	b.WriteString(m.st.title.Render("tori") + "\n\n")
	b.WriteString(m.st.primary.Render("Enter your TorBox API key.") + "\n")
	b.WriteString(m.st.secondary.Render("Find it at torbox.app → Settings → API.") + "\n")
	b.WriteString(m.st.secondary.Render("It is stored in your keyring, never in a file.") + "\n\n")
	b.WriteString(in.View() + "\n\n")
	switch {
	case m.checking:
		b.WriteString(m.st.warning.Render("checking key…") + "\n")
	case m.keyErr != "":
		b.WriteString(m.st.danger.Render(wrap(m.keyErr, 56)) + "\n")
	}
	b.WriteString(m.inlineHints("enter", "save", "esc", "quit"))
	return m.overlayBox(lo, m.st.overlay, b.String())
}

// --- Add link ---

func (m Model) handleAddKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.overlay = overlayNone
		m.addInput.Blur()
		return m, nil
	case "enter":
		link := strings.TrimSpace(m.addInput.Value())
		if link == "" {
			return m, nil
		}
		m.overlay = overlayNone
		m.addInput.Blur()
		return m, m.addLink(link)
	}
	var cmd tea.Cmd
	m.addInput, cmd = m.addInput.Update(msg)
	return m, cmd
}

// addLink picks the TorBox endpoint from the link's shape: magnets and bare
// info hashes are torrents, .nzb URLs are usenet, anything else is a web
// download (direct link or file hoster).
func (m Model) addLink(link string) tea.Cmd {
	api := m.api
	if api == nil {
		return nil
	}
	kind := ClassifyLink(link)
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		var err error
		switch kind {
		case "torrent":
			if !strings.HasPrefix(link, "magnet:") {
				link = "magnet:?xt=urn:btih:" + link
			}
			_, err = api.AddMagnet(ctx, link, false)
		case "usenet":
			_, err = api.AddUsenet(ctx, link, "")
		default:
			_, err = api.AddWeb(ctx, link)
		}
		if err != nil {
			return actionMsg{text: "add " + kind + " failed", err: err}
		}
		return actionMsg{text: "added " + kind + ": " + truncate(link, 50), refreshLib: true}
	}
}

// ClassifyLink says which TorBox endpoint a link belongs to.
func ClassifyLink(link string) string {
	l := strings.ToLower(link)
	switch {
	case strings.HasPrefix(l, "magnet:"):
		return "torrent"
	case isInfoHash(l):
		return "torrent"
	case strings.Contains(l, ".nzb"):
		return "usenet"
	default:
		return "web"
	}
}

func isInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (m Model) viewAdd(lo layout) string {
	in := m.addInput
	in.SetWidth(52)
	var b strings.Builder
	b.WriteString(m.st.section.Render("add to torbox") + "\n\n")
	b.WriteString(in.View() + "\n\n")
	kind := "web download"
	switch ClassifyLink(strings.TrimSpace(in.Value())) {
	case "torrent":
		kind = "torrent"
	case "usenet":
		kind = "usenet (NZB)"
	}
	if strings.TrimSpace(in.Value()) != "" {
		b.WriteString(m.st.secondary.Render("adds as: ") + m.st.primary.Render(kind) + "\n\n")
	} else {
		b.WriteString(m.st.muted.Render("magnet link, info hash, NZB URL, or a direct / hoster link") + "\n\n")
	}
	b.WriteString(m.inlineHints("enter", "add", "esc", "cancel"))
	return m.overlayBox(lo, m.st.overlay, b.String())
}

// --- Delete confirm ---

func (m Model) viewConfirm(lo layout) string {
	it := m.confirm
	body := m.st.warning.Bold(true).Render("Delete from TorBox?") + "\n\n" +
		m.st.primary.Render(wrap(it.Name, 54)) + "\n" +
		m.st.secondary.Render(it.Kind.String()+" · "+humanBytes(int64(it.Size))) + "\n\n" +
		m.st.secondary.Render("Files already downloaded to this machine are kept.") + "\n\n" +
		m.inlineHints("y", "delete", "any key", "cancel")
	return m.overlayBox(lo, m.st.overlayWarn, body)
}

// --- Help ---

// viewHelp drops the blank lines between sections when the terminal is
// short, then clips as a last resort.
func (m Model) viewHelp(lo layout) string {
	k := func(key, desc string) string {
		return "  " + m.st.hintKey.Render(padRight(key, 9)) + m.st.secondary.Render(desc)
	}
	sections := [][]string{
		{m.st.section.Render("everywhere"),
			k("1 2 3", "search · library · downloads   (tab cycles)"),
			k("alt+1-3", "same, even while typing a search"),
			k("a", "add a magnet, hash, NZB or web link"),
			k("j k", "move (or scroll the wheel)"),
			k("mouse", "click tabs and rows · double-click to open"),
			k("q", "quit (unfinished downloads resume next time)")},
		{m.st.section.Render("search"),
			k("/", "edit query (IMDb ids work: tt0137523)"),
			k("enter", "add to TorBox (on ◆: open in library)"),
			k("y", "copy magnet"),
			k("c u s", "cached only · usenet · sort")},
		{m.st.section.Render("library"),
			k("enter", "pick files · d download all · z as zip"),
			k("D R", "delete · reannounce"),
			k("/", "filter by name (any order: bunny 1080)"),
			k("f r", "all/ready/active · refresh")},
		{m.st.section.Render("downloads"),
			k("p x C", "pause/resume · cancel · clear finished")},
		{m.st.secondary.Render("● ready/cached  ◐ working  ○ idle  ◆ yours  ✕ failed")},
		{m.inlineHints("any key", "close")},
	}
	avail := lo.Height - 4 // box border and padding
	total := 0
	for _, s := range sections {
		total += len(s)
	}
	gap := total+len(sections)-1 <= avail
	var lines []string
	for i, s := range sections {
		if i > 0 && gap {
			lines = append(lines, "")
		}
		lines = append(lines, s...)
	}
	style := m.st.overlay
	if !gap {
		// Drop the vertical padding too; that frees two lines.
		style = style.Padding(0, 2)
		avail += 2
	}
	inner := min(lo.Width-4, 64) - 2 - style.GetHorizontalPadding()
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, "…")
	}
	if len(lines) > avail {
		lines = append(lines[:avail-1], m.st.muted.Render("  … taller terminal for full help"))
	}
	return m.overlayBox(lo, style, strings.Join(lines, "\n"))
}
