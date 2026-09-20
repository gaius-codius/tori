// Package config loads ~/.config/tori/config.toml. The file is optional;
// every field has a default. The API key never lives here (see secret).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	appName    = "tori"
	configFile = "config.toml"
	envConfig  = "TORI_CONFIG"
)

type Config struct {
	// DownloadDir is where aria2c saves files. "~" expands to $HOME.
	DownloadDir string `toml:"download_dir"`
	// Downloader is the aria2c binary; empty means look it up on PATH.
	Downloader string `toml:"downloader"`
	// DownloaderArgs are appended to the aria2c argv.
	DownloaderArgs []string `toml:"downloader_args"`
	// CachedOnly starts search with the cached-only filter on.
	CachedOnly bool `toml:"cached_only"`
	// Refresh is the library poll interval, e.g. "5s".
	Refresh string `toml:"refresh"`
	// SearchURL overrides the TorBox Search API base URL.
	SearchURL string `toml:"search_url"`
	// Mouse enables clicking and wheel scrolling (default true). With it on,
	// most terminals need Shift+drag to select text.
	Mouse *bool `toml:"mouse"`
	// APIURL overrides the TorBox main API base URL (proxies, testing).
	APIURL string `toml:"api_url"`

	path string
}

func Default(home string) Config {
	return Config{
		DownloadDir: filepath.Join(home, "Downloads"),
		Downloader:  "aria2c",
		CachedOnly:  true,
		Refresh:     "5s",
	}
}

// Path resolves TORI_CONFIG, then $XDG_CONFIG_HOME/tori, then ~/.config/tori.
func Path(getenv func(string) string, home string) string {
	if p := getenv(envConfig); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, appName, configFile)
	}
	return filepath.Join(home, ".config", appName, configFile)
}

// Load returns defaults when the file does not exist.
func Load(path, home string) (Config, error) {
	cfg := Default(home)
	cfg.path = path
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	cfg.path = path
	cfg.DownloadDir = expandHome(cfg.DownloadDir, home)
	if _, err := time.ParseDuration(cfg.Refresh); err != nil {
		return cfg, fmt.Errorf("%s: refresh: %w", path, err)
	}
	return cfg, nil
}

func (c Config) Path() string { return c.path }

// MouseEnabled defaults to true when the key is absent.
func (c Config) MouseEnabled() bool { return c.Mouse == nil || *c.Mouse }

// RefreshInterval never returns less than two seconds, to stay well inside
// TorBox's rate limits.
func (c Config) RefreshInterval() time.Duration {
	d, err := time.ParseDuration(c.Refresh)
	if err != nil || d < 2*time.Second {
		return 5 * time.Second
	}
	return d
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
