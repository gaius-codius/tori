package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/tori/internal/torbox"
)

type libraryState struct {
	items    []torbox.Item
	cursor   int
	loaded   bool
	err      string
	filter   string // "", "ready", "active"
	syncedAt time.Time
	failedAt time.Time
}

type libraryMsg struct {
	items []torbox.Item
	err   error
}

// fetchLibrary loads all three kinds in parallel. A kind the plan does not
// allow (usenet on lower plans) fails quietly instead of hiding the rest.
func (m Model) fetchLibrary() tea.Cmd {
	api := m.api
	if api == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		var (
			mu    sync.Mutex
			wg    sync.WaitGroup
			all   []torbox.Item
			first error
			fails int
		)
		for _, k := range []torbox.Kind{torbox.KindTorrent, torbox.KindWeb, torbox.KindUsenet} {
			wg.Add(1)
			go func(k torbox.Kind) {
				defer wg.Done()
				items, err := api.List(ctx, k)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					fails++
					if first == nil || torbox.IsAuth(err) {
						first = err
					}
					return
				}
				all = append(all, items...)
			}(k)
		}
		wg.Wait()
		if fails == 3 || torbox.IsAuth(first) {
			return libraryMsg{err: first}
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
		return libraryMsg{items: all}
	}
}

func (m Model) handleLibrary(msg libraryMsg) (tea.Model, tea.Cmd) {
	next := m.libTick()
	if msg.err != nil {
		m.lib.err = msg.err.Error()
		m.lib.failedAt = time.Now()
		if torbox.IsAuth(msg.err) {
			mm, cmd := m.promptKey("TorBox rejected the API key")
			return mm, tea.Batch(cmd, next)
		}
		return m, next
	}
	// Keep the cursor on the same item across refreshes.
	var selID int64 = -1
	var selKind torbox.Kind
	if it, ok := m.selectedItem(); ok {
		selID, selKind = it.ID, it.Kind
	}
	m.lib.items = msg.items
	m.lib.loaded = true
	m.lib.err = ""
	m.lib.syncedAt = time.Now()
	m.markOwned()
	vis := m.visibleItems()
	m.lib.cursor = clamp(m.lib.cursor, len(vis))
	for i, it := range vis {
		if it.ID == selID && it.Kind == selKind {
			m.lib.cursor = i
		}
	}
	// Refresh an open file picker so it sees new files.
	if m.overlay == overlayFiles {
		for _, it := range msg.items {
			if it.ID == m.files.item.ID && it.Kind == m.files.item.Kind {
				m.files.item = it
			}
		}
	}
	return m, next
}

// markOwned flags search results that are already in the library.
func (m *Model) markOwned() {
	have := make(map[string]bool, len(m.lib.items))
	for _, it := range m.lib.items {
		have[strings.ToLower(it.Hash)] = true
	}
	for i := range m.search.results {
		if have[strings.ToLower(m.search.results[i].Hash)] {
			m.search.results[i].Owned = true
		}
	}
}

func (m Model) visibleItems() []torbox.Item {
	if m.lib.filter == "" {
		return m.lib.items
	}
	var out []torbox.Item
	for _, it := range m.lib.items {
		switch m.lib.filter {
		case "ready":
			if it.Ready() {
				out = append(out, it)
			}
		case "active":
			if !it.Ready() {
				out = append(out, it)
			}
		}
	}
	return out
}

func (m Model) selectedItem() (torbox.Item, bool) {
	vis := m.visibleItems()
	if m.lib.cursor < 0 || m.lib.cursor >= len(vis) {
		return torbox.Item{}, false
	}
	return vis[m.lib.cursor], true
}

func (m Model) handleLibraryKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.visibleItems())
	switch key {
	case "j", "down":
		m.lib.cursor = clamp(m.lib.cursor+1, n)
		return m, nil
	case "k", "up":
		m.lib.cursor = clamp(m.lib.cursor-1, n)
		return m, nil
	case "g", "home":
		m.lib.cursor = 0
		return m, nil
	case "G", "end":
		m.lib.cursor = clamp(n-1, n)
		return m, nil
	case "f":
		switch m.lib.filter {
		case "":
			m.lib.filter = "ready"
		case "ready":
			m.lib.filter = "active"
		default:
			m.lib.filter = ""
		}
		m.lib.cursor = 0
		return m, nil
	case "r":
		return m, tea.Batch(m.fetchLibrary(), m.setStatus("refreshing", false))
	}
	it, ok := m.selectedItem()
	if !ok {
		return m, nil
	}
	switch key {
	case "enter", "l", "right":
		m.files = filesState{item: it, picked: map[int64]bool{}}
		m.overlay = overlayFiles
		return m, nil
	case "d":
		if !it.Ready() {
			return m, m.setStatus("not ready on TorBox yet", true)
		}
		return m, m.queueFiles(it, it.Files)
	case "z":
		if !it.Ready() {
			return m, m.setStatus("not ready on TorBox yet", true)
		}
		return m, m.queueZip(it)
	case "D":
		m.confirm = it
		m.overlay = overlayConfirm
		return m, nil
	case "R":
		if it.Kind != torbox.KindTorrent {
			return m, m.setStatus("reannounce is for torrents only", true)
		}
		api := m.api
		return m, func() tea.Msg {
			ctx, cancel := ctxTimeout()
			defer cancel()
			err := api.Reannounce(ctx, it)
			return actionMsg{text: "reannounced " + truncate(it.Name, 50), err: err, refreshLib: true}
		}
	}
	return m, nil
}

func (m Model) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "Y", "D":
		it := m.confirm
		m.overlay = overlayNone
		api := m.api
		return m, func() tea.Msg {
			ctx, cancel := ctxTimeout()
			defer cancel()
			err := api.Delete(ctx, it)
			if err != nil {
				return actionMsg{text: "delete failed", err: err}
			}
			return actionMsg{text: "deleted " + truncate(it.Name, 50), refreshLib: true}
		}
	default:
		m.overlay = overlayNone
		return m, nil
	}
}

// queueFiles asks TorBox for a link per file and hands each to aria2c, into
// a folder named after the item when it has more than one file.
func (m Model) queueFiles(it torbox.Item, files []torbox.File) tea.Cmd {
	if m.opt.Downloader == nil {
		return m.setStatus("downloads disabled: aria2c is not available", true)
	}
	if len(files) == 0 {
		return m.setStatus("no files listed for this item yet", true)
	}
	api, dl := m.api, m.opt.Downloader
	subdir := ""
	if len(it.Files) > 1 {
		subdir = safeName(it.Name)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		queued := 0
		for _, f := range files {
			link, err := api.DownloadURL(ctx, it, f.ID)
			if err != nil {
				return actionMsg{text: fmt.Sprintf("queued %d of %d; link failed", queued, len(files)), err: err, refreshDL: queued > 0}
			}
			if _, err := dl.Add(ctx, link, safeName(filepath.Base(f.DisplayName())), subdir); err != nil {
				return actionMsg{text: fmt.Sprintf("queued %d of %d; aria2 failed", queued, len(files)), err: err, refreshDL: queued > 0}
			}
			queued++
		}
		return actionMsg{text: fmt.Sprintf("queued %d file(s) from %s", queued, truncate(it.Name, 40)), refreshDL: true}
	}
}

func (m Model) queueZip(it torbox.Item) tea.Cmd {
	if m.opt.Downloader == nil {
		return m.setStatus("downloads disabled: aria2c is not available", true)
	}
	api, dl := m.api, m.opt.Downloader
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		link, err := api.DownloadURL(ctx, it, -1)
		if err != nil {
			return actionMsg{text: "zip link failed", err: err}
		}
		if _, err := dl.Add(ctx, link, safeName(it.Name)+".zip", ""); err != nil {
			return actionMsg{text: "aria2 failed", err: err}
		}
		return actionMsg{text: "queued zip of " + truncate(it.Name, 50), refreshDL: true}
	}
}

// safeName keeps a TorBox-supplied name from escaping the download folder.
func safeName(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "/", "_"))
	s = strings.ReplaceAll(s, "\x00", "")
	if s == "" || s == "." || s == ".." {
		return "download"
	}
	return s
}

func (m Model) itemDot(it torbox.Item) string {
	st := lower(it.State)
	switch {
	case it.Ready():
		return m.st.dotReady()
	case it.Error != "" || strings.Contains(st, "error") || strings.Contains(st, "failed"):
		return m.st.dotFailed()
	case strings.Contains(st, "stalled") || st == "paused":
		return m.st.dotIdle()
	default:
		return m.st.dotProgress()
	}
}

func (m Model) itemState(it torbox.Item) string {
	switch {
	case it.Ready():
		return "ready"
	case it.Error != "":
		return it.Error
	}
	st := it.State
	if st == "" {
		st = "queued"
	}
	if it.Percent() > 0 {
		st = fmt.Sprintf("%s %.0f%%", st, it.Percent())
	}
	if sp := humanSpeed(int64(it.Speed)); sp != "" {
		st += " " + sp
	}
	return st
}

var kindTag = map[torbox.Kind]string{torbox.KindTorrent: "tor", torbox.KindWeb: "web", torbox.KindUsenet: "nzb"}

// libCols sizes the library columns; ADDED drops out on narrow terminals.
func (m Model) libCols(lo layout) (name, state int, showAdded bool) {
	state = 26
	if lo.Compact {
		state = 16
	}
	showAdded = !lo.Compact
	name = lo.ContentWidth - 2 - 2 - 4 - 10 - 2 - state
	if showAdded {
		name -= 10
	}
	return name, state, showAdded
}

// libChrome is the library tab's footer and detail strip.
func (m Model) libChrome(lo layout) (footer, detail string) {
	it, ok := m.selectedItem()
	ready := ok && it.Ready()
	footer = m.footer(lo,
		m.hints(hIf(ok, "enter", "files"), hIf(ready, "d", "download"), hIf(ready, "z", "zip")),
		m.hints(hIf(ok, "D", "delete"), hIf(ok && it.Kind == torbox.KindTorrent && !ready, "R", "reannounce"), h("f", "filter")),
		m.hints(h("a", "add link"), h("?", "help")),
	)
	if ok && lo.Height >= 20 {
		detail = m.itemDetail(lo, it)
	}
	return footer, detail
}

// libRows is where library rows are drawn.
func (m Model) libRows(lo layout) (top, start, end int) {
	footer, detail := m.libChrome(lo)
	start, end = window(len(m.visibleItems()), m.lib.cursor, m.bodyHeight(lo, detail, footer)-2)
	return bodyTop + 2, start, end
}

func (m Model) viewLibrary(lo layout) string {
	footer, detail := m.libChrome(lo)
	vis := m.visibleItems()
	var b strings.Builder
	label := "all"
	if m.lib.filter != "" {
		label = m.lib.filter
	}
	b.WriteString(m.st.section.Render(fmt.Sprintf("library · %s · %d", label, len(vis))) + "\n")
	nameW, stateW, showAdded := m.libCols(lo)
	switch {
	case m.lib.err != "" && !m.lib.loaded:
		b.WriteString(m.st.danger.Render(wrap("  "+m.lib.err, lo.ContentWidth)))
	case !m.lib.loaded:
		b.WriteString(m.st.secondary.Render("  loading…"))
	case len(vis) == 0 && m.lib.filter != "":
		b.WriteString(m.st.secondary.Render("  nothing " + m.lib.filter + ": press f to change the filter"))
	case len(vis) == 0:
		b.WriteString(m.st.secondary.Render("  nothing here yet: search with 1, or add a link with a"))
	default:
		hd := "  " + padRight("", 4) + padRight("NAME", nameW) + padLeft("SIZE", 10)
		if showAdded {
			hd += padLeft("ADDED", 10)
		}
		hd += "  " + padRight("STATE", stateW)
		b.WriteString("  " + m.st.section.Render(hd) + "\n")
		_, start, end := m.libRows(lo)
		for i := start; i < end; i++ {
			x := vis[i]
			line := m.itemDot(x) + " " +
				m.st.faint.Render(padRight(kindTag[x.Kind], 4)) +
				m.st.primary.Render(padRight(x.Name, nameW)) +
				m.st.secondary.Render(padLeft(humanBytes(int64(x.Size)), 10))
			if showAdded {
				added := ""
				if t, ok := parseTime(x.CreatedAt); ok {
					added = ago(t)
				}
				line += m.st.secondary.Render(padLeft(added, 10))
			}
			stateStyle := m.st.secondary
			if x.Ready() {
				stateStyle = m.st.success
			}
			line += "  " + stateStyle.Render(padRight(m.itemState(x), stateW))
			b.WriteString(m.row(lo, line, i == m.lib.cursor) + "\n")
		}
	}
	return m.page(lo, strings.TrimRight(b.String(), "\n"), detail, footer)
}

// itemDetail shows the selected item's full name and facts.
func (m Model) itemDetail(lo layout, it torbox.Item) string {
	var meta []string
	meta = append(meta, it.Kind.String())
	if n := len(it.Files); n > 0 {
		meta = append(meta, fmt.Sprintf("%d file%s", n, plural(n)))
	}
	meta = append(meta, humanBytes(int64(it.Size)))
	if t, ok := parseTime(it.CreatedAt); ok {
		meta = append(meta, "added "+ago(t))
	}
	if it.Kind == torbox.KindTorrent && !it.Ready() {
		meta = append(meta, fmt.Sprintf("%d seeds · %d peers", it.Seeds, it.Peers))
	}
	if it.ExpiresAt != nil {
		if t, ok := parseTime(*it.ExpiresAt); ok {
			meta = append(meta, "expires "+until(t))
		}
	}
	return m.st.primary.Render(wrapLines(it.Name, lo.ContentWidth, 2)) + "\n" +
		m.st.secondary.Render(strings.Join(meta, " · "))
}
