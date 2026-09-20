package torbox

import (
	"context"
	"net/url"
	"strconv"
)

// Kind is one of TorBox's three download types.
type Kind int

const (
	KindTorrent Kind = iota
	KindWeb
	KindUsenet
)

func (k Kind) String() string {
	switch k {
	case KindWeb:
		return "web"
	case KindUsenet:
		return "usenet"
	default:
		return "torrent"
	}
}

// Per-kind path segments and ID parameter names. Note the control endpoint
// for web downloads says webdl_id while requestdl says web_id.
type kindAPI struct {
	prefix, control, controlID, requestID string
}

var kinds = map[Kind]kindAPI{
	KindTorrent: {"/torrents", "/torrents/controltorrent", "torrent_id", "torrent_id"},
	KindWeb:     {"/webdl", "/webdl/controlwebdownload", "webdl_id", "web_id"},
	KindUsenet:  {"/usenet", "/usenet/controlusenetdownload", "usenet_id", "usenet_id"},
}

type File struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Size      Int64  `json:"size"`
	Mimetype  string `json:"mimetype"`
}

// DisplayName is the file's name without the torrent folder prefix.
func (f File) DisplayName() string {
	if f.ShortName != "" {
		return f.ShortName
	}
	return f.Name
}

// Item is a torrent, web download or usenet download in the user's list.
type Item struct {
	Kind             Kind    `json:"-"`
	ID               int64   `json:"id"`
	Hash             string  `json:"hash"`
	Name             string  `json:"name"`
	Size             Int64   `json:"size"`
	Active           bool    `json:"active"`
	State            string  `json:"download_state"`
	Progress         float64 `json:"progress"`
	Speed            Int64   `json:"download_speed"`
	UploadSpeed      Int64   `json:"upload_speed"`
	ETA              Int64   `json:"eta"`
	Seeds            int     `json:"seeds"`
	Peers            int     `json:"peers"`
	Ratio            float64 `json:"ratio"`
	Cached           bool    `json:"cached"`
	DownloadFinished bool    `json:"download_finished"`
	DownloadPresent  bool    `json:"download_present"`
	CreatedAt        string  `json:"created_at"`
	ExpiresAt        *string `json:"expires_at"`
	Error            string  `json:"error"`
	Files            []File  `json:"files"`
}

// Ready means the files are on TorBox and can be downloaded.
func (it Item) Ready() bool { return it.DownloadFinished && it.DownloadPresent }

// Percent normalises progress, which the docs show as 0-1 but some clients
// treat as 0-100.
func (it Item) Percent() float64 {
	p := it.Progress
	if p <= 1 {
		p *= 100
	}
	if it.Ready() {
		return 100
	}
	if p > 100 {
		p = 100
	}
	return p
}

// List returns all items of one kind. Torrent lists are cached server-side
// for minutes unless bypassed, so tori always bypasses.
func (c *Client) List(ctx context.Context, k Kind) ([]Item, error) {
	q := url.Values{"bypass_cache": {"true"}}
	var items []Item
	if err := c.get(ctx, c.BaseURL, kinds[k].prefix+"/mylist", q, &items); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Kind = k
	}
	return items, nil
}

// AddResult identifies the created item.
type AddResult struct {
	Kind Kind
	ID   int64
	Hash string
}

// AddMagnet adds a torrent by magnet link.
func (c *Client) AddMagnet(ctx context.Context, magnet string, onlyIfCached bool) (AddResult, error) {
	var out struct {
		Hash      string `json:"hash"`
		TorrentID int64  `json:"torrent_id"`
	}
	f := map[string]string{"magnet": magnet}
	if onlyIfCached {
		f["add_only_if_cached"] = "true"
	}
	err := c.postMultipart(ctx, "/torrents/createtorrent", f, &out)
	return AddResult{Kind: KindTorrent, ID: out.TorrentID, Hash: out.Hash}, err
}

// AddWeb adds a direct or hoster link.
func (c *Client) AddWeb(ctx context.Context, link string) (AddResult, error) {
	var out struct {
		Hash string `json:"hash"`
		ID   int64  `json:"webdownload_id"`
	}
	err := c.postMultipart(ctx, "/webdl/createwebdownload", map[string]string{"link": link}, &out)
	return AddResult{Kind: KindWeb, ID: out.ID, Hash: out.Hash}, err
}

// AddUsenet adds an NZB by link. TorBox's own search NZB links only work
// from inside TorBox, so they must go through here.
func (c *Client) AddUsenet(ctx context.Context, link, name string) (AddResult, error) {
	var out struct {
		Hash string `json:"hash"`
		ID   int64  `json:"usenetdownload_id"`
	}
	f := map[string]string{"link": link}
	if name != "" {
		f["name"] = name
	}
	err := c.postMultipart(ctx, "/usenet/createusenetdownload", f, &out)
	return AddResult{Kind: KindUsenet, ID: out.ID, Hash: out.Hash}, err
}

// Delete removes an item from the user's list.
func (c *Client) Delete(ctx context.Context, it Item) error {
	return c.control(ctx, it, "delete")
}

// Reannounce asks the trackers for peers again (torrents only).
func (c *Client) Reannounce(ctx context.Context, it Item) error {
	return c.control(ctx, it, "reannounce")
}

func (c *Client) control(ctx context.Context, it Item, op string) error {
	api := kinds[it.Kind]
	body := map[string]any{api.controlID: it.ID, "operation": op}
	return c.postJSON(ctx, api.control, body, nil)
}

// DownloadURL returns a CDN link for one file, or a zip of the whole item
// when fileID is negative. Links must be started within about an hour.
func (c *Client) DownloadURL(ctx context.Context, it Item, fileID int64) (string, error) {
	api := kinds[it.Kind]
	q := url.Values{
		"token":       {c.key},
		api.requestID: {strconv.FormatInt(it.ID, 10)},
	}
	if fileID < 0 {
		q.Set("zip_link", "true")
	} else {
		q.Set("file_id", strconv.FormatInt(fileID, 10))
	}
	var link string
	err := c.get(ctx, c.BaseURL, api.prefix+"/requestdl", q, &link)
	return link, err
}

// CheckCached returns the subset of torrent hashes TorBox already has.
func (c *Client) CheckCached(ctx context.Context, hashes []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(hashes) == 0 {
		return out, nil
	}
	var data map[string]struct {
		Hash string `json:"hash"`
	}
	q := "?format=object&list_files=false"
	body := map[string]any{"hashes": hashes}
	if err := c.postJSON(ctx, "/torrents/checkcached"+q, body, &data); err != nil {
		return nil, err
	}
	for h := range data {
		out[h] = true
	}
	return out, nil
}

// User is the subset of /user/me tori shows.
type User struct {
	Email            string  `json:"email"`
	Plan             int     `json:"plan"`
	PremiumExpiresAt *string `json:"premium_expires_at"`
}

func (u User) PlanName() string {
	switch u.Plan {
	case 0:
		return "Free"
	case 1:
		return "Essential"
	case 2:
		return "Pro"
	case 3:
		return "Standard"
	default:
		return "plan " + strconv.Itoa(u.Plan)
	}
}

// Me validates the key and returns the account.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.get(ctx, c.BaseURL, "/user/me", nil, &u)
	return u, err
}
