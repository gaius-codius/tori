package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gaius-codius/tori/internal/torbox"
)

type sortMode int

const (
	sortSeeders sortMode = iota
	sortSize
	sortName
)

func (s sortMode) String() string {
	return [...]string{"seeders", "size", "name"}[s]
}

type searchState struct {
	input      textinput.Model
	query      string
	usenet     bool
	cachedOnly bool
	sort       sortMode
	results    []torbox.Result
	cursor     int
	loading    bool
	err        string
	seq        int
}

func newSearchState(in textinput.Model, cachedOnly bool) searchState {
	return searchState{input: in, cachedOnly: cachedOnly}
}

type searchDoneMsg struct {
	seq     int
	results []torbox.Result
	err     error
}

func (m Model) runSearch() (tea.Model, tea.Cmd) {
	q := strings.TrimSpace(m.search.input.Value())
	if q == "" || m.api == nil {
		return m, nil
	}
	m.search.query = q
	m.search.loading = true
	m.search.err = ""
	m.search.seq++
	seq := m.search.seq
	api := m.api
	opt := torbox.SearchOptions{Usenet: m.search.usenet, CachedOnly: m.search.cachedOnly}
	m.search.input.Blur()
	return m, func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		res, err := api.Search(ctx, q, opt)
		return searchDoneMsg{seq: seq, results: res, err: err}
	}
}

func (m Model) handleSearchDone(msg searchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.search.seq {
		return m, nil // superseded
	}
	m.search.loading = false
	if msg.err != nil {
		m.search.err = msg.err.Error()
		m.search.results = nil
		if torbox.IsAuth(msg.err) {
			return m.promptKey("TorBox rejected the API key")
		}
		return m, nil
	}
	m.search.results = msg.results
	m.markOwned()
	m.sortResults()
	m.search.cursor = 0
	return m, nil
}

func (m *Model) sortResults() {
	r := m.search.results
	switch m.search.sort {
	case sortSize:
		sort.SliceStable(r, func(i, j int) bool { return r[i].Size > r[j].Size })
	case sortName:
		sort.SliceStable(r, func(i, j int) bool { return strings.ToLower(r[i].Name()) < strings.ToLower(r[j].Name()) })
	default:
		// Cached first, then by seeders: cached items are instant anyway.
		sort.SliceStable(r, func(i, j int) bool {
			if r[i].Cached != r[j].Cached {
				return r[i].Cached
			}
			return r[i].Seeders > r[j].Seeders
		})
	}
}

func (m Model) handleSearchInputKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		return m.runSearch()
	case "down":
		// down means "go to the list". With no list, leaving the box would
		// take the only visible cursor off screen, so hold the keystroke.
		if len(m.search.results) == 0 {
			return m, nil
		}
		m.search.input.Blur()
		return m, nil
	case "esc":
		m.search.input.Blur()
		return m, nil
	case "tab":
		return m.switchTab(viewLibrary)
	case "shift+tab":
		return m.switchTab(viewDownloads)
	}
	var cmd tea.Cmd
	m.search.input, cmd = m.search.input.Update(msg)
	return m, cmd
}

func (m Model) handleSearchKey(key string) (tea.Model, tea.Cmd) {
	n := len(m.search.results)
	switch key {
	case "/", "i":
		return m, m.search.input.Focus()
	case "j", "down":
		m.search.cursor = clamp(m.search.cursor+1, n)
	case "k", "up":
		if m.search.cursor == 0 {
			return m, m.search.input.Focus()
		}
		m.search.cursor--
	case "g", "home":
		m.search.cursor = 0
	case "G", "end":
		m.search.cursor = clamp(n-1, n)
	case "c":
		m.search.cachedOnly = !m.search.cachedOnly
		return m.rerun()
	case "u":
		m.search.usenet = !m.search.usenet
		return m.rerun()
	case "s":
		m.search.sort = (m.search.sort + 1) % 3
		m.sortResults()
		m.search.cursor = 0
	case "enter":
		if n == 0 {
			return m, m.search.input.Focus()
		}
		r := m.search.results[m.search.cursor]
		if r.Owned {
			return m.openInLibrary(r)
		}
		return m, m.addResult(r)
	case "y":
		if n == 0 {
			return m, nil
		}
		r := m.search.results[m.search.cursor]
		link := r.Magnet
		if m.search.usenet {
			return m, m.setStatus("NZB links from TorBox search only work inside TorBox", true)
		}
		if link == "" {
			return m, m.setStatus("no magnet for this result", true)
		}
		return m, m.copy(link, "magnet copied")
	}
	return m, nil
}

func (m Model) rerun() (tea.Model, tea.Cmd) {
	if m.search.query == "" {
		return m, nil
	}
	return m.runSearch()
}

// addedMsg reports a search result added to TorBox.
type addedMsg struct {
	hash, name string
	err        error
}

func (m Model) addResult(r torbox.Result) tea.Cmd {
	api := m.api
	name := r.Name()
	return func() tea.Msg {
		ctx, cancel := ctxTimeout()
		defer cancel()
		var (
			res torbox.AddResult
			err error
		)
		switch {
		case r.Type == "usenet" || r.NZB != "":
			res, err = api.AddUsenet(ctx, r.NZB, name)
		case r.Magnet != "":
			res, err = api.AddMagnet(ctx, r.Magnet, false)
		case r.Hash != "":
			res, err = api.AddMagnet(ctx, "magnet:?xt=urn:btih:"+r.Hash, false)
		default:
			err = fmt.Errorf("no magnet or NZB")
		}
		hash := r.Hash
		if res.Hash != "" {
			hash = res.Hash
		}
		return addedMsg{hash: hash, name: name, err: err}
	}
}

// handleAdded marks the result as yours so the list shows it at once.
func (m Model) handleAdded(msg addedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m2, cmd := m.Update(actionMsg{text: "add failed", err: msg.err})
		return m2, cmd
	}
	for i := range m.search.results {
		if strings.EqualFold(m.search.results[i].Hash, msg.hash) {
			m.search.results[i].Owned = true
		}
	}
	return m, tea.Batch(m.setStatus("added "+truncate(msg.name, 60)+" · enter again to open it in the library", false), m.fetchLibrary())
}

// openInLibrary jumps to the library row for a result already added.
func (m Model) openInLibrary(r torbox.Result) (tea.Model, tea.Cmd) {
	m.tab = viewLibrary
	m.lib.filter = ""
	m.clearLibQuery() // or the row we are jumping to may be filtered out
	for i, it := range m.lib.items {
		if strings.EqualFold(it.Hash, r.Hash) {
			m.lib.cursor = i
			return m, nil
		}
	}
	return m, tea.Batch(m.setBusy("not in the library list yet; refreshing"), m.fetchLibrary())
}

// searchChrome is the search tab's footer and detail strip; the mouse
// handler needs them too, to find where rows are drawn.
func (m Model) searchChrome(lo layout) (footer, detail string) {
	s := m.search
	// With nothing to move to, esc is still the way out of the box, and
	// what it reaches is the filter keys.
	leave := "results"
	if len(s.results) == 0 {
		leave = "filters"
	}
	typing := m.footer(lo,
		m.hints(h("enter", "search"), h("esc", leave)),
		m.hints(h("alt+1-3", "tabs"), h("tab", "next tab")),
	)
	n := len(s.results) > 0
	addLabel := "add"
	if n && s.results[s.cursor].Owned {
		addLabel = "open in library"
	}
	browsing := m.footer(lo,
		m.hints(h("/", "search"), hIf(n, "enter", addLabel), hIf(n && !s.usenet, "y", "copy magnet")),
		m.hints(h("c", "cached"), h("u", "usenet"), hIf(n, "s", "sort")),
		m.hints(h("a", "add link"), h("?", "help")),
	)
	if s.input.Focused() {
		footer = steadyFooter(typing, browsing)
	} else {
		footer = steadyFooter(browsing, typing)
	}
	if len(s.results) > 0 && !s.loading && s.err == "" && lo.Height >= 20 {
		detail = m.resultDetail(lo, s.results[s.cursor])
	}
	return footer, detail
}

// searchRows is where result rows are drawn: screen y of the first visible
// row and the visible index range.
func (m Model) searchRows(lo layout) (top, start, end int) {
	footer, detail := m.searchChrome(lo)
	start, end = window(len(m.search.results), m.search.cursor, m.bodyHeight(lo, detail, footer)-3-1)
	return bodyTop + 4, start, end
}

func (m Model) viewSearch(lo layout) string {
	s := m.search
	mode := "torrents"
	if s.usenet {
		mode = "usenet"
	}
	var b strings.Builder
	in := s.input
	in.SetWidth(lo.ContentWidth - 4)
	b.WriteString(in.View() + "\n")
	b.WriteString(m.rule(lo, s.input.Focused()) + "\n")
	filters := "  " + m.toggle("c", "cached only", s.cachedOnly) + "   " + m.toggle("u", "usenet", s.usenet)
	if summary := m.searchSummary(); summary != "" {
		pad := lo.ContentWidth - lipgloss.Width(filters) - lipgloss.Width(summary)
		if pad >= 2 {
			filters += strings.Repeat(" ", pad) + summary
		}
	}
	b.WriteString(filters + "\n")

	footer, detail := m.searchChrome(lo)
	listH := m.bodyHeight(lo, detail, footer) - 3

	if s.loading || s.err != "" || len(s.results) == 0 {
		b.WriteString("\n")
	}
	switch {
	case s.loading:
		b.WriteString(m.st.secondary.Render("  searching " + mode + " for “" + s.query + "”…"))
	case s.err != "":
		b.WriteString(m.st.danger.Render(wrap("  "+s.err, lo.ContentWidth)))
		if strings.Contains(s.err, "unreachable") {
			b.WriteString("\n\n" + m.st.secondary.Render(wrap("  TorBox's search service is separate from the main API and is currently not answering. The library, adding links with a, and downloads still work.", lo.ContentWidth)))
		}
	case s.query == "":
		b.WriteString(m.st.secondary.Render("  type a title or an IMDb id and press enter"))
	case len(s.results) == 0:
		msg := "  no results"
		if s.cachedOnly {
			msg += " (cached only is on: press c to include uncached)"
		}
		b.WriteString(m.st.secondary.Render(msg))
	default:
		b.WriteString(m.searchHeader(lo) + "\n")
		start, end := window(len(s.results), s.cursor, listH-1)
		for i := start; i < end; i++ {
			b.WriteString(m.searchRow(lo, s.results[i], i == s.cursor, !s.input.Focused()) + "\n")
		}
	}
	return m.page(lo, strings.TrimRight(b.String(), "\n"), detail, footer)
}

// searchSummary is "12 results · 8 cached · 1 yours".
func (m Model) searchSummary() string {
	r := m.search.results
	if len(r) == 0 || m.search.loading {
		return ""
	}
	cached, owned := 0, 0
	for _, x := range r {
		if x.Cached {
			cached++
		}
		if x.Owned {
			owned++
		}
	}
	parts := []string{fmt.Sprintf("%d result%s", len(r), plural(len(r)))}
	if !m.search.cachedOnly {
		parts = append(parts, fmt.Sprintf("%d cached", cached))
	}
	if owned > 0 {
		parts = append(parts, fmt.Sprintf("%d yours", owned))
	}
	return m.st.secondary.Render(strings.Join(parts, " · "))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// resultDetail shows the selected result in full: the name is what list
// truncation hurts most, since quality and codec sit at its end.
func (m Model) resultDetail(lo layout, r torbox.Result) string {
	name := wrapLines(r.Name(), lo.ContentWidth, 2)
	var meta []string
	switch {
	case r.Owned:
		meta = append(meta, m.st.accent.Render("in your library"))
	case r.Cached:
		meta = append(meta, m.st.success.Render("cached: instant"))
	default:
		meta = append(meta, m.st.secondary.Render("not cached: TorBox downloads it first"))
	}
	if r.Tracker != "" {
		meta = append(meta, m.st.secondary.Render(r.Tracker))
	}
	if r.Files > 0 {
		meta = append(meta, m.st.secondary.Render(fmt.Sprintf("%d file%s", r.Files, plural(int(r.Files)))))
	}
	if r.Age != "" {
		meta = append(meta, m.st.secondary.Render(r.Age+" old"))
	}
	if r.Parsed.Resolution != "" || r.Parsed.Codec != "" {
		meta = append(meta, m.st.secondary.Render(strings.TrimSpace(r.Parsed.Resolution+" "+r.Parsed.Codec)))
	}
	return m.st.primary.Render(name) + "\n" + strings.Join(meta, m.st.faint.Render(" · "))
}

// wrapLines hard-wraps s to w cells, keeping at most n lines.
func wrapLines(s string, w, n int) string {
	var out []string
	r := []rune(s)
	for len(r) > 0 && len(out) < n {
		cut := len(r)
		for lipgloss.Width(string(r[:cut])) > w {
			cut--
		}
		if len(out) == n-1 && cut < len(r) {
			out = append(out, truncate(string(r), w))
			break
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return strings.Join(out, "\n")
}

// toggle renders a filter as "c [x] cached only". The key comes first, the
// way the tab bar labels its tabs, because this row is not a focus stop:
// nothing lands on it, it is only switched by its key. A checkbox rather
// than a dot, so it does not read as a cursor either. While the search box
// has focus the key is dimmed, since c and u type into the query there.
func (m Model) toggle(key, label string, on bool) string {
	keyStyle := m.st.hintKey
	if m.search.input.Focused() {
		keyStyle = m.st.faint
	}
	box, text := m.st.faint.Render("[ ]"), m.st.secondary.Render(label)
	if on {
		box, text = m.st.success.Render("[x]"), m.st.primary.Render(label)
	}
	return keyStyle.Render(key) + " " + box + " " + text
}

// searchCols sizes the columns; wide terminals get a status column.
func (m Model) searchCols(lo layout) (name int, showAge, showStatus bool) {
	fixed := 2 + 10 + 9
	showAge = !lo.Compact
	showStatus = lo.Width >= 120
	if showAge {
		fixed += 7
	}
	if showStatus {
		fixed += 9
	}
	return lo.ContentWidth - 2 - fixed, showAge, showStatus
}

func (m Model) searchHeader(lo layout) string {
	nameW, showAge, showStatus := m.searchCols(lo)
	arrow := func(label string, mode sortMode) string {
		if m.search.sort == mode {
			return label + " ▾"
		}
		return label
	}
	hd := "  " + padRight(arrow("name", sortName), nameW)
	if showStatus {
		hd += padRight("", 9)
	}
	hd += padLeft(arrow("size", sortSize), 10) + padLeft(arrow("seeds", sortSeeders), 9)
	if showAge {
		hd += padLeft("age", 7)
	}
	return "  " + m.st.section.Render(hd)
}

func (m Model) searchRow(lo layout, r torbox.Result, sel, focused bool) string {
	nameW, showAge, showStatus := m.searchCols(lo)
	dot, status := m.st.dotIdle(), ""
	switch {
	case r.Owned:
		dot, status = m.st.accent.Render("◆"), m.st.accent.Render(padRight("yours", 9))
	case r.Cached:
		dot, status = m.st.dotReady(), m.st.success.Render(padRight("cached", 9))
	default:
		status = padRight("", 9)
	}
	nameStyle := m.st.primary
	if !sel && !r.Cached && !r.Owned {
		nameStyle = m.st.secondary
	}
	seeds := "-"
	if r.Seeders >= 0 && r.Type != "usenet" {
		seeds = strconv.Itoa(r.Seeders)
	}
	line := dot + " " + nameStyle.Render(padRight(r.Name(), nameW))
	if showStatus {
		line += status
	}
	line += m.st.secondary.Render(padLeft(humanBytes(int64(r.Size)), 10) + padLeft(seeds, 9))
	if showAge {
		line += m.st.secondary.Render(padLeft(r.Age, 7))
	}
	return m.listRow(lo, line, sel, focused)
}

// wrap flows s to w cells, indenting continuation lines by two.
func wrap(s string, w int) string {
	if w < 10 {
		return s
	}
	// Fields drops the caller's leading indent; keep it on the first line so
	// the message lines up with the rest of the body.
	indent := s[:len(s)-len(strings.TrimLeft(s, " "))]
	var out, line []string
	n := 0
	for _, word := range strings.Fields(s) {
		for _, piece := range splitLong(word, w-2) {
			switch {
			case len(line) == 0 && len(out) == 0:
				piece = indent + piece
			case n+lipgloss.Width(piece)+1 > w:
				out = append(out, strings.Join(line, " "))
				line, n = nil, 0
				piece = "  " + piece
			}
			line = append(line, piece)
			n += lipgloss.Width(piece) + 1
		}
	}
	if len(line) > 0 {
		out = append(out, strings.Join(line, " "))
	}
	return strings.Join(out, "\n")
}

// splitLong breaks a word that cannot fit a line of its own into w-wide
// chunks. Without it a long unbroken run — the request URL inside a
// transport error, typically — has nowhere to wrap and the frame clips the
// rest of the message away.
func splitLong(word string, w int) []string {
	if w < 1 || lipgloss.Width(word) <= w {
		return []string{word}
	}
	var out []string
	r := []rune(word)
	for len(r) > 0 {
		cut := len(r)
		for cut > 1 && lipgloss.Width(string(r[:cut])) > w {
			cut--
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	return out
}
