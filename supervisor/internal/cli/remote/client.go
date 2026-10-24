package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIPath is the remote access API's root path on a supervisor.
const APIPath = "/api/agent/v1"

// Client talks to one desktop's remote access API.
type Client struct {
	Alias   string // remote name, for messages ("alice"); may equal Desktop
	Base    string
	Desktop string // expected desktop name; may be empty
	Token   string
	HTTP    *http.Client
}

// FromRemote builds a client for a saved remote.
func FromRemote(r *Remote) *Client {
	return &Client{Alias: r.Name, Base: r.Base, Desktop: r.Desktop, Token: r.Token}
}

// Info is GET /api/agent/v1.
type Info struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Image        string `json:"image"`
	ImageTitle   string `json:"imageTitle"`
	ImageVersion string `json:"imageVersion"`
	Screen       struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"screen"`
	API int `json:"api"`
}

// Label names the desktop in messages.
func (c *Client) Label() string {
	if c.Desktop != "" {
		return c.Desktop
	}
	if c.Alias != "" {
		return c.Alias
	}
	return "the desktop"
}

// URL returns base + APIPath + p.
func (c *Client) URL(p string) string { return c.Base + APIPath + p }

// WSURL is URL with the scheme switched to ws/wss.
func (c *Client) WSURL(p string) string {
	u := c.URL(p)
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// Header returns the auth header for remote access API calls.
func (c *Client) Header() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.Token)
	return h
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Do sends an authenticated request to the remote access API.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, c.NetError(err)
	}
	return resp, nil
}

// Info fetches the desktop's description. A 409 (stopped) still proves the
// token works; it comes back as an *APIError with Status 409.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL(""), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.ResponseError(resp)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("%s answered with something that isn't the remote access API (%v): check the URL", c.Base, err)
	}
	return &info, nil
}

// APIError is an error answer from the remote access API, already phrased with what
// to do next.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// IsStatus reports whether err is an *APIError with the given status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// ResponseError turns a non-2xx response into an *APIError. It reads at most
// a little of the body.
func (c *Client) ResponseError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	var msg string
	if resp.Body != nil {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(data, &body) == nil && body.Error != "" {
			msg = body.Error
		} else {
			msg = strings.TrimSpace(string(data))
		}
	}
	return &APIError{Status: resp.StatusCode, Msg: c.explain(resp.StatusCode, msg)}
}

func (c *Client) explain(status int, msg string) string {
	name := c.Label()
	switch status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("token rejected (401): ask someone with access to %s for a new token (on its Remote access tab; this one is wrong, expired or revoked), then run `deskyard remote add %s %s --token dyt_…` again",
			name, aliasOr(c.Alias, name), (&Target{Base: c.Base, Desktop: c.Desktop}).URL())
	case http.StatusConflict:
		if msg == "" {
			msg = name + " is not running; someone with access can start it"
		}
		return msg + " (409)"
	case http.StatusNotFound:
		return fmt.Sprintf("%s has no remote access API (404): check the URL (expected https://host/%s)", c.Base, name)
	case http.StatusForbidden:
		if msg == "" {
			msg = "forbidden"
		}
		return fmt.Sprintf("%s (403): this token can't do that", msg)
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	if status >= 500 {
		return fmt.Sprintf("the supervisor failed (%d): %s; try again, and if it keeps failing tell someone who runs the supervisor", status, msg)
	}
	return fmt.Sprintf("%s (%d)", msg, status)
}

func aliasOr(alias, name string) string {
	if alias != "" {
		return alias
	}
	return name
}

// NetError phrases a transport error.
func (c *Client) NetError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("can't reach %s: %v. Check the URL and your network", c.Base, err)
}
