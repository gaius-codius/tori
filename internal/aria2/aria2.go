// Package aria2 runs a private aria2c process and drives it over JSON-RPC.
// Tori owns the process: it starts with the TUI and stops when the TUI quits.
// Unfinished downloads are kept in a session file and resume on next start.
package aria2

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type Options struct {
	Binary      string   // aria2c path or name on PATH
	Dir         string   // default download directory
	SessionFile string   // unfinished downloads survive restarts here
	ExtraArgs   []string // appended verbatim
}

type Daemon struct {
	cmd    *exec.Cmd
	url    string
	token  string
	client *http.Client
	done   chan struct{}
}

// Start launches aria2c on a free loopback port and waits until RPC answers.
func Start(ctx context.Context, opt Options) (*Daemon, error) {
	bin := opt.Binary
	if bin == "" {
		bin = "aria2c"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("aria2c not found (%s): install aria2 or set downloader in config", bin)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	secret, err := randomToken()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("download dir: %w", err)
	}
	args := []string{
		"--enable-rpc",
		"--rpc-listen-all=false",
		"--rpc-listen-port=" + strconv.Itoa(port),
		"--dir=" + opt.Dir,
		"--continue=true",
		"--max-concurrent-downloads=3",
		"--max-connection-per-server=4",
		"--split=4",
		"--auto-file-renaming=false",
		"--console-log-level=error",
		"--quiet=true",
		"--stop-with-process=" + strconv.Itoa(os.Getpid()),
	}
	if opt.SessionFile != "" {
		if err := os.MkdirAll(filepath.Dir(opt.SessionFile), 0o700); err != nil {
			return nil, fmt.Errorf("session dir: %w", err)
		}
		args = append(args, "--save-session="+opt.SessionFile, "--save-session-interval=15")
		if _, err := os.Stat(opt.SessionFile); err == nil {
			args = append(args, "--input-file="+opt.SessionFile)
		}
	}
	// The RPC secret goes in a private conf file, not argv, where any local
	// user could read it from ps.
	conf, err := os.CreateTemp("", "tori-aria2-*.conf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(conf.Name())
	if _, err := fmt.Fprintf(conf, "rpc-secret=%s\n", secret); err != nil {
		conf.Close()
		return nil, err
	}
	if err := conf.Close(); err != nil {
		return nil, err
	}
	args = append(args, "--conf-path="+conf.Name())
	args = append(args, opt.ExtraArgs...)

	cmd := exec.Command(path, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start aria2c: %w", err)
	}
	d := &Daemon{
		cmd:    cmd,
		url:    fmt.Sprintf("http://127.0.0.1:%d/jsonrpc", port),
		token:  "token:" + secret,
		client: &http.Client{Timeout: 5 * time.Second},
		done:   make(chan struct{}),
	}
	go func() { _ = cmd.Wait(); close(d.done) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var v struct {
			Version string `json:"version"`
		}
		if err := d.call(ctx, "aria2.getVersion", &v); err == nil {
			return d, nil
		}
		select {
		case <-d.done:
			return nil, fmt.Errorf("aria2c exited: %s", bytes.TrimSpace(stderr.Bytes()))
		case <-ctx.Done():
			d.kill()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			d.kill()
			return nil, errors.New("aria2c did not answer RPC within 5s")
		}
	}
}

// Stop saves the session and shuts aria2c down, killing it if it lingers.
func (d *Daemon) Stop() {
	if d == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = d.call(ctx, "aria2.saveSession", nil)
	_ = d.call(ctx, "aria2.shutdown", nil)
	select {
	case <-d.done:
	case <-time.After(3 * time.Second):
		d.kill()
	}
}

func (d *Daemon) kill() {
	if d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
}

// Job is one aria2 download.
type Job struct {
	GID       string
	Status    string // active, waiting, paused, error, complete, removed
	Name      string
	Path      string
	Total     int64
	Done      int64
	Speed     int64
	ErrorCode string
	ErrorMsg  string
}

func (j Job) Progress() float64 {
	if j.Total <= 0 {
		return 0
	}
	return float64(j.Done) / float64(j.Total)
}

// ETA is zero when unknown.
func (j Job) ETA() time.Duration {
	if j.Speed <= 0 || j.Total <= j.Done {
		return 0
	}
	return time.Duration((j.Total-j.Done)/j.Speed) * time.Second
}

// Add queues a URL. name overrides the saved file name when set; subdir is
// joined onto the default download dir.
func (d *Daemon) Add(ctx context.Context, url, name, subdir string) (string, error) {
	opts := map[string]string{}
	if name != "" {
		opts["out"] = name
	}
	if subdir != "" {
		var g struct {
			Dir string `json:"dir"`
		}
		if err := d.call(ctx, "aria2.getGlobalOption", &g); err != nil {
			return "", err
		}
		opts["dir"] = filepath.Join(g.Dir, subdir)
	}
	var gid string
	err := d.call(ctx, "aria2.addUri", &gid, []string{url}, opts)
	return gid, err
}

func (d *Daemon) Pause(ctx context.Context, gid string) error {
	return d.call(ctx, "aria2.pause", nil, gid)
}

func (d *Daemon) Resume(ctx context.Context, gid string) error {
	return d.call(ctx, "aria2.unpause", nil, gid)
}

// Remove stops an active job, or clears a finished one from the list.
func (d *Daemon) Remove(ctx context.Context, j Job) error {
	switch j.Status {
	case "active", "waiting", "paused":
		if err := d.call(ctx, "aria2.remove", nil, j.GID); err != nil {
			return err
		}
	}
	// Removing an active job leaves a result behind; clear it too. Errors are
	// expected if aria2 has not finished removing yet.
	_ = d.call(ctx, "aria2.removeDownloadResult", nil, j.GID)
	return nil
}

// ClearFinished drops complete, errored and removed jobs from the list.
func (d *Daemon) ClearFinished(ctx context.Context) error {
	return d.call(ctx, "aria2.purgeDownloadResult", nil)
}

var statusKeys = []string{"gid", "status", "totalLength", "completedLength", "downloadSpeed", "errorCode", "errorMessage", "files"}

// List returns active, waiting and recently stopped jobs, in that order.
func (d *Daemon) List(ctx context.Context) ([]Job, error) {
	var active, waiting, stopped []rawStatus
	if err := d.call(ctx, "aria2.tellActive", &active, statusKeys); err != nil {
		return nil, err
	}
	if err := d.call(ctx, "aria2.tellWaiting", &waiting, 0, 200, statusKeys); err != nil {
		return nil, err
	}
	if err := d.call(ctx, "aria2.tellStopped", &stopped, 0, 200, statusKeys); err != nil {
		return nil, err
	}
	var out []Job
	for _, group := range [][]rawStatus{active, waiting, stopped} {
		for _, r := range group {
			out = append(out, r.job())
		}
	}
	return out, nil
}

type rawStatus struct {
	GID             string `json:"gid"`
	Status          string `json:"status"`
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	ErrorCode       string `json:"errorCode"`
	ErrorMessage    string `json:"errorMessage"`
	Files           []struct {
		Path string `json:"path"`
		URIs []struct {
			URI string `json:"uri"`
		} `json:"uris"`
	} `json:"files"`
}

func (r rawStatus) job() Job {
	j := Job{
		GID:       r.GID,
		Status:    r.Status,
		Total:     atoi(r.TotalLength),
		Done:      atoi(r.CompletedLength),
		Speed:     atoi(r.DownloadSpeed),
		ErrorCode: r.ErrorCode,
		ErrorMsg:  r.ErrorMessage,
	}
	if len(r.Files) > 0 {
		f := r.Files[0]
		j.Path = f.Path
		j.Name = filepath.Base(f.Path)
		if f.Path == "" && len(f.URIs) > 0 {
			j.Name = f.URIs[0].URI
		}
	}
	if j.Name == "" || j.Name == "." {
		j.Name = r.GID
	}
	return j
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (d *Daemon) call(ctx context.Context, method string, out any, params ...any) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      "tori",
		Method:  method,
		Params:  append([]any{d.token}, params...),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s: %s", method, r.Error.Message)
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
