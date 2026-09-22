// Command genshots renders a few TUI frames with fake sample data and prints
// ANSI to stdout. Used to build README screenshots (no live API / no secrets).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/tori/internal/aria2"
	"github.com/gaius-codius/tori/internal/config"
	"github.com/gaius-codius/tori/internal/secret"
	"github.com/gaius-codius/tori/internal/tui"
	"github.com/gaius-codius/tori/internal/torbox"
)

type fakeAPI struct {
	results []torbox.Result
	items   []torbox.Item
}

func (f *fakeAPI) Me(context.Context) (torbox.User, error) {
	return torbox.User{Plan: 2}, nil
}
func (f *fakeAPI) Search(context.Context, string, torbox.SearchOptions) ([]torbox.Result, error) {
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
func (f *fakeAPI) AddMagnet(context.Context, string, bool) (torbox.AddResult, error) {
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) AddWeb(context.Context, string) (torbox.AddResult, error) {
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) AddUsenet(context.Context, string, string) (torbox.AddResult, error) {
	return torbox.AddResult{}, nil
}
func (f *fakeAPI) Delete(context.Context, torbox.Item) error     { return nil }
func (f *fakeAPI) Reannounce(context.Context, torbox.Item) error { return nil }
func (f *fakeAPI) DownloadURL(context.Context, torbox.Item, int64) (string, error) {
	return "https://cdn.example/f", nil
}

type fakeDL struct{ jobs []aria2.Job }

func (d *fakeDL) Add(context.Context, string, string, string) (string, error) { return "gid", nil }
func (d *fakeDL) List(context.Context) ([]aria2.Job, error)                   { return d.jobs, nil }
func (d *fakeDL) Pause(context.Context, string) error                         { return nil }
func (d *fakeDL) Resume(context.Context, string) error                        { return nil }
func (d *fakeDL) Remove(context.Context, aria2.Job) error                     { return nil }
func (d *fakeDL) ClearFinished(context.Context) error                         { return nil }

func sampleItems() []torbox.Item {
	return []torbox.Item{
		{Kind: torbox.KindTorrent, ID: 1, Name: "Big.Buck.Bunny.2008.1080p.BluRay.x264", Size: 3 << 30,
			DownloadFinished: true, DownloadPresent: true, CreatedAt: "2026-09-19",
			Files: []torbox.File{{ID: 0, ShortName: "bbb.mkv", Size: 3 << 30}, {ID: 1, ShortName: "bbb.srt", Size: 40 << 10}}},
		{Kind: torbox.KindWeb, ID: 2, Name: "ubuntu-26.04-desktop-amd64.iso", Size: 6 << 30,
			State: "downloading", Progress: 0.42, Speed: 12 << 20, CreatedAt: "2026-09-20"},
	}
}

func sampleResults() []torbox.Result {
	return []torbox.Result{
		{Hash: "bbb", RawTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-DEMO", Magnet: "magnet:?xt=urn:btih:bbb",
			Seeders: 420, Size: 3 << 30, Cached: true, Age: "2d", Files: 2},
		{Hash: "ubu", RawTitle: "ubuntu-26.04-desktop-amd64.iso", Magnet: "magnet:?xt=urn:btih:ubu",
			Seeders: 88, Size: 6 << 30, Cached: false, Age: "5h", Files: 1},
		{Hash: "sin", RawTitle: "Sintel.2010.1080p.BluRay.x264", Magnet: "magnet:?xt=urn:btih:sin",
			Seeders: 61, Size: 2 << 30, Cached: true, Age: "1w", Files: 1},
	}
}

func sampleJobs() []aria2.Job {
	return []aria2.Job{
		{GID: "1", Name: "bbb.mkv", Status: "active", Total: 3 << 30, Done: (3 << 30) * 7 / 10, Speed: 8 << 20},
		{GID: "2", Name: "ubuntu-26.04-desktop-amd64.iso", Status: "paused", Total: 6 << 30, Done: (6 << 30) * 15 / 100, Speed: 0},
	}
}

type harness struct {
	m   tui.Model
	api *fakeAPI
	dl  *fakeDL
}

func newHarness() *harness {
	api := &fakeAPI{items: sampleItems(), results: sampleResults()}
	dl := &fakeDL{jobs: sampleJobs()}
	home, _ := os.UserHomeDir()
	cfg := config.Default(home)
	h := &harness{api: api, dl: dl}
	h.m = tui.New(tui.Options{
		Config:     cfg,
		Keyring:    secret.NewMemory(),
		Getenv:     func(k string) string { if k == secret.EnvKey { return "demo-key" }; return "" },
		Downloader: dl,
		NewAPI:     func(string) tui.API { return api },
		Home:       home,
		Clipboard:  func(string) error { return nil },
	})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 28})
	h.run(h.m.Init())
	return h
}

func (h *harness) send(msg tea.Msg) {
	next, cmd := h.m.Update(msg)
	h.m = next.(tui.Model)
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
	case <-time.After(50 * time.Millisecond):
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
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "?":
			msg = tea.KeyPressMsg{Code: '?', Text: "?"}
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

func main() {
	_ = os.Setenv("COLORTERM", "truecolor")
	_ = os.Setenv("TERM", "xterm-256color")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genshots <search|library|downloads|help>")
		os.Exit(2)
	}
	h := newHarness()
	switch os.Args[1] {
	case "search":
		h.key("esc")
		h.key("/")
		h.typeText("bunny")
		h.key("enter")
	case "library":
		h.key("esc", "2")
	case "downloads":
		h.key("esc", "3")
	case "help":
		h.key("esc", "?")
	default:
		fmt.Fprintln(os.Stderr, "unknown shot:", os.Args[1])
		os.Exit(2)
	}
	// View() wraps AltScreen; print the raw frame freeze will capture.
	fmt.Print(h.m.View().Content)
}
