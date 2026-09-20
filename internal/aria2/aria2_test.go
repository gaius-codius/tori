package aria2

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Clearing a finished job from the list must never delete what it downloaded.
	if _, err := os.Stat(filepath.Join(dir, "sub", "renamed.bin")); err != nil {
		t.Fatalf("Remove deleted a completed download: %v", err)
	}
}

// A cancelled download must not poison the next attempt at the same name.
// aria2 runs with --continue, so a control file left behind makes the retry
// resume the dead job, ignore the fresh link and fail with "No URI
// available" — which is what happens when a TorBox link expires and the
// user re-queues the file.
func TestDaemon_CancelThenRequeueSameName(t *testing.T) {
	if _, err := exec.LookPath("aria2c"); err != nil {
		t.Skip("aria2c not installed")
	}
	const total = 4 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(total))
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32<<10)
		for sent := 0; sent < total; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond) // slow enough to cancel mid-flight
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	d, err := Start(ctx, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	started := func(j Job) bool { return j.Done > 0 }
	gid, err := d.Add(ctx, srv.URL+"/file.bin", "same.bin", "")
	if err != nil {
		t.Fatal(err)
	}
	running := waitJob(ctx, t, d, gid, started)
	if err := d.Remove(ctx, running); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "same.bin")
	for _, p := range []string{path, path + ".aria2"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the cancel (err %v)", filepath.Base(p), err)
		}
	}

	gid2, err := d.Add(ctx, srv.URL+"/file.bin", "same.bin", "")
	if err != nil {
		t.Fatal(err)
	}
	retry := waitJob(ctx, t, d, gid2, func(j Job) bool { return started(j) || j.Status == "error" })
	if retry.Status == "error" {
		t.Fatalf("re-queue failed: %s (%s)", retry.ErrorMsg, retry.ErrorCode)
	}
	if err := d.Remove(ctx, retry); err != nil {
		t.Fatal(err)
	}
}

// waitJob polls until ok reports the job is in the state the test wants.
func waitJob(ctx context.Context, t *testing.T, d *Daemon, gid string, ok func(Job) bool) Job {
	t.Helper()
	var last Job
	for {
		jobs, err := d.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range jobs {
			if j.GID == gid {
				last = j
				if ok(j) {
					return j
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s, last %+v", gid, last)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestStart_MissingBinary(t *testing.T) {
	_, err := Start(context.Background(), Options{Binary: "definitely-not-aria2c", Dir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
}
