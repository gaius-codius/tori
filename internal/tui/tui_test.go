package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/gaius-codius/tori/internal/aria2"
	"github.com/gaius-codius/tori/internal/config"
	"github.com/gaius-codius/tori/internal/secret"
	"github.com/gaius-codius/tori/internal/torbox"
)

type fakeAPI struct {
	mu       sync.Mutex
	meErr    error
	results  []torbox.Result
	items    []torbox.Item
	searched []torbox.SearchOptions
	added    []string
	deleted  []int64
	links    []int64

	reannounced []int64
}

func (f *fakeAPI) Me(context.Context) (torbox.User, error) {
	return torbox.User{Plan: 2, Email: "me@example.com"}, f.meErr
}
func (f *fakeAPI) Search(_ context.Context, q string, o torbox.SearchOptions) ([]torbox.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searched = append(f.searched, o)
	return append([]torbox.Result(nil), f.results...), nil
}
func (f *fakeAPI) List(_ context.Context, k torbox.Kind) ([]torbox.Item, error) {
	var out []torbox.Item
	for _, it := range f.items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out, nil
}
func (f *fakeAPI) AddMagnet(_ context.Context, m string, _ bool) (torbox.AddResult, error) {
	f.added = append(f.added, "magnet:"+m)
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) AddWeb(_ context.Context, l string) (torbox.AddResult, error) {
	f.added = append(f.added, "web:"+l)
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) AddUsenet(_ context.Context, l, _ string) (torbox.AddResult, error) {
	f.added = append(f.added, "usenet:"+l)
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) Delete(_ context.Context, it torbox.Item) error {
	f.deleted = append(f.deleted, it.ID)
	return nil
}
func (f *fakeAPI) Reannounce(_ context.Context, it torbox.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reannounced = append(f.reannounced, it.ID)
	return nil
}
func (f *fakeAPI) DownloadURL(_ context.Context, it torbox.Item, fileID int64) (string, error) {
	f.links = append(f.links, fileID)
	return "https://cdn.example/f", nil
}

type fakeDL struct {
	added []string
	jobs  []aria2.Job
}

func (d *fakeDL) Add(_ context.Context, url, name, subdir string) (string, error) {
	d.added = append(d.added, subdir+"/"+name)
	return "gid", nil
}
func (d *fakeDL) List(context.Context) ([]aria2.Job, error) { return d.jobs, nil }
func (d *fakeDL) Pause(context.Context, string) error       { return nil }
func (d *fakeDL) Resume(context.Context, string) error      { return nil }
func (d *fakeDL) Remove(context.Context, aria2.Job) error   { return nil }
func (d *fakeDL) ClearFinished(context.Context) error       { return nil }

func sampleItems() []torbox.Item {
	return []torbox.Item{
		{Kind: torbox.KindTorrent, ID: 1, Name: "Big.Buck.Bunny.2008.1080p.BluRay.x264", Size: 3 << 30,
			DownloadFinished: true, DownloadPresent: true, CreatedAt: "2026-09-19",
			Files: []torbox.File{{ID: 0, ShortName: "bbb.mkv", Size: 3 << 30}, {ID: 1, ShortName: "bbb.srt", Size: 40 << 10}}},
		{Kind: torbox.KindWeb, ID: 2, Name: "ubuntu-26.04-desktop-amd64.iso", Size: 6 << 30,
			State: "downloading", Progress: 0.42, Speed: 12 << 20, CreatedAt: "2026-09-20"},
	}
}

type harness struct {
	t   *testing.T
	m   Model
	api *fakeAPI
	dl  *fakeDL
	kr  *secret.Memory
}

func newHarness(t *testing.T, env map[string]string) *harness {
	t.Helper()
	h := &harness{t: t, api: &fakeAPI{items: sampleItems()}, dl: &fakeDL{}, kr: secret.NewMemory()}
	home := t.TempDir()
	cfg := config.Default(home)
	h.m = New(Options{
		Config:     cfg,
		Keyring:    h.kr,
		Getenv:     func(k string) string { return env[k] },
		Downloader: h.dl,
		NewAPI:     func(string) API { return h.api },
		Home:       home,
		Clipboard:  func(string) error { return nil },
	})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.run(h.m.Init())
	return h
}

// send delivers msg and runs resulting commands synchronously, skipping
// ticks (which would block) and following batches.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	next, cmd := h.m.Update(msg)
	h.m = next.(Model)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				h.run(c)
			}
		case nil:
		default:
			h.send(msg)
		}
	case <-timeout():
		// A tick; ignore.
	}
}

func (h *harness) key(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		h.send(msg)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) view() string { return h.m.render() }

func TestFirstRun_PromptsForKeyAndSavesIt(t *testing.T) {
	h := newHarness(t, nil)
	if h.m.overlay != overlayKey || !strings.Contains(h.view(), "Enter your TorBox API key") {
		t.Fatalf("expected key prompt:\n%s", h.view())
	}
	h.typeText("abc123")
	h.key("enter")
	if h.m.overlay != overlayNone {
		t.Fatalf("still on key prompt: %s", h.m.keyErr)
	}
	k, err := h.kr.Lookup()
	if err != nil || k.Reveal() != "abc123" {
		t.Fatalf("key not saved: %v", err)
	}
	if !strings.Contains(h.view(), "Pro") {
		t.Fatalf("missing account meta:\n%s", h.view())
	}
}

func TestFirstRun_RejectedKeyStaysOnPrompt(t *testing.T) {
	h := newHarness(t, nil)
	h.api.meErr = &torbox.APIError{Status: 403, Code: "BAD_TOKEN"}
	h.typeText("nope")
	h.key("enter")
	if h.m.overlay != overlayKey || !strings.Contains(h.view(), "BAD_TOKEN") {
		t.Fatalf("expected error on prompt:\n%s", h.view())
	}
	if _, err := h.kr.Lookup(); !errors.Is(err, secret.ErrNotFound) {
		t.Fatal("bad key was saved")
	}
}

func TestSearch_AddAndToggles(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{
		{Hash: "a", RawTitle: "Uncached.Release.720p", Magnet: "magnet:?xt=urn:btih:a", Seeders: 90, Size: 1 << 30},
		{Hash: "b", RawTitle: "Cached.Release.1080p", Magnet: "magnet:?xt=urn:btih:b", Seeders: 5, Size: 2 << 30, Cached: true},
	}
	h.typeText("bunny")
	h.key("enter")
	v := h.view()
	if !strings.Contains(v, "Cached.Release.1080p") || strings.Index(v, "Cached.Release") > strings.Index(v, "Uncached") {
		t.Fatalf("cached result should sort first:\n%s", v)
	}
	if !h.api.searched[0].CachedOnly {
		t.Fatal("cached_only should default on")
	}
	h.key("c")
	if h.api.searched[1].CachedOnly {
		t.Fatal("c should turn cached only off and rerun")
	}
	h.key("enter")
	if len(h.api.added) != 1 || h.api.added[0] != "magnet:magnet:?xt=urn:btih:b" {
		t.Fatalf("added %v", h.api.added)
	}
}

// The filter row is not a focus stop, so it carries its own keys, and down
// only leaves the search box when there is a list to leave it for.
func TestSearch_FilterRowCarriesItsKeys(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	if v := ansi.Strip(h.view()); !strings.Contains(v, "c [x] cached only   u [ ] usenet") {
		t.Fatalf("filter row should show its keys:\n%s", v)
	}
	h.key("down")
	if !h.m.search.input.Focused() {
		t.Fatal("down with no results should keep the cursor in the search box")
	}
	h.key("esc")
	if h.m.search.input.Focused() {
		t.Fatal("esc should leave the box, which is how the filter keys are reached")
	}
	h.key("u")
	if !h.m.search.usenet {
		t.Fatal("u should flip the usenet filter")
	}

	h.api.results = []torbox.Result{{Hash: "a", RawTitle: "Some.Release", Magnet: "magnet:?xt=urn:btih:a", Cached: true}}
	h.key("/")
	h.typeText("x")
	h.key("enter")
	h.key("/")
	h.key("down")
	if h.m.search.input.Focused() {
		t.Fatal("down with results should move to the list")
	}
}

func TestLibrary_DownloadAndDelete(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2")
	v := h.view()
	if !strings.Contains(v, "ubuntu-26.04") || !strings.Contains(v, "42%") || !strings.Contains(v, "━") {
		t.Fatalf("library view:\n%s", v)
	}
	// Newest first: ubuntu (09-20) is row 0, bunny row 1.
	h.key("d")
	if len(h.dl.added) != 0 || !h.m.statusErr {
		t.Fatal("unfinished item should not download")
	}
	h.key("j", "d")
	if strings.Join(h.dl.added, ",") != "Big.Buck.Bunny.2008.1080p.BluRay.x264/bbb.mkv,Big.Buck.Bunny.2008.1080p.BluRay.x264/bbb.srt" {
		t.Fatalf("queued %v", h.dl.added)
	}
	h.key("enter")
	if h.m.overlay != overlayFiles {
		t.Fatal("enter should open files")
	}
	h.key("j", "space", "esc")
	h.key("D")
	if !strings.Contains(h.view(), "Delete from TorBox?") {
		t.Fatalf("confirm:\n%s", h.view())
	}
	h.key("n")
	if len(h.api.deleted) != 0 {
		t.Fatal("n must cancel")
	}
	h.key("D", "y")
	if len(h.api.deleted) != 1 || h.api.deleted[0] != 1 {
		t.Fatalf("deleted %v", h.api.deleted)
	}
}

func TestFiles_PickSubset(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2", "j", "enter", "j", "space", "enter")
	if len(h.dl.added) != 1 || !strings.HasSuffix(h.dl.added[0], "/bbb.srt") {
		t.Fatalf("queued %v", h.dl.added)
	}
}

func TestAddLink_Classifies(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:abc":                  "torrent",
		"0123456789abcdef0123456789abcdef01234567": "torrent",
		"https://indexer.example/get/123.nzb":      "usenet",
		"https://1fichier.com/?abc":                "web",
	}
	for in, want := range cases {
		if got := ClassifyLink(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "a")
	h.typeText("https://example.com/file.zip")
	h.key("enter")
	if len(h.api.added) != 1 || h.api.added[0] != "web:https://example.com/file.zip" {
		t.Fatalf("added %v", h.api.added)
	}
}

func TestRender_FitsTerminal(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{RawTitle: strings.Repeat("Very.Long.Release.Name.", 8), Size: 1 << 30, Cached: true, Seeders: 3, Age: "2d"}}
	h.typeText("x")
	h.key("enter")
	h.dl.jobs = []aria2.Job{{GID: "1", Status: "active", Name: "bbb.mkv", Total: 100, Done: 40, Speed: 5 << 20}}
	var dump strings.Builder
	for _, size := range [][2]int{{50, 12}, {80, 24}, {140, 40}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"1", "2", "3", "?"} {
			h.key("esc")
			h.key(tab)
			h.run(h.m.fetchJobs())
			v := h.view()
			for i, line := range strings.Split(v, "\n") {
				if w := lipgloss.Width(line); w > size[0] {
					t.Errorf("%dx%d tab %s line %d is %d wide", size[0], size[1], tab, i, w)
				}
			}
			if n := lipgloss.Height(v); n > size[1] {
				t.Errorf("%dx%d tab %s is %d lines tall", size[0], size[1], tab, n)
			}
			dump.WriteString("\n=== " + tab + " " + string(rune('0'+size[0]/10)) + "\n" + v + "\n")
		}
	}
	if p := os.Getenv("TORI_RENDER_DUMP"); p != "" {
		_ = os.WriteFile(p, []byte(dump.String()), 0o644)
	}
}

// The selected row must keep the selection background after every inner
// style reset, or only the gaps between segments get highlighted.
func TestRow_SelectionBackgroundSpansSegments(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	lo := newLayout(80, 24)
	text := h.m.st.success.Render("●") + " " + h.m.st.primary.Render("name") + h.m.st.secondary.Render("  1.0 GiB")
	out := h.m.row(lo, text, true)
	if w := lipgloss.Width(out); w != lo.ContentWidth {
		t.Fatalf("width %d, want %d", w, lo.ContentWidth)
	}
	r, g, b, _ := h.m.st.pal.Selection.RGBA()
	bg := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
	body := strings.TrimSuffix(out, "\x1b[m")
	for _, reset := range []string{"\x1b[m", "\x1b[0m"} {
		parts := strings.Split(body, reset)
		for i, p := range parts[1:] {
			if !strings.HasPrefix(p, bg) {
				t.Fatalf("reset %d not followed by selection bg: %q", i, out)
			}
		}
	}
	if plain := h.m.row(lo, text, false); strings.Contains(plain, bg) {
		t.Fatal("unselected row has selection bg")
	}
}

func TestSearch_AddMarksYoursThenOpensInLibrary(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{Hash: "BBB", RawTitle: "Big.Buck.Bunny", Magnet: "magnet:?xt=urn:btih:bbb", Cached: true}}
	// The library already holds hash bbb (lowercase): search must mark it.
	h.api.items = append(h.api.items, torbox.Item{Kind: torbox.KindTorrent, ID: 9, Hash: "bbb", Name: "Big.Buck.Bunny", CreatedAt: "2026-09-21"})
	h.run(h.m.fetchLibrary())
	h.typeText("bunny")
	h.key("enter")
	if !h.m.search.results[0].Owned || !strings.Contains(h.view(), "open in library") {
		t.Fatalf("owned result not marked:\n%s", h.view())
	}
	h.key("enter")
	if len(h.api.added) != 0 {
		t.Fatal("enter on an owned result must not add it again")
	}
	it, ok := h.m.selectedItem()
	if h.m.tab != viewLibrary || !ok || it.ID != 9 {
		t.Fatalf("tab %v item %+v", h.m.tab, it)
	}
}

func TestSearch_AddedFlagsRowImmediately(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{Hash: "new1", RawTitle: "Fresh.Release", Magnet: "magnet:?xt=urn:btih:new1"}}
	h.typeText("fresh")
	h.key("enter", "c")
	h.key("enter")
	if !h.m.search.results[0].Owned || !strings.Contains(h.m.status, "enter again") {
		t.Fatalf("owned=%v status=%q", h.m.search.results[0].Owned, h.m.status)
	}
}

func TestDownloads_FinishedGoLast(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.dl.jobs = []aria2.Job{
		{GID: "1", Status: "complete", Name: "done.mkv", Total: 10, Done: 10, Path: "/x/done.mkv"},
		{GID: "2", Status: "active", Name: "going.mkv", Total: 10, Done: 4},
	}
	h.key("esc", "3")
	h.run(h.m.fetchJobs())
	jobs := h.m.orderedJobs()
	if jobs[0].GID != "2" || jobs[1].GID != "1" {
		t.Fatalf("order %v", jobs)
	}
	v := h.view()
	if strings.Index(v, "going.mkv") > strings.Index(v, "finished · 1") || !strings.Contains(v, "40%") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestHints_DimWhenUnavailable(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	on := h.m.hints(hIf(true, "d", "download"))
	off := h.m.hints(hIf(false, "d", "download"))
	if on == off || !strings.Contains(off, "d download") {
		t.Fatalf("on %q off %q", on, off)
	}
}

func TestAltDigits_SwitchTabsWhileTyping(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	if !h.m.search.input.Focused() {
		t.Fatal("search should start focused")
	}
	h.typeText("2001")
	if h.m.search.input.Value() != "2001" {
		t.Fatalf("digits must type into the query, got %q", h.m.search.input.Value())
	}
	if !strings.Contains(h.view(), "alt+1-3") {
		t.Fatalf("footer should show the alt helper while typing:\n%s", h.view())
	}
	h.send(tea.KeyPressMsg{Code: '2', Mod: tea.ModAlt})
	if h.m.tab != viewLibrary || h.m.search.input.Focused() {
		t.Fatalf("alt+2: tab %v focused %v", h.m.tab, h.m.search.input.Focused())
	}
	h.send(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt})
	if h.m.tab != viewSearch || h.m.search.input.Value() != "2001" {
		t.Fatalf("alt+1: tab %v query %q", h.m.tab, h.m.search.input.Value())
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if h.m.tab != viewDownloads {
		t.Fatalf("shift+tab from the search box: tab %v", h.m.tab)
	}
}

// screenPos finds the line and column where text appears in the rendered view.
func (h *harness) screenPos(text string) (x, y int) {
	h.t.Helper()
	for i, line := range strings.Split(ansi.Strip(h.view()), "\n") {
		if j := strings.Index(line, text); j >= 0 {
			return lipgloss.Width(line[:j]), i
		}
	}
	h.t.Fatalf("%q not on screen:\n%s", text, ansi.Strip(h.view()))
	return 0, 0
}

func (h *harness) click(text string) {
	h.t.Helper()
	x, y := h.screenPos(text)
	h.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// A transport error carries the request URL, which has nothing to wrap on;
// without splitting it the frame clips the message instead of showing it.
func TestWrap_SplitsAnUnbreakableRun(t *testing.T) {
	long := "https://search-api.torbox.app/torrents/search?query=" + strings.Repeat("x", 120)
	got := wrap("  search failed: "+long, 60)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 60 {
			t.Fatalf("line %d is %d cells wide:\n%s", i, w, got)
		}
	}
	if joined := strings.ReplaceAll(strings.ReplaceAll(got, "\n", ""), " ", ""); !strings.Contains(joined, long) {
		t.Fatalf("the URL did not survive wrapping:\n%s", got)
	}
}

// The library list is already in memory, so / filters it locally. Tokens
// match in any order, because release names are dot-separated.
func TestLibrary_FilterByName(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2", "/")
	if !h.m.lib.input.Focused() {
		t.Fatal("/ should focus the filter")
	}
	h.typeText("bunny 1080")
	vis := h.m.visibleItems()
	if len(vis) != 1 || vis[0].ID != 1 {
		t.Fatalf("filter matched %d items: %+v", len(vis), vis)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "1 of 2") {
		t.Fatalf("headline should count the matches:\n%s", v)
	}
	h.key("enter")
	if h.m.lib.input.Focused() || h.m.lib.query != "bunny 1080" {
		t.Fatalf("enter should leave the box with the filter applied: %q", h.m.lib.query)
	}
	// Rows do not move, so a click still lands on the row it looks like.
	h.click("Big.Buck.Bunny")
	if it, ok := h.m.selectedItem(); !ok || it.ID != 1 {
		t.Fatalf("click selected %+v", it)
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "› bunny 1080") {
		t.Fatalf("the applied filter should stay visible:\n%s", v)
	}
	h.key("esc")
	if h.m.lib.query != "" || len(h.m.visibleItems()) != 2 {
		t.Fatalf("esc should clear the filter: %q", h.m.lib.query)
	}
	// The headline doubles as the box, so clicking it starts a filter.
	x, y := h.screenPos("filter by name")
	h.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !h.m.lib.input.Focused() {
		t.Fatal("clicking the headline should focus the filter")
	}
}

// A name filter must not hide the row that "open in library" jumps to.
func TestLibrary_OpenFromSearchClearsTheFilter(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2", "/")
	h.typeText("nothing matches this")
	if len(h.m.visibleItems()) != 0 {
		t.Fatal("expected the filter to hide everything")
	}
	next, _ := h.m.openInLibrary(torbox.Result{Hash: "whatever"})
	m := next.(Model)
	if m.lib.query != "" || m.lib.input.Value() != "" {
		t.Fatalf("the filter survived the jump: %q", m.lib.query)
	}
}

// The footer dims R once a torrent is ready; the key must agree with it.
func TestLibrary_ReannounceOnlyWhileUnfinished(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2", "j") // newest first: the web item, then the ready torrent
	it, ok := h.m.selectedItem()
	if !ok || it.Kind != torbox.KindTorrent || !it.Ready() {
		t.Fatalf("expected the finished torrent selected, got %+v", it)
	}
	h.key("R")
	if len(h.api.reannounced) != 0 {
		t.Fatalf("reannounced a finished torrent: %v", h.api.reannounced)
	}
	if !strings.Contains(h.m.status, "nothing to reannounce") {
		t.Fatalf("status %q", h.m.status)
	}
}

// aria2 moves a job between its active, waiting and stopped groups as it
// changes state, which reorders the list under the cursor.
func TestDownloads_SelectionFollowsTheJobAcrossRefreshes(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.dl.jobs = []aria2.Job{
		{GID: "1", Status: "active", Name: "one.mkv", Total: 10, Done: 2},
		{GID: "2", Status: "active", Name: "two.mkv", Total: 10, Done: 5},
	}
	h.key("esc", "3")
	h.run(h.m.fetchJobs())
	h.key("j")
	if got := h.m.selectedJobGID(); got != "2" {
		t.Fatalf("selected %q, want two.mkv", got)
	}
	h.dl.jobs = []aria2.Job{
		{GID: "2", Status: "paused", Name: "two.mkv", Total: 10, Done: 5},
		{GID: "1", Status: "active", Name: "one.mkv", Total: 10, Done: 3},
	}
	h.run(h.m.fetchJobs())
	if got := h.m.selectedJobGID(); got != "2" {
		t.Fatalf("selection jumped to %q when the list reordered", got)
	}
	h.dl.jobs = []aria2.Job{{GID: "1", Status: "active", Name: "one.mkv", Total: 10, Done: 4}}
	h.run(h.m.fetchJobs())
	if h.m.dl.cursor != 0 {
		t.Fatalf("cursor %d after the selected job left the list", h.m.dl.cursor)
	}
}

func TestMouse_TabsRowsAndActivate(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.click("library")
	if h.m.tab != viewLibrary {
		t.Fatalf("tab %v", h.m.tab)
	}
	// Newest first: ubuntu then bunny. A single click on ubuntu, already
	// selected, must not open it.
	h.click("ubuntu-26.04")
	if h.m.overlay != overlayNone {
		t.Fatal("a single click on the selected row must only select")
	}
	h.m.clicked = lastClick{}
	// Click bunny once to select, then a double-click opens it.
	h.click("Big.Buck.Bunny")
	if it, _ := h.m.selectedItem(); it.ID != 1 || h.m.overlay != overlayNone {
		t.Fatalf("first click should select only: %+v overlay %v", it, h.m.overlay)
	}
	h.click("Big.Buck.Bunny")
	if h.m.overlay != overlayFiles {
		t.Fatal("a double-click should open files")
	}
	h.click("bbb.srt")
	h.click("bbb.srt")
	if !h.m.files.picked[1] {
		t.Fatal("double-clicking a file should pick it")
	}
}

func TestMouse_SearchRowsTogglesAndWheel(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{
		{Hash: "a", RawTitle: "First.Result", Magnet: "magnet:?xt=urn:btih:a", Cached: true, Seeders: 9},
		{Hash: "b", RawTitle: "Second.Result", Magnet: "magnet:?xt=urn:btih:b", Cached: true, Seeders: 1},
	}
	h.typeText("x")
	h.key("enter")
	h.click("Second.Result")
	if h.m.search.cursor != 1 || len(h.api.added) != 0 {
		t.Fatalf("cursor %d added %v", h.m.search.cursor, h.api.added)
	}
	h.click("Second.Result")
	if len(h.api.added) != 1 || h.api.added[0] != "magnet:magnet:?xt=urn:btih:b" {
		t.Fatalf("added %v", h.api.added)
	}
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if h.m.search.cursor != 0 {
		t.Fatalf("wheel up: cursor %d", h.m.search.cursor)
	}
	before := len(h.api.searched)
	h.click("cached only")
	if h.m.search.cachedOnly || len(h.api.searched) != before+1 {
		t.Fatal("clicking the toggle should flip it and search again")
	}
	h.click("x ")
	if !h.m.search.input.Focused() {
		t.Fatal("clicking the search box should focus it")
	}
}

func TestMouse_DownloadsSelectsJobOnEitherLine(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.dl.jobs = []aria2.Job{
		{GID: "1", Status: "active", Name: "one.mkv", Total: 10, Done: 2},
		{GID: "2", Status: "active", Name: "two.mkv", Total: 10, Done: 5},
		{GID: "3", Status: "complete", Name: "three.mkv", Total: 10, Done: 10, Path: "/d/three.mkv"},
	}
	h.key("esc", "3")
	h.run(h.m.fetchJobs())
	h.click("two.mkv")
	if h.m.dl.cursor != 1 {
		t.Fatalf("cursor %d", h.m.dl.cursor)
	}
	h.click("three.mkv")
	if h.m.dl.cursor != 2 {
		t.Fatalf("cursor %d", h.m.dl.cursor)
	}
	_, y := h.screenPos("finished ·")
	h.send(tea.MouseClickMsg{X: 5, Y: y, Button: tea.MouseLeft})
	if h.m.dl.cursor != 2 {
		t.Fatal("clicking the header must not move the cursor")
	}
}

func TestMouse_ConfigCanTurnItOff(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	if h.m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse should default on")
	}
	off := false
	h.m.opt.Config.Mouse = &off
	if h.m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("mouse = false should disable it")
	}
}

func TestMouse_SlowSecondClickDoesNotActivate(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2")
	h.click("Big.Buck.Bunny")
	h.m.clicked.at = h.m.clicked.at.Add(-time.Second)
	h.click("Big.Buck.Bunny")
	if h.m.overlay != overlayNone {
		t.Fatal("clicks a second apart are not a double-click")
	}
}

// The list is sized by subtracting the footer, so a footer that changed
// height with focus would shift the rows under the cursor as you type.
func TestFooter_SameHeightInEveryFocusState(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{Hash: "a", RawTitle: "Some.Release", Magnet: "magnet:?xt=urn:btih:a", Cached: true}}
	h.typeText("x")
	h.key("enter")
	for _, w := range []int{50, 60, 80, 100, 140} {
		lo := newLayout(w, 30)
		m := h.m
		m.search.input.Focus()
		typing, _ := m.searchChrome(lo)
		m.search.input.Blur()
		browsing, _ := m.searchChrome(lo)
		if lipgloss.Height(typing) != lipgloss.Height(browsing) {
			t.Fatalf("search at %d cols: footer %d rows typing, %d browsing", w, lipgloss.Height(typing), lipgloss.Height(browsing))
		}
		m.lib.input.Focus()
		typing, _ = m.libChrome(lo)
		m.lib.input.Blur()
		browsing, _ = m.libChrome(lo)
		if lipgloss.Height(typing) != lipgloss.Height(browsing) {
			t.Fatalf("library at %d cols: footer %d rows typing, %d browsing", w, lipgloss.Height(typing), lipgloss.Height(browsing))
		}
	}
}

// Only the thing listening wears the accent: while the query box has focus
// the selected result keeps a grey bar and loses its band.
func TestSearch_FocusMovesTheSelectionBand(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{Hash: "a", RawTitle: "Some.Release", Magnet: "magnet:?xt=urn:btih:a", Cached: true}}
	h.typeText("x")
	h.key("enter")
	const band = "\x1b[48;2;"
	if v := h.view(); !strings.Contains(v, band) {
		t.Fatal("the list has focus, so its selected row should carry the band")
	}
	h.key("/")
	v := h.view()
	if strings.Contains(v, band) {
		t.Fatal("the box has focus, so the list's band should go")
	}
	if !strings.Contains(ansi.Strip(v), "▐ ● Some.Release") {
		t.Fatalf("the cursor should still show:\n%s", ansi.Strip(v))
	}
}

func TestSelection_ReverseVideoWithoutColour(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k", "NO_COLOR": "1"})
	h.key("esc", "2")
	v := h.view()
	if !strings.Contains(v, "\x1b[7m") || strings.Contains(v, "\x1b[48;2;") {
		t.Fatal("NO_COLOR should draw the selection in reverse video, not a band")
	}
	// One unbroken strip: a colour inside it would flip into a background
	// and patch the row.
	strip := v[strings.Index(v, "\x1b[7m")+len("\x1b[7m"):]
	strip = strip[:strings.Index(strip, "\x1b[m")]
	if strings.Contains(strip, "\x1b[") || !strings.Contains(strip, "ubuntu-26.04") {
		t.Fatalf("reverse strip %q", strip)
	}
}

// A click on a row hands the keys back to the list, as it does in search,
// so d downloads instead of typing into the filter.
func TestMouse_LibraryRowClickLeavesTheFilter(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.key("esc", "2", "/")
	h.typeText("bunny")
	h.click("Big.Buck.Bunny")
	if h.m.lib.input.Focused() || h.m.lib.query != "bunny" {
		t.Fatalf("focused %v query %q", h.m.lib.input.Focused(), h.m.lib.query)
	}
	h.key("d")
	if len(h.dl.added) == 0 {
		t.Fatal("d should download once the list has the keys")
	}
	// The rule under the box is part of it.
	_, y := h.screenPos("› bunny")
	h.send(tea.MouseClickMsg{X: 10, Y: y + 1, Button: tea.MouseLeft})
	if !h.m.lib.input.Focused() {
		t.Fatal("clicking the rule should focus the filter")
	}
}

func TestDownloads_NoColumnHeaderWhenAllFinished(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.dl.jobs = []aria2.Job{{GID: "1", Status: "complete", Name: "done.mkv", Total: 10, Done: 10, Path: "/d/done.mkv"}}
	h.key("esc", "3")
	h.run(h.m.fetchJobs())
	if v := ansi.Strip(h.view()); strings.Contains(v, "progress") {
		t.Fatalf("finished rows have no progress column:\n%s", v)
	}
	h.click("done.mkv")
	if h.m.dl.cursor != 0 {
		t.Fatalf("cursor %d", h.m.dl.cursor)
	}
}

// Colour is never the only difference between done and failed.
func TestStatus_LeadsWithAMark(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	lo := newLayout(100, 30)
	cases := []struct {
		set  func(*Model)
		mark string
	}{
		{func(m *Model) { m.setStatus("queued 2 files", false) }, "✓ queued 2 files"},
		{func(m *Model) { m.setStatus("add failed", true) }, "✕ add failed"},
		{func(m *Model) { m.setBusy("refreshing") }, "… refreshing"},
	}
	for _, c := range cases {
		m := h.m
		c.set(&m)
		if got := ansi.Strip(m.statusLine(lo)); !strings.HasPrefix(got, c.mark) {
			t.Fatalf("status line %q, want it to start %q", got, c.mark)
		}
	}
}

func TestTruncateLeft_KeepsTheLeaf(t *testing.T) {
	if got := truncateLeft("~/Media/incoming/tori/september", 16); got != "…/tori/september" {
		t.Fatalf("got %q", got)
	}
	if got := truncateLeft("~/short", 16); got != "~/short" {
		t.Fatalf("got %q", got)
	}
}

func TestDownloads_OneLinePerJobWhenWide(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.dl.jobs = []aria2.Job{
		{GID: "1", Status: "active", Name: "one.mkv", Total: 100 << 20, Done: 25 << 20, Speed: 5 << 20},
		{GID: "2", Status: "error", Name: "bad.mkv", ErrorMsg: "disk full"},
	}
	h.key("esc", "3")
	h.run(h.m.fetchJobs())
	v := ansi.Strip(h.view())
	for _, want := range []string{"1 active · 5.0 MiB/s · 15s", "speed", "25%", "bad.mkv", "disk full"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "one.mkv") != 1 || strings.Contains(v, "downloading") {
		t.Fatalf("wide layout should be one line per job:\n%s", v)
	}
	// Narrow terminals keep the two-line form, and a click on either line
	// still picks the job.
	h.send(tea.WindowSizeMsg{Width: 70, Height: 30})
	v = ansi.Strip(h.view())
	if !strings.Contains(v, "downloading") || strings.Contains(v, "speed") {
		t.Fatalf("narrow layout:\n%s", v)
	}
	h.click("disk full")
	if h.m.dl.cursor != 1 {
		t.Fatalf("cursor %d", h.m.dl.cursor)
	}
}

// Without an Omarchy theme the terminal's background picks the mode: the
// dark palette's pale text would be unreadable on a light terminal, since
// tori never paints a background of its own.
func TestTheme_LightTerminalGetsTheLightPalette(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	dark := h.m.st.pal.Hex["primary"]
	h.send(tea.BackgroundColorMsg{Color: color.Black})
	if got := h.m.st.pal.Hex["primary"]; got != dark {
		t.Fatalf("a dark answer changed primary to %s", got)
	}
	h.send(tea.BackgroundColorMsg{Color: color.White})
	light := h.m.st.pal.Hex["primary"]
	if light == dark {
		t.Fatal("a light terminal should switch to the light palette")
	}
	// Inputs copy their styles when made, so they must be restyled too.
	if h.m.search.input.Styles().Focused.Text.GetForeground() != h.m.st.pal.Primary {
		t.Fatal("the search box kept the dark palette")
	}
}

// An Omarchy theme says what the desktop is; the terminal is not asked.
func TestTheme_OmarchyModeIsNotOverridden(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "mode = \"dark\"\nbackground = \"#101010\"\nforeground = \"#F0F0F0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Config: config.Default(home), Keyring: secret.NewMemory(), Home: home,
		Getenv: func(string) string { return "" }, NewAPI: func(string) API { return &fakeAPI{} }})
	next, _ := m.Update(tea.BackgroundColorMsg{Color: color.White})
	if got := next.(Model).st.pal.Hex["primary"]; got != "#F0F0F0" {
		t.Fatalf("the Omarchy theme was overridden: primary %s", got)
	}
}
