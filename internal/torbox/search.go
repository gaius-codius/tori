package torbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Result is one Search API hit. Torrent and usenet results share a shape.
type Result struct {
	Hash     string `json:"hash"`
	RawTitle string `json:"raw_title"`
	Title    string `json:"title"`
	Magnet   string `json:"magnet"`
	NZB      string `json:"nzb"`
	Seeders  int    `json:"last_known_seeders"`
	Peers    int    `json:"last_known_peers"`
	Size     Int64  `json:"size"`
	Tracker  string `json:"tracker"`
	Files    Int64  `json:"files"`
	Type     string `json:"type"`
	Age      string `json:"age"`
	Cached   bool   `json:"cached"`
	Owned    bool   `json:"owned"`
	Parsed   struct {
		Resolution string `json:"resolution"`
		Quality    string `json:"quality"`
		Codec      string `json:"codec"`
	} `json:"title_parsed_data"`
}

// Name prefers the release name, which carries quality details.
func (r Result) Name() string {
	if r.RawTitle != "" {
		return r.RawTitle
	}
	return r.Title
}

type SearchOptions struct {
	Usenet     bool
	CachedOnly bool
}

var imdbRe = regexp.MustCompile(`^(?:imdb:)?(tt\d{5,})$`)

// Search queries the Search API. A bare IMDb ID (tt0137523) or imdb:tt...
// searches by ID; anything else is a text search.
func (c *Client) Search(ctx context.Context, query string, opt SearchOptions) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	kind := "torrents"
	if opt.Usenet {
		kind = "usenet"
	}
	var path string
	if m := imdbRe.FindStringSubmatch(strings.ToLower(query)); m != nil {
		path = "/" + kind + "/imdb:" + m[1]
	} else {
		path = "/" + kind + "/search/" + url.PathEscape(query)
	}
	q := url.Values{
		"metadata":    {"false"},
		"check_cache": {"true"},
		"check_owned": {"true"},
	}
	if opt.CachedOnly {
		q.Set("cached_only", "true")
	}
	var data struct {
		Torrents []Result `json:"torrents"`
		NZBs     []Result `json:"nzbs"`
	}
	if err := c.get(ctx, c.SearchURL, path, q, &data); err != nil {
		return nil, searchErr(err, c.SearchURL)
	}
	if opt.Usenet {
		return data.NZBs, nil
	}
	return data.Torrents, nil
}

// searchErr makes an unreachable Search API obvious; it has been offline
// while the main API kept working.
func searchErr(err error, base string) error {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		host := base
		if u, perr := url.Parse(base); perr == nil {
			host = u.Host
		}
		return fmt.Errorf("TorBox search is unreachable (%s): %w", host, err)
	}
	return err
}
