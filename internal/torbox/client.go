// Package torbox is a small client for the TorBox main API and Search API.
// Response shapes are taken from the official OpenAPI and Postman docs; the
// Search API shapes come from the third-party clients that use it.
package torbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://api.torbox.app/v1/api"
	DefaultSearchURL = "https://search-api.torbox.app"
)

type Client struct {
	BaseURL   string
	SearchURL string
	HTTP      *http.Client
	key       string
}

func New(apiKey string) *Client {
	return &Client{
		BaseURL:   DefaultBaseURL,
		SearchURL: DefaultSearchURL,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		key:       apiKey,
	}
}

// APIError is a failed TorBox call. Code is TorBox's error string (BAD_TOKEN,
// ACTIVE_LIMIT, ...) when the server sent one.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Detail != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	case e.Detail != "":
		return e.Detail
	case e.Code != "":
		return e.Code
	default:
		return fmt.Sprintf("HTTP %d", e.Status)
	}
}

// IsAuth reports a missing or rejected API key.
func IsAuth(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case "NO_AUTH", "BAD_TOKEN", "AUTH_ERROR":
		return true
	}
	return e.Status == http.StatusUnauthorized
}

type envelope struct {
	Success bool            `json:"success"`
	Error   *string         `json:"error"`
	Detail  json.RawMessage `json:"detail"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "tori")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// requestdl carries the key in its query string, and url.Error
		// prints the full URL.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			uerr.URL = redactURL(uerr.URL)
		}
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		if resp.StatusCode >= 400 {
			return &APIError{Status: resp.StatusCode, Detail: strings.TrimSpace(truncate(string(body), 200))}
		}
		return fmt.Errorf("decode response: %w", err)
	}
	// TorBox can report success:true alongside an error code (ITEM_NOT_FOUND),
	// and FastAPI errors carry only detail, so check all three.
	if resp.StatusCode >= 400 || !env.Success || (env.Error != nil && *env.Error != "") {
		e := &APIError{Status: resp.StatusCode, Detail: detailText(env.Detail)}
		if env.Error != nil {
			e.Code = *env.Error
		}
		if resp.StatusCode == http.StatusTooManyRequests && e.Detail == "" {
			e.Detail = "rate limited, try again shortly"
		}
		return e
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return nil
}

// detailText flattens detail, which is a string normally and a list of
// validation errors on 422.
func detailText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		msgs := make([]string, 0, len(list))
		for _, l := range list {
			msgs = append(msgs, l.Msg)
		}
		return strings.Join(msgs, "; ")
	}
	return truncate(string(raw), 200)
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	q := u.Query()
	if q.Has("token") {
		q.Set("token", "REDACTED")
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (c *Client) get(ctx context.Context, base, path string, q url.Values, out any) error {
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *Client) postMultipart(ctx context.Context, path string, fields map[string]string, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req, out)
}

func boolStr(b bool) string { return strconv.FormatBool(b) }

// Int64 accepts a JSON number or a numeric string; TorBox sends both.
type Int64 int64

func (n *Int64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = Int64(f)
	return nil
}
