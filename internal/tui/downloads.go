package tui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/tori/internal/aria2"
)

type downloadsState struct {
	jobs   []aria2.Job
	cursor int
	err    error
}

type jobsMsg struct {
	jobs []aria2.Job
	err  error
}

func (m Model) fetchJobs() tea.Cmd {
	dl := m.opt.Downloader
	if dl == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		jobs, err := dl.List(ctx)
		return jobsMsg{jobs: jobs, err: err}
	}
}

// orderedJobs puts unfinished jobs first, then finished ones, keeping
// aria2's order within each group. Keys and the view both index into it.
func (m Model) orderedJobs() []aria2.Job {
	var open, done []aria2.Job
	for _, j := range m.dl.jobs {
		if j.Status == "complete" || j.Status == "removed" {
			done = append(done, j)
		} else {
			open = append(open, j)
		}
	}
	return append(open, done...)
}

func (m Model) handleDownloadsKey(key string) (tea.Model, tea.Cmd) {
	jobs := m.orderedJobs()
	n := len(jobs)
	switch key {
	case "j", "down":
		m.dl.cursor = clamp(m.dl.cursor+1, n)
		return m, nil
	case "k", "up":
		m.dl.cursor = clamp(m.dl.cursor-1, n)
		return m, nil
	case "C":
		return m, m.dlAction("cleared finished", func(ctx context.Context, dl Downloader) error {
			return dl.ClearFinished(ctx)
		})
	case "o":
		return m, m.openFolder(m.opt.Config.DownloadDir)
	}
	if n == 0 {
		return m, nil
	}
	j := jobs[m.dl.cursor]
	switch key {
	case "p", "space", " ":
		switch j.Status {
		case "active", "waiting":
			return m, m.dlAction("paused "+truncate(j.Name, 40), func(ctx context.Context, dl Downloader) error {
				return dl.Pause(ctx, j.GID)
			})
		case "paused":
			return m, m.dlAction("resumed "+truncate(j.Name, 40), func(ctx context.Context, dl Downloader) error {
				return dl.Resume(ctx, j.GID)
			})
		}
	case "x", "D":
		verb := "removed "
		if j.Status == "active" || j.Status == "waiting" || j.Status == "paused" {
			verb = "cancelled "
		}
		return m, m.dlAction(verb+truncate(j.Name, 40), func(ctx context.Context, dl Downloader) error {
			return dl.Remove(ctx, j)
		})
	case "enter", "O":
		if j.Path != "" {
			return m, m.openFolder(filepath.Dir(j.Path))
		}
	}
	return m, nil
}

func (m Model) dlAction(text string, fn func(context.Context, Downloader) error) tea.Cmd {
	dl := m.opt.Downloader
	if dl == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := fn(ctx, dl)
		if err != nil {
			return actionMsg{text: "aria2", err: err, refreshDL: true}
		}
		return actionMsg{text: text, refreshDL: true}
	}
}

func (m Model) openFolder(dir string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("xdg-open", dir)
		if err := cmd.Start(); err != nil {
			return actionMsg{text: "open folder", err: err}
		}
		go func() { _ = cmd.Wait() }()
		return actionMsg{text: "opened " + dir}
	}
}

func (m Model) jobDot(j aria2.Job) string {
	switch j.Status {
	case "complete":
		return m.st.dotReady()
	case "error":
		return m.st.dotFailed()
	case "active":
		return m.st.dotProgress()
	default:
		return m.st.dotIdle()
	}
}

// dlFooter is the downloads tab's footer.
func (m Model) dlFooter(lo layout) string {
	jobs := m.orderedJobs()
	var sel aria2.Job
	ok := len(jobs) > 0
	if ok {
		sel = jobs[m.dl.cursor]
	}
	running := ok && (sel.Status == "active" || sel.Status == "waiting" || sel.Status == "paused")
	pauseLabel := "pause"
	if sel.Status == "paused" {
		pauseLabel = "resume"
	}
	removeLabel := "remove"
	if running {
		removeLabel = "cancel"
	}
	finished := 0
	for _, j := range jobs {
		if j.Status == "complete" || j.Status == "error" || j.Status == "removed" {
			finished++
		}
	}
	return m.footer(lo,
		m.hints(hIf(running, "p", pauseLabel), hIf(ok, "x", removeLabel), hIf(finished > 0, "C", "clear finished")),
		m.hints(hIf(ok && sel.Path != "", "enter", "open folder"), h("o", "downloads folder"), h("?", "help")),
	)
}

// dlLine is one screen line of the job list; job is -1 for headers.
type dlLine struct {
	text string
	job  int
}

// dlLines lays out the job list: unfinished jobs take two lines (name, then
// bar), finished ones one, under a header. It returns the visible slice and
// the screen y of its first line, so drawing and clicks agree.
func (m Model) dlLines(lo layout) (top int, lines []dlLine) {
	jobs := m.orderedJobs()
	var all []dlLine
	header := false
	for i, j := range jobs {
		sel := i == m.dl.cursor
		done := j.Status == "complete" || j.Status == "removed"
		if done && !header {
			all = append(all, dlLine{"  " + m.st.section.Render(fmt.Sprintf("finished · %d", countDone(jobs))), -1})
			header = true
		}
		if done {
			where := m.relDir(j.Path)
			nameW := lo.ContentWidth - 2 - 2 - 10 - 2 - min(lipglossWidth(where), lo.ContentWidth/3)
			text := m.jobDot(j) + " " + m.st.primary.Render(padRight(j.Name, nameW)) +
				m.st.secondary.Render(padLeft(humanBytes(j.Total), 10)) + "  " +
				m.st.faint.Render(truncate(where, lo.ContentWidth/3))
			all = append(all, dlLine{m.row(lo, text, sel), i})
			continue
		}
		nameW := lo.ContentWidth - 2 - 2 - 12
		status := j.Status
		if status == "active" {
			status = "downloading"
		}
		l1 := m.jobDot(j) + " " + m.st.primary.Render(padRight(j.Name, nameW)) + m.st.secondary.Render(padLeft(status, 12))
		var l2 string
		if j.Status == "error" {
			l2 = "  " + m.st.danger.Render(j.ErrorMsg)
		} else {
			stats := m.jobStats(j)
			barW := min(lo.ContentWidth-2-2-lipglossWidth(stats)-2, 40)
			l2 = "  " + m.progressBar(j.Progress(), barW) + "  " + m.st.secondary.Render(stats)
		}
		all = append(all, dlLine{m.row(lo, l1, sel), i}, dlLine{m.row(lo, l2, sel), i})
	}
	// Scroll by lines, keeping the selected job's lines in view.
	first, last := -1, 0
	for k, l := range all {
		if l.job == m.dl.cursor {
			if first < 0 {
				first = k
			}
			last = k
		}
	}
	room := m.bodyHeight(lo, "", m.dlFooter(lo)) - 1
	start := 0
	if last >= room {
		start = last - room + 1
	}
	if first >= 0 && first < start {
		start = first
	}
	end := min(len(all), start+room)
	return bodyTop + 1, all[start:end]
}

func (m Model) viewDownloads(lo layout) string {
	footer := m.dlFooter(lo)
	dir := tildePath(m.opt.Config.DownloadDir, m.opt.Home)
	var b strings.Builder
	b.WriteString(m.st.section.Render("downloads") + m.st.secondary.Render(" → "+truncate(dir, lo.ContentWidth-14)) + "\n")
	switch {
	case m.opt.Downloader == nil:
		msg := "aria2c is not running, so downloads are off"
		if m.opt.DLErr != nil {
			msg = m.opt.DLErr.Error()
		}
		b.WriteString(m.st.danger.Render(wrap("  "+msg, lo.ContentWidth)))
	case m.dl.err != nil && len(m.dl.jobs) == 0:
		b.WriteString(m.st.danger.Render("  aria2: " + m.dl.err.Error()))
	case len(m.dl.jobs) == 0:
		b.WriteString(m.st.secondary.Render("  nothing downloading: pick something in the library (2) and press d"))
	default:
		_, lines := m.dlLines(lo)
		for _, l := range lines {
			b.WriteString(l.text + "\n")
		}
	}
	return m.page(lo, strings.TrimRight(b.String(), "\n"), "", footer)
}

// item's subfolder, or "" when it sits at the top.
func (m Model) relDir(path string) string {
	dir := filepath.Dir(path)
	rel, err := filepath.Rel(m.opt.Config.DownloadDir, dir)
	switch {
	case err != nil || strings.HasPrefix(rel, ".."):
		return tildePath(dir, m.opt.Home)
	case rel == ".":
		return ""
	default:
		return rel + "/"
	}
}

func countDone(jobs []aria2.Job) int {
	n := 0
	for _, j := range jobs {
		if j.Status == "complete" || j.Status == "removed" {
			n++
		}
	}
	return n
}

func (m Model) jobStats(j aria2.Job) string {
	s := fmt.Sprintf("%3.0f%%  %s / %s", j.Progress()*100, humanBytes(j.Done), humanBytes(j.Total))
	if sp := humanSpeed(j.Speed); sp != "" && j.Status == "active" {
		s += "  " + sp
	}
	if eta := humanETA(j.ETA()); eta != "" && j.Status == "active" {
		s += "  " + eta + " left"
	}
	return s
}
