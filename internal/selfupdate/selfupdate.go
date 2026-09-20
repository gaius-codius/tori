// Package selfupdate replaces the running binary with the latest GitHub
// release, verified against the release's SHA256SUMS.txt.
package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const Repo = "gaius-codius/tori"

type Updater struct {
	Repo    string
	APIBase string // https://api.github.com
	HTTP    *http.Client
}

func New() *Updater {
	return &Updater{Repo: Repo, APIBase: "https://api.github.com", HTTP: http.DefaultClient}
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// AssetName is the release binary for this platform, e.g. tori-linux-amd64.
func AssetName() string { return fmt.Sprintf("tori-%s-%s", runtime.GOOS, runtime.GOARCH) }

// Latest returns the newest release tag.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	r, err := u.latest(ctx)
	if err != nil {
		return "", err
	}
	return r.TagName, nil
}

func (u *Updater) latest(ctx context.Context) (release, error) {
	var r release
	url := fmt.Sprintf("%s/repos/%s/releases/latest", u.APIBase, u.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return r, errors.New("no releases published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("GitHub: %s", resp.Status)
	}
	return r, json.NewDecoder(resp.Body).Decode(&r)
}

// Update installs the latest release over exe unless current already matches.
// It returns the tag it installed, or "" if already up to date.
func (u *Updater) Update(ctx context.Context, current, exe string) (string, error) {
	r, err := u.latest(ctx)
	if err != nil {
		return "", err
	}
	if r.TagName == current {
		return "", nil
	}
	want := AssetName()
	var binURL, sumsURL string
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			binURL = a.URL
		case "SHA256SUMS.txt":
			sumsURL = a.URL
		}
	}
	if binURL == "" {
		return "", fmt.Errorf("release %s has no %s build", r.TagName, want)
	}
	if sumsURL == "" {
		return "", fmt.Errorf("release %s has no SHA256SUMS.txt", r.TagName)
	}
	sums, err := u.fetch(ctx, sumsURL, 1<<20)
	if err != nil {
		return "", fmt.Errorf("checksums: %w", err)
	}
	wantSum, err := findSum(sums, want)
	if err != nil {
		return "", err
	}

	// Write next to the binary so the final rename stays on one filesystem.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".tori-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	if err := u.download(ctx, binURL, tmp, wantSum); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return "", err
	}
	return r.TagName, nil
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (u *Updater) download(ctx context.Context, url string, dst io.Writer, wantSum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(resp.Body, 200<<20)); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, wantSum)
	}
	return nil
}

// findSum reads sha256sum output ("<hex>  <name>" or "<hex> *<name>").
func findSum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS.txt has no entry for %s", name)
}
