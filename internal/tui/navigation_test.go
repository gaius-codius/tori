package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/gaius-codius/tori/internal/secret"
	"github.com/gaius-codius/tori/internal/torbox"
)

func TestTabNavigation(t *testing.T) {
	for _, results := range []bool{false, true} {
		name := "empty"
		if results {
			name = "results"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, map[string]string{secret.EnvKey: "k"})
			h.typeText("a query")
			if results {
				h.m.search.results = []torbox.Result{{RawTitle: "result"}}
			}
			stops := []searchControl{searchCached, searchUsenet, searchSort}
			if results {
				stops = append(stops, searchResults)
			}
			stops = append(stops, searchQuery)
			for _, want := range stops {
				h.key("tab")
				if h.m.tab != viewSearch || h.m.search.control != want || h.m.search.input.Focused() != (want == searchQuery) {
					t.Fatalf("tab: control %v, want %v; tab %v, input %v", h.m.search.control, want, h.m.tab, h.m.search.input.Focused())
				}
			}
			for i := len(stops) - 2; i >= 0; i-- {
				h.key("shift+tab")
				if h.m.search.control != stops[i] {
					t.Fatalf("shift+tab: control %v, want %v", h.m.search.control, stops[i])
				}
			}
			if h.m.search.input.Value() != "a query" {
				t.Fatal("focus navigation changed the query")
			}
		})
	}
}

func TestSearchFocusedFilters(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.api.results = []torbox.Result{{RawTitle: "result", Magnet: "magnet:?xt=urn:btih:a"}}
	h.typeText("query")
	h.key("enter", "tab", "tab")
	if v := ansi.Strip(h.view()); !strings.Contains(v, "▸c [x] cached only") {
		t.Fatalf("missing focus marker:\n%s", v)
	}
	h.key("space")
	if h.m.search.cachedOnly || h.m.search.control != searchCached || len(h.api.searched) != 2 {
		t.Fatal("space should toggle cached and rerun while retaining focus")
	}
	h.key("tab", "enter")
	if !h.m.search.usenet || h.m.search.control != searchUsenet || len(h.api.added) != 0 {
		t.Fatal("enter should toggle usenet, without adding a result")
	}
	h.key("tab", "space")
	if h.m.search.sort != sortSize {
		t.Fatal("space should cycle sort")
	}
	h.key("esc")
	if !h.m.search.input.Focused() || h.m.search.input.Value() != "query" {
		t.Fatal("escape should return to query without clearing it")
	}
}

func TestCtrlTabWhileTyping(t *testing.T) {
	h := newHarness(t, map[string]string{secret.EnvKey: "k"})
	h.m.search.results = []torbox.Result{{RawTitle: "existing result"}}
	h.typeText("query")
	h.key("ctrl+tab", "/")
	h.typeText("bunny")
	h.key("ctrl+tab")
	if h.m.tab != viewDownloads {
		t.Fatal("ctrl+tab should work from library input")
	}
	h.key("ctrl+tab")
	if h.m.tab != viewSearch || !h.m.search.input.Focused() || h.m.search.input.Value() != "query" {
		t.Fatal("ctrl+tab should wrap and retain query")
	}
	h.key("ctrl+shift+tab", "ctrl+shift+tab")
	if h.m.tab != viewLibrary || h.m.lib.query != "bunny" {
		t.Fatal("reverse cycling should wrap and retain library filter")
	}
	h.key("esc", "tab", "shift+tab")
	if h.m.tab != viewLibrary || h.m.lib.input.Focused() {
		t.Fatal("tab and shift+tab should cycle library focus")
	}
	h.key("/", "ctrl+u")
	if h.m.lib.query != "" || h.m.lib.input.Value() != "" {
		t.Fatal("ctrl+u should clear the query while typing")
	}
}

func TestSearchMouseFilterFocus(t *testing.T) {
	for _, width := range []int{50, 100} {
		h := newHarness(t, map[string]string{secret.EnvKey: "k"})
		h.send(tea.WindowSizeMsg{Width: width, Height: 30})
		h.click("u [ ] usenet")
		if !h.m.search.usenet || h.m.search.control != searchUsenet {
			t.Fatal("click should toggle and focus usenet")
		}
		h.click("s seeders")
		if h.m.search.sort != sortSize || h.m.search.control != searchSort {
			t.Fatal("click should cycle and focus sort")
		}
	}
}
