package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gaius-codius/tori/internal/aria2"
	"github.com/gaius-codius/tori/internal/config"
	"github.com/gaius-codius/tori/internal/secret"
	"github.com/gaius-codius/tori/internal/selfupdate"
	"github.com/gaius-codius/tori/internal/torbox"
	"github.com/gaius-codius/tori/internal/tui"
)

const helpText = `Usage: tori [command]

Tori is a terminal UI for TorBox: search, manage your library, and
download files with aria2c.

With no command, tori starts the TUI.

Commands:
  add <link>     Add a magnet, info hash, NZB URL or web link to TorBox
  update         Update tori to the latest release
  forget-key     Remove the API key from the keyring
  version        Print the version

Options:
  -h, --help     Show this help

The API key comes from TORBOX_API_KEY, else the keyring (set it in the TUI).
Config: TORI_CONFIG, else $XDG_CONFIG_HOME/tori/config.toml.
`

// version is stamped by release builds: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) > 0 && (args[0] == "version" || args[0] == "--version" || args[0] == "-v"):
		fmt.Fprintln(stdout, "tori "+currentVersion())
		return 0
	case len(args) > 0 && args[0] == "update":
		return runUpdate(stdout, stderr)
	}
	home, _ := os.UserHomeDir()
	cfg, err := config.Load(config.Path(os.Getenv, home), home)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(args) == 0 {
		if err := startTUI(cfg, home); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprint(stdout, helpText)
		return 0
	case "add":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: tori add <link>")
			return 2
		}
		return runAdd(args[1], cfg, stdout, stderr)
	case "forget-key":
		if err := secret.NewDBus().Delete(); err != nil && !errors.Is(err, secret.ErrNotFound) {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "API key removed from the keyring")
		return 0
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "unknown flag: %s\n", args[0])
		} else {
			fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		}
		return 2
	}
}

func newClient(cfg config.Config, key string) *torbox.Client {
	c := torbox.New(key)
	if cfg.APIURL != "" {
		c.BaseURL = strings.TrimRight(cfg.APIURL, "/")
	}
	if cfg.SearchURL != "" {
		c.SearchURL = strings.TrimRight(cfg.SearchURL, "/")
	}
	return c
}

func startTUI(cfg config.Config, home string) error {
	opt := tui.Options{
		Config:    cfg,
		Keyring:   secret.NewDBus(),
		Home:      home,
		Clipboard: clipboard.WriteAll,
		NewAPI:    func(key string) tui.API { return newClient(cfg, key) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	d, err := aria2.Start(ctx, aria2.Options{
		Binary:      cfg.Downloader,
		Dir:         cfg.DownloadDir,
		SessionFile: filepath.Join(stateDir(home), "aria2.session"),
		ExtraArgs:   cfg.DownloaderArgs,
	})
	cancel()
	if err != nil {
		opt.DLErr = err
	} else {
		opt.Downloader = d
		defer d.Stop()
	}
	return tui.Run(opt)
}

func stateDir(home string) string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "tori")
	}
	return filepath.Join(home, ".local", "state", "tori")
}

func runAdd(link string, cfg config.Config, stdout, stderr io.Writer) int {
	key, _, err := secret.Resolve(os.Getenv, secret.NewDBus())
	if err != nil || key.Empty() {
		fmt.Fprintln(stderr, "no API key: run tori once to set it, or export "+secret.EnvKey)
		return 2
	}
	c := newClient(cfg, key.Reveal())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	kind := tui.ClassifyLink(link)
	switch kind {
	case "torrent":
		if !strings.HasPrefix(strings.ToLower(link), "magnet:") {
			link = "magnet:?xt=urn:btih:" + link
		}
		_, err = c.AddMagnet(ctx, link, false)
	case "usenet":
		_, err = c.AddUsenet(ctx, link, "")
	default:
		_, err = c.AddWeb(ctx, link)
	}
	if err != nil {
		fmt.Fprintf(stderr, "add %s: %v\n", kind, err)
		return 1
	}
	fmt.Fprintf(stdout, "added %s\n", kind)
	return 0
}

// currentVersion prefers the stamped version, then the module version that
// go install records.
func currentVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func runUpdate(stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(stderr, "cannot find the tori binary:", err)
		return 1
	}
	cur := currentVersion()
	if cur == "dev" {
		fmt.Fprintln(stderr, "this is a development build; update it with `make install` from the source tree")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fmt.Fprintf(stdout, "tori %s: checking for updates…\n", cur)
	tag, err := selfupdate.New().Update(ctx, cur, exe)
	if err != nil {
		fmt.Fprintln(stderr, "update failed:", err)
		return 1
	}
	if tag == "" {
		fmt.Fprintln(stdout, "already up to date")
		return 0
	}
	fmt.Fprintf(stdout, "updated %s → %s (%s)\n", cur, tag, exe)
	return 0
}
