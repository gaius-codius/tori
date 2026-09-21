package tui

import (
	"context"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/gaius-codius/tori/internal/aria2"
	"github.com/gaius-codius/tori/internal/config"
	"github.com/gaius-codius/tori/internal/secret"
	"github.com/gaius-codius/tori/internal/theme"
	"github.com/gaius-codius/tori/internal/torbox"
)

type view int

const (
	viewSearch view = iota
	viewLibrary
	viewDownloads
)

// overlay is a modal drawn instead of the current tab.
type overlay int

const (
	overlayNone overlay = iota
	overlayKey          // API key entry
	overlayHelp
	overlayAdd     // paste a magnet / link / NZB URL
	overlayFiles   // file picker for one library item
	overlayConfirm // delete confirmation
)

// Downloader is the part of aria2.Daemon the TUI uses.
type Downloader interface {
	Add(ctx context.Context, url, name, subdir string) (string, error)
	List(ctx context.Context) ([]aria2.Job, error)
	Pause(ctx context.Context, gid string) error
	Resume(ctx context.Context, gid string) error
	Remove(ctx context.Context, j aria2.Job) error
	ClearFinished(ctx context.Context) error
}

// API is the part of torbox.Client the TUI uses.
type API interface {
	Me(ctx context.Context) (torbox.User, error)
	Search(ctx context.Context, q string, opt torbox.SearchOptions) ([]torbox.Result, error)
	List(ctx context.Context, k torbox.Kind) ([]torbox.Item, error)
	AddMagnet(ctx context.Context, magnet string, onlyIfCached bool) (torbox.AddResult, error)
	AddWeb(ctx context.Context, link string) (torbox.AddResult, error)
	AddUsenet(ctx context.Context, link, name string) (torbox.AddResult, error)
	Delete(ctx context.Context, it torbox.Item) error
	Reannounce(ctx context.Context, it torbox.Item) error
	DownloadURL(ctx context.Context, it torbox.Item, fileID int64) (string, error)
}

type Options struct {
	Config     config.Config
	Keyring    secret.Store
	Getenv     func(string) string
	Downloader Downloader // nil disables downloads (aria2c missing)
	DLErr      error      // why Downloader is nil
	NewAPI     func(key string) API
	Home       string
	Clipboard  func(string) error
}

type Model struct {
	opt Options
	st  styles
	// askMode is set when no Omarchy theme says whether the desktop is
	// light or dark, so the terminal's background has to decide.
	askMode bool
	width   int
	height  int

	tab     view
	overlay overlay
	prevOv  overlay

	api       API
	user      *torbox.User
	keySource secret.Source
	keyInput  textinput.Model
	keyErr    string
	checking  bool

	status     string
	statusErr  bool
	statusBusy bool
	statusSeq  int

	search searchState
	lib    libraryState
	dl     downloadsState

	clicked lastClick

	addInput textinput.Model
	files    filesState
	confirm  torbox.Item
}

func New(opt Options) Model {
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.Home == "" {
		opt.Home, _ = os.UserHomeDir()
	}
	pal, rep := theme.Load(opt.Home)
	m := Model{opt: opt, width: 80, height: 24, askMode: rep.MissingFile || rep.InvalidTOML}
	m.st = m.stylesFor(pal)
	m.keyInput = m.newInput("paste API key", true)
	m.addInput = m.newInput("magnet:?xt=…  or  https://…", false)
	m.search = newSearchState(m.newInput("search torrents, or an IMDb id like tt0137523", false), opt.Config.CachedOnly)
	m.lib.input = m.newInput("filter by name", false)
	if opt.DLErr != nil {
		m.setStatus(opt.DLErr.Error(), true)
	}

	key, src, err := secret.Resolve(opt.Getenv, opt.Keyring)
	if err != nil || key.Empty() {
		m.overlay = overlayKey
		m.keyInput.Focus()
		if err != nil && !isNotFound(err) {
			m.keyErr = err.Error()
		}
		return m
	}
	m.keySource = src
	m.api = opt.NewAPI(key.Reveal())
	m.search.input.Focus()
	return m
}

func isNotFound(err error) bool { return err == secret.ErrNotFound }

// stylesFor builds the styles for pal.
func (m Model) stylesFor(pal theme.Palette) styles {
	if m.opt.Getenv("NO_COLOR") != "" {
		// Colour is stripped, so a background band would vanish with it.
		pal.NoBand = true
	}
	return newStyles(pal)
}

// restyle switches to pal after start-up. Inputs copy their styles when
// made, so each is styled again.
func (m *Model) restyle(pal theme.Palette) {
	m.st = m.stylesFor(pal)
	for _, in := range []*textinput.Model{&m.keyInput, &m.addInput, &m.search.input, &m.lib.input} {
		m.styleInput(in)
	}
}

func (m Model) newInput(placeholder string, password bool) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = "› "
	if password {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	m.styleInput(&ti)
	return ti
}

func (m Model) styleInput(ti *textinput.Model) {
	s := textinput.DefaultDarkStyles()
	s.Focused.Prompt = m.st.accent
	s.Blurred.Prompt = m.st.muted
	s.Focused.Text = m.st.primary
	s.Blurred.Text = m.st.secondary
	s.Focused.Placeholder = m.st.muted
	s.Blurred.Placeholder = m.st.muted
	s.Cursor.Color = m.st.pal.Accent
	ti.SetStyles(s)
}

// --- messages ---

type meMsg struct {
	user torbox.User
	err  error
	key  *secret.Key // set when validating a newly entered key
}
type statusClearMsg struct{ seq int }
type libTickMsg struct{}
type dlTickMsg struct{}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.dlTick(0)}
	if m.askMode {
		// The answer arrives as a BackgroundColorMsg; until then the dark
		// palette stands, which is also what a terminal that never answers
		// (inside tmux, or over a pipe) keeps.
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.api != nil {
		cmds = append(cmds, m.fetchMe(nil), m.fetchLibrary(), textinput.Blink)
	}
	return tea.Batch(cmds...)
}

func (m Model) fetchMe(key *secret.Key) tea.Cmd {
	api := m.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		u, err := api.Me(ctx)
		return meMsg{user: u, err: err, key: key}
	}
}

func (m Model) libTick() tea.Cmd {
	return tea.Tick(m.opt.Config.RefreshInterval(), func(time.Time) tea.Msg { return libTickMsg{} })
}

// dlTick polls aria2 quickly while the downloads tab is open.
func (m Model) dlTick(d time.Duration) tea.Cmd {
	if d == 0 {
		d = time.Second
		if m.tab != viewDownloads {
			d = 3 * time.Second
		}
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return dlTickMsg{} })
}

func (m *Model) setStatus(s string, isErr bool) tea.Cmd {
	m.status = s
	m.statusErr = isErr
	m.statusBusy = false
	m.statusSeq++
	seq := m.statusSeq
	d := 4 * time.Second
	if isErr {
		d = 8 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return statusClearMsg{seq: seq} })
}

// setBusy is setStatus for work that has started but not finished, which
// is neither a success nor a failure yet.
func (m *Model) setBusy(s string) tea.Cmd {
	cmd := m.setStatus(s, false)
	m.statusBusy = true
	return cmd
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		// tori never paints a background, so on a light terminal the dark
		// palette's pale text would sit on white.
		if m.askMode && !msg.IsDark() {
			m.restyle(theme.Base(false))
		}
		return m, nil
	case statusClearMsg:
		if msg.seq == m.statusSeq {
			m.status = ""
		}
		return m, nil
	case meMsg:
		return m.handleMe(msg)
	case searchDoneMsg:
		return m.handleSearchDone(msg)
	case addedMsg:
		return m.handleAdded(msg)
	case libraryMsg:
		return m.handleLibrary(msg)
	case libTickMsg:
		if m.api == nil {
			return m, nil
		}
		return m, m.fetchLibrary()
	case dlTickMsg:
		return m, m.fetchJobs()
	case jobsMsg:
		gid := m.selectedJobGID()
		m.dl.jobs, m.dl.err = msg.jobs, msg.err
		m.dl.cursor = m.jobIndex(gid, m.dl.cursor)
		return m, m.dlTick(0)
	case actionMsg:
		cmds := []tea.Cmd{m.setStatus(msg.text, msg.err != nil)}
		if msg.err != nil {
			m.status = msg.text + ": " + msg.err.Error()
			if torbox.IsAuth(msg.err) {
				return m.promptKey("TorBox rejected the API key")
			}
		}
		if msg.refreshLib {
			cmds = append(cmds, m.fetchLibrary())
		}
		if msg.refreshDL {
			cmds = append(cmds, m.fetchJobs())
		}
		return m, tea.Batch(cmds...)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg, tea.MouseWheelMsg:
		return m.handleMouse(msg)
	}
	return m.updateInputs(msg)
}

// updateInputs forwards blink and paste messages to the focused input.
func (m Model) updateInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case m.overlay == overlayKey:
		m.keyInput, cmd = m.keyInput.Update(msg)
	case m.overlay == overlayAdd:
		m.addInput, cmd = m.addInput.Update(msg)
	case m.overlay == overlayNone && m.tab == viewSearch && m.search.input.Focused():
		m.search.input, cmd = m.search.input.Update(msg)
	case m.overlay == overlayNone && m.tab == viewLibrary && m.lib.input.Focused():
		m.lib.input, cmd = m.lib.input.Update(msg)
	}
	return m, cmd
}

func (m Model) handleMe(msg meMsg) (tea.Model, tea.Cmd) {
	m.checking = false
	if msg.err != nil {
		if msg.key != nil || torbox.IsAuth(msg.err) {
			m.api = nil
			return m.promptKey(msg.err.Error())
		}
		return m, m.setStatus("account check failed: "+msg.err.Error(), true)
	}
	u := msg.user
	m.user = &u
	if msg.key == nil {
		return m, nil
	}
	// A new key worked: keep it and start normally.
	m.keySource = secret.SourceKeyring
	var cmds []tea.Cmd
	if err := m.opt.Keyring.Save(*msg.key); err != nil {
		m.keySource = "session"
		cmds = append(cmds, m.setStatus("key works but could not be saved to the keyring: "+err.Error(), true))
	} else {
		cmds = append(cmds, m.setStatus("API key saved to the keyring", false))
	}
	m.overlay = overlayNone
	m.keyInput.Reset()
	m.keyInput.Blur()
	m.tab = viewSearch
	cmds = append(cmds, m.search.input.Focus(), m.fetchLibrary())
	return m, tea.Batch(cmds...)
}

func (m Model) promptKey(reason string) (tea.Model, tea.Cmd) {
	if m.keySource == secret.SourceEnv {
		// Replacing the key would not stick while the env var is set.
		return m, m.setStatus(reason+" (the key comes from "+secret.EnvKey+"; fix it there)", true)
	}
	m.api = nil
	m.user = nil
	m.overlay = overlayKey
	m.keyErr = reason
	m.search.input.Blur()
	return m, m.keyInput.Focus()
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.overlay {
	case overlayKey:
		return m.handleKeyEntry(msg, key)
	case overlayHelp:
		m.overlay = m.prevOv
		return m, nil
	case overlayAdd:
		return m.handleAddKey(msg, key)
	case overlayFiles:
		return m.handleFilesKey(key)
	case overlayConfirm:
		return m.handleConfirmKey(key)
	}

	// Alt+1/2/3 switch tabs from anywhere, even while typing a search.
	switch key {
	case "alt+1":
		return m.switchTab(viewSearch)
	case "alt+2":
		return m.switchTab(viewLibrary)
	case "alt+3":
		return m.switchTab(viewDownloads)
	}

	// Typing into the search box or the library filter swallows everything
	// but navigation.
	if m.tab == viewSearch && m.search.input.Focused() {
		return m.handleSearchInputKey(msg, key)
	}
	if m.tab == viewLibrary && m.lib.input.Focused() {
		return m.handleLibraryInputKey(msg, key)
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "?":
		m.prevOv = m.overlay
		m.overlay = overlayHelp
		return m, nil
	case "1":
		return m.switchTab(viewSearch)
	case "2":
		return m.switchTab(viewLibrary)
	case "3":
		return m.switchTab(viewDownloads)
	case "tab":
		return m.switchTab((m.tab + 1) % 3)
	case "shift+tab":
		return m.switchTab((m.tab + 2) % 3)
	case "a":
		m.overlay = overlayAdd
		m.addInput.Reset()
		return m, m.addInput.Focus()
	}
	switch m.tab {
	case viewSearch:
		return m.handleSearchKey(key)
	case viewLibrary:
		return m.handleLibraryKey(key)
	default:
		return m.handleDownloadsKey(key)
	}
}

func (m Model) switchTab(v view) (tea.Model, tea.Cmd) {
	if v != viewSearch {
		m.search.input.Blur()
	}
	m.tab = v
	if v == viewSearch && len(m.search.results) == 0 {
		return m, m.search.input.Focus()
	}
	if v == viewDownloads {
		return m, m.fetchJobs()
	}
	return m, nil
}

func (m Model) handleKeyEntry(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		return m, tea.Quit
	case "enter":
		if m.checking {
			return m, nil
		}
		k, err := secret.NewKey(m.keyInput.Value())
		if err != nil || k.Empty() {
			m.keyErr = "enter a key (torbox.app → Settings → API)"
			return m, nil
		}
		m.checking = true
		m.keyErr = ""
		m.api = m.opt.NewAPI(k.Reveal())
		return m, m.fetchMe(&k)
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(msg)
	return m, cmd
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	if m.opt.Config.MouseEnabled() {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

func (m Model) render() string {
	if m.width < minWidth || m.height < minHeight {
		return m.st.muted.Render("tori needs at least 50×12")
	}
	lo := newLayout(m.width, m.height)
	switch m.overlay {
	case overlayKey:
		return m.viewKeyEntry(lo)
	case overlayHelp:
		return m.viewHelp(lo)
	case overlayAdd:
		return m.viewAdd(lo)
	case overlayConfirm:
		return m.viewConfirm(lo)
	case overlayFiles:
		return m.viewFiles(lo)
	}
	switch m.tab {
	case viewLibrary:
		return m.viewLibrary(lo)
	case viewDownloads:
		return m.viewDownloads(lo)
	default:
		return m.viewSearch(lo)
	}
}

func (m Model) activeJobs() int {
	n := 0
	for _, j := range m.dl.jobs {
		if j.Status == "active" || j.Status == "waiting" {
			n++
		}
	}
	return n
}

func clamp(i, n int) int {
	if n == 0 || i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// actionMsg reports a finished background action.
type actionMsg struct {
	text       string
	err        error
	refreshLib bool
	refreshDL  bool
}

func lower(s string) string { return strings.ToLower(s) }
