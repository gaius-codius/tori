package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGitHub(t *testing.T, tag string, bin []byte, sum string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":"SHA256SUMS.txt","browser_download_url":"%s/sums"}]}`,
				tag, AssetName(), srv.URL, srv.URL)
		case "/bin":
			_, _ = w.Write(bin)
		case "/sums":
			fmt.Fprintf(w, "%s  %s\nffff  tori-other-arch\n", sum, AssetName())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func setup(t *testing.T) string {
	exe := filepath.Join(t.TempDir(), "tori")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestUpdate_ReplacesBinary(t *testing.T) {
	bin := []byte("new binary")
	srv := fakeGitHub(t, "v0.2.0", bin, sha(bin))
	exe := setup(t)
	u := &Updater{Repo: "o/r", APIBase: srv.URL, HTTP: srv.Client()}
	tag, err := u.Update(context.Background(), "v0.1.0", exe)
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("tag %q err %v", tag, err)
	}
	got, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	if string(got) != "new binary" || st.Mode().Perm() != 0o755 {
		t.Fatalf("got %q mode %v", got, st.Mode())
	}
}

func TestUpdate_UpToDate(t *testing.T) {
	srv := fakeGitHub(t, "v0.1.0", []byte("x"), sha([]byte("x")))
	exe := setup(t)
	u := &Updater{Repo: "o/r", APIBase: srv.URL, HTTP: srv.Client()}
	tag, err := u.Update(context.Background(), "v0.1.0", exe)
	if err != nil || tag != "" {
		t.Fatalf("tag %q err %v", tag, err)
	}
}

func TestUpdate_BadChecksumKeepsOldBinary(t *testing.T) {
	srv := fakeGitHub(t, "v0.2.0", []byte("tampered"), sha([]byte("original")))
	exe := setup(t)
	u := &Updater{Repo: "o/r", APIBase: srv.URL, HTTP: srv.Client()}
	_, err := u.Update(context.Background(), "v0.1.0", exe)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatalf("binary replaced: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".tori-update-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}
