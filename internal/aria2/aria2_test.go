package aria2

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemon_DownloadsAndReports(t *testing.T) {
	if _, err := exec.LookPath("aria2c"); err != nil {
		t.Skip("aria2c not installed")
	}
	payload := bytes.Repeat([]byte("tori"), 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "file.bin", time.Time{}, bytes.NewReader(payload))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, err := Start(ctx, Options{Dir: dir, SessionFile: filepath.Join(dir, "state", "session")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	gid, err := d.Add(ctx, srv.URL+"/file.bin", "renamed.bin", "sub")
	if err != nil {
		t.Fatal(err)
	}
	var last Job
	for {
		jobs, err := d.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			if j.GID == gid {
				last = j
			}
		}
		if last.Status == "complete" || last.Status == "error" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out, last status %+v", last)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if last.Status != "complete" {
		t.Fatalf("status %s: %s", last.Status, last.ErrorMsg)
	}
	if last.Name != "renamed.bin" || last.Total != int64(len(payload)) || last.Progress() != 1 {
		t.Fatalf("job %+v", last)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sub", "renamed.bin"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("file mismatch: %v", err)
	}
	if err := d.Remove(ctx, last); err != nil {
		t.Fatal(err)
	}
}

func TestStart_MissingBinary(t *testing.T) {
	_, err := Start(context.Background(), Options{Binary: "definitely-not-aria2c", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
}
