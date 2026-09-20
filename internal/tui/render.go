package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	minWidth     = 50
	minHeight    = 12
	widthCompact = 80
)

type layout struct {
	Width, Height int
	ContentWidth  int // inside frame border and padding
	Compact       bool
}

func newLayout(w, h int) layout {
	cw := w - 4
	if cw < 1 {
		cw = 1
	}
	return layout{Width: w, Height: h, ContentWidth: cw, Compact: w < widthCompact}
}

// bodyTop is the screen row of the first body line: the title bar, then
// the frame's top border.
const bodyTop = 2

// page renders the title bar above a full-height frame. From the bottom
// up the frame holds the key hints, a fixed status line, and an optional
// detail strip for the selected item; the body fills the rest. Body lines
// are clipped so a long line or a short terminal never pushes the border
// off screen, and the status line never makes the layout jump.
func (m Model) page(lo layout, body, detail, footer string) string {
	title := m.titleBar(lo)
	frameH := lo.Height - lipgloss.Height(title)
	room := m.bodyHeight(lo, detail, footer)
	lines := strings.Split(body, "\n")
	if len(lines) > room {
		lines = lines[:room]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, lo.ContentWidth, "…")
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	if detail != "" {
		lines = append(lines, m.st.divider.Render(strings.Repeat("─", lo.ContentWidth)))
		for _, l := range strings.Split(detail, "\n") {
			lines = append(lines, ansi.Truncate(l, lo.ContentWidth, "…"))
		}
	}
	lines = append(lines, m.statusLine(lo), footer)
	return title + "\n" + m.st.frame.Width(lo.Width).Height(frameH).MaxHeight(frameH).Render(strings.Join(lines, "\n"))
}

// bodyHeight is the number of body lines page() leaves for the list.
func (m Model) bodyHeight(lo layout, detail, footer string) int {
	h := lo.Height - 1 - 2 - 1 - lipgloss.Height(footer)
	if detail != "" {
		h -= 1 + lipgloss.Height(detail)
	}
	if h < 1 {
		return 1
	}
	return h
}

// tabItem is one tab in the title bar; x0/x1 are its screen columns.
type tabItem struct {
	key, label string
	v          view
	x0, x1     int
}

const (
	titleText = "tori"
	tabGap    = "   "
)

// tabItems lists the tabs with their screen columns, shared by the title
// bar and the mouse handler.
func (m Model) tabItems() []tabItem {
	items := []tabItem{{key: "1", label: "search", v: viewSearch}, {key: "2", label: "library", v: viewLibrary}, {key: "3", label: "downloads", v: viewDownloads}}
	x := len(titleText) + len(tabGap)
	for i := range items {
		if items[i].v == viewDownloads && m.activeJobs() > 0 {
			items[i].label += fmt.Sprintf(" %d", m.activeJobs())
		}
		w := lipgloss.Width(items[i].key + " " + items[i].label)
		items[i].x0, items[i].x1 = x, x+w
		x += w + len(tabGap)
	}
	return items
}

func (m Model) titleBar(lo layout) string {
	left := m.st.title.Render(titleText) + tabGap
	for i, t := range m.tabItems() {
		if i > 0 {
			left += tabGap
		}
		if m.tab == t.v {
			left += m.st.tabKey.Render(t.key) + " " + m.st.tabActive.Render(t.label)
		} else {
			left += m.st.faint.Render(t.key) + " " + m.st.tab.Render(t.label)
		}
	}
	meta := m.accountMeta()
	if meta == "" {
		return left
	}
	metaR := m.st.secondary.Render(meta)
	pad := lo.Width - lipgloss.Width(left) - lipgloss.Width(metaR)
	if pad < 2 {
		return left
	}
	return left + strings.Repeat(" ", pad) + metaR
}

// accountMeta is the plan and how long it has left.
func (m Model) accountMeta() string {
	if m.user == nil {
		return ""
	}
	meta := m.user.PlanName()
	if m.user.PremiumExpiresAt != nil {
		if t, ok := parseTime(*m.user.PremiumExpiresAt); ok {
			days := int(time.Until(t).Hours() / 24)
			switch {
			case days < 0:
				meta += " · expired"
			case days == 0:
				meta += " · ends today"
			default:
				meta += fmt.Sprintf(" · %dd left", days)
			}
		}
	}
	return meta
}

// statusLine is always one line: the latest message on the left and the
// library sync state on the right.
func (m Model) statusLine(lo layout) string {
	right := m.syncState()
	left := ""
	if m.status != "" {
		st := m.st.secondary
		if m.statusErr {
			st = m.st.danger
		}
		max := lo.ContentWidth - lipgloss.Width(right) - 2
		left = st.Render(truncate(m.status, max))
	}
	pad := lo.ContentWidth - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		return left
	}
	return left + strings.Repeat(" ", pad) + right
}

func (m Model) syncState() string {
	switch {
	case m.api == nil:
		return ""
	case m.lib.err != "" && m.lib.failedAt.After(m.lib.syncedAt):
		return m.st.warning.Render("sync failed " + ago(m.lib.failedAt))
	case m.lib.syncedAt.IsZero():
		return m.st.faint.Render("syncing…")
	default:
		return m.st.faint.Render("synced " + ago(m.lib.syncedAt))
	}
}

// footer flows hint groups onto lines, divided by │, keeping a group
// together when it fits and breaking it between hints when it does not.
func (m Model) footer(lo layout, groups ...string) string {
	var lines []string
	w := lo.ContentWidth
	cur := ""
	add := func(piece, sep string) {
		switch {
		case cur == "":
			cur = piece
		case lipgloss.Width(cur+sep+piece) <= w:
			cur += sep + piece
		default:
			lines = append(lines, cur)
			cur = piece
		}
	}
	for _, g := range groups {
		if g == "" {
			continue
		}
		joined := strings.ReplaceAll(g, hintSep, hintGap)
		if lipgloss.Width(joined) <= w {
			add(joined, m.st.footerDivide)
			continue
		}
		for i, h := range strings.Split(g, hintSep) {
			sep := hintGap
			if i == 0 {
				sep = m.st.footerDivide
			}
			add(h, sep)
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// hintSep separates hints inside a group so footer can break between them.
const (
	hintSep = "\x1f"
	hintGap = "  "
)

// hint is one key hint; off dims it when the action does not apply.
type hint struct {
	key, label string
	off        bool
}

func h(key, label string) hint { return hint{key: key, label: label} }

// hIf is a hint that is dimmed unless on is true.
func hIf(on bool, key, label string) hint { return hint{key: key, label: label, off: !on} }

func (m Model) hints(hs ...hint) string {
	parts := make([]string, 0, len(hs))
	for _, x := range hs {
		if x.off {
			parts = append(parts, m.st.faint.Render(x.key+" "+x.label))
		} else {
			parts = append(parts, m.st.hintKey.Render(x.key)+" "+m.st.hintLabel.Render(x.label))
		}
	}
	return strings.Join(parts, hintSep)
}

// row renders one list line padded to the content width. The selected row
// gets an accent bar and the theme's selection background across the whole
// line.
func (m Model) row(lo layout, text string, selected bool) string {
	w := lo.ContentWidth - 2
	text = ansi.Truncate(text, w, "…")
	if pad := w - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	if !selected {
		return "  " + text
	}
	return m.st.accent.Render("▐") + m.onSelection(" "+text)
}

// onSelection paints s on the selection background. Each styled segment in
// s ends with a reset that would also end an outer background, so the
// background is re-applied after every reset rather than wrapped around s.
func (m Model) onSelection(s string) string {
	r, g, b, _ := m.st.pal.Selection.RGBA()
	bg := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	s = strings.NewReplacer(
		"\x1b[m", "\x1b[m"+bg,
		"\x1b[0m", "\x1b[0m"+bg,
		"\x1b[49m", bg,
	).Replace(s)
	return bg + s + "\x1b[m"
}

// window returns [start,end) of n rows that fit height, keeping cursor visible.
func window(n, cursor, height int) (int, int) {
	if height < 1 {
		height = 1
	}
	if n <= height {
		return 0, n
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > n {
		start = n - height
	}
	return start, start + height
}

func (m Model) progressBar(frac float64, width int) string {
	if width < 1 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	full := int(frac * float64(width))
	return m.st.barFull.Render(strings.Repeat("━", full)) + m.st.barEmpty.Render(strings.Repeat("─", width-full))
}

func (m Model) overlayBox(lo layout, style lipgloss.Style, body string) string {
	w := lo.Width - 4
	if w > 64 {
		w = 64
	}
	box := style.Width(w).Render(body)
	return lipgloss.Place(lo.Width, lo.Height, lipgloss.Center, lipgloss.Center, box)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// padRight pads or truncates plain text to exactly w cells.
func padRight(s string, w int) string {
	s = truncate(s, w)
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func padLeft(s string, w int) string {
	s = truncate(s, w)
	if n := lipgloss.Width(s); n < w {
		s = strings.Repeat(" ", w-n) + s
	}
	return s
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanSpeed(n int64) string {
	if n <= 0 {
		return ""
	}
	return humanBytes(n) + "/s"
}

func humanETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	mnt := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, mnt)
	case mnt > 0:
		return fmt.Sprintf("%dm%02ds", mnt, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// inlineHints is hints for use inside a body or overlay, not the footer.
func (m Model) inlineHints(pairs ...string) string {
	hs := make([]hint, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		hs = append(hs, h(pairs[i], pairs[i+1]))
	}
	return strings.ReplaceAll(m.hints(hs...), hintSep, hintGap)
}

// ago is a short relative time: "now", "12s ago", "5m ago", "3h ago", "2d ago".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 2*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// until is a short future duration: "in 3h", "in 5d".
func until(t time.Time) string {
	d := time.Until(t)
	switch {
	case d <= 0:
		return "expired"
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes())+1)
	case d < 48*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

// parseTime accepts the timestamp shapes TorBox returns.
func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// tildePath shortens a path under home to ~/…
func tildePath(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
