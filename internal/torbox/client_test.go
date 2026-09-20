package torbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("sekrit")
	c.BaseURL = srv.URL
	c.SearchURL = srv.URL
	return c
}

func writeEnv(w http.ResponseWriter, status int, errCode any, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": errCode == nil, "error": errCode, "detail": "d", "data": data,
	})
}

func TestList_DecodesAndTagsKind(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webdl/mylist" || r.URL.Query().Get("bypass_cache") != "true" {
			t.Errorf("path %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer sekrit" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"success":true,"error":null,"detail":"ok","data":[
			{"id":7,"name":"a.iso","size":"1024","progress":0.5,"download_state":"downloading",
			 "download_finished":false,"download_present":false,"files":[{"id":0,"name":"x/a.iso","short_name":"a.iso","size":1024}]}]}`)
	})
	items, err := c.List(context.Background(), KindWeb)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != KindWeb || items[0].Size != 1024 || items[0].Percent() != 50 {
		t.Fatalf("%+v", items)
	}
	if items[0].Files[0].DisplayName() != "a.iso" {
		t.Fatalf("%+v", items[0].Files)
	}
}

func TestDo_ErrorShapes(t *testing.T) {
	cases := []struct {
		name     string
		h        http.HandlerFunc
		auth     bool
		contains string
	}{
		{"bad token", func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 403, "BAD_TOKEN", nil) }, true, "BAD_TOKEN"},
		{"fastapi 401", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"detail":"Not authenticated"}`)
		}, true, "Not authenticated"},
		{"success with error code", func(w http.ResponseWriter, r *http.Request) { writeEnv(w, 200, "ITEM_NOT_FOUND", nil) }, false, "ITEM_NOT_FOUND"},
		{"422 list", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"detail":[{"loc":["q"],"msg":"field required","type":"x"}]}`)
		}, false, "field required"},
		{"html 502", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(502)
			_, _ = io.WriteString(w, "<html>bad gateway</html>")
		}, false, "bad gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, tc.h)
			_, err := c.Me(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("err %v", err)
			}
			if IsAuth(err) != tc.auth {
				t.Fatalf("IsAuth=%v", IsAuth(err))
			}
		})
	}
}

func TestDownloadURL_ParamsPerKind(t *testing.T) {
	var got []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+"?"+r.URL.RawQuery)
		writeEnv(w, 200, nil, "https://cdn/x")
	})
	ctx := context.Background()
	if _, err := c.DownloadURL(ctx, Item{Kind: KindTorrent, ID: 1}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadURL(ctx, Item{Kind: KindWeb, ID: 2}, -1); err != nil {
		t.Fatal(err)
	}
	if got[0] != "/torrents/requestdl?file_id=3&token=sekrit&torrent_id=1" {
		t.Fatal(got[0])
	}
	if got[1] != "/webdl/requestdl?token=sekrit&web_id=2&zip_link=true" {
		t.Fatal(got[1])
	}
}

func TestDownloadURL_TransportErrorRedactsKey(t *testing.T) {
	c := New("sekrit")
	c.BaseURL = "http://127.0.0.1:1"
	_, err := c.DownloadURL(context.Background(), Item{Kind: KindTorrent, ID: 1}, 0)
	if err == nil || strings.Contains(err.Error(), "sekrit") {
		t.Fatalf("err %v", err)
	}
}

func TestControl_UsesPerKindIDField(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webdl/controlwebdownload" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeEnv(w, 200, nil, nil)
	})
	if err := c.Delete(context.Background(), Item{Kind: KindWeb, ID: 9}); err != nil {
		t.Fatal(err)
	}
	if body["webdl_id"] != float64(9) || body["operation"] != "delete" {
		t.Fatalf("%v", body)
	}
}

func TestSearch_IMDbAndText(t *testing.T) {
	var paths []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"success":true,"error":null,"detail":"","data":{"torrents":[
			{"hash":"abc","raw_title":"Big.Buck.Bunny.1080p","title":"Big Buck Bunny","magnet":"magnet:?xt=urn:btih:abc",
			 "last_known_seeders":12,"size":"123456","files":1,"type":"torrent","age":"3d","cached":true}],"nzbs":null}}`)
	})
	ctx := context.Background()
	res, err := c.Search(ctx, "tt0137523", SearchOptions{CachedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Name() != "Big.Buck.Bunny.1080p" || res[0].Size != 123456 || !res[0].Cached {
		t.Fatalf("%+v", res)
	}
	if _, err := c.Search(ctx, "big buck/bunny", SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(paths[0], "/torrents/imdb:tt0137523?") || !strings.Contains(paths[0], "cached_only=true") {
		t.Fatal(paths[0])
	}
	if !strings.HasPrefix(paths[1], "/torrents/search/big%20buck%2Fbunny?") || strings.Contains(paths[1], "cached_only") {
		t.Fatal(paths[1])
	}
}

func TestSearch_UnreachableIsExplained(t *testing.T) {
	c := New("k")
	c.SearchURL = "http://no-such-host.invalid"
	_, err := c.Search(context.Background(), "x", SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "TorBox search is unreachable") {
		t.Fatalf("err %v", err)
	}
}
