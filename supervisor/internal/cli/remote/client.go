package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a supervisor's API, and to one computer's remote access
// API, with a client credential.
type Client struct {
	Alias    string // server name, for messages ("deskyard.example.com")
	Base     string
	Computer string // the computer URL and WSURL address; may be empty
	Token    string // client credential (dyc_…); empty for unauthenticated calls
	HTTP     *http.Client
}

// FromServer builds a client for a saved server and one of its computers.
func FromServer(s *Server, computer string) *Client {
	return &Client{Alias: s.Name, Base: s.URL, Computer: computer, Token: s.Credential}
}

// Info is GET /api/computers/{name}/status.
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

// Label names the computer in messages.
func (c *Client) Label() string {
	if c.Computer != "" {
		return c.Computer
	}
	if c.Alias != "" {
		return c.Alias
	}
	return "the computer"
}

// URL is the computer's remote access route p (/exec, /cdp/…).
func (c *Client) URL(p string) string {
	return c.Base + "/api/computers/" + url.PathEscape(c.Computer) + p
}

// API is the supervisor API route p (/me, /accounts).
func (c *Client) API(p string) string { return c.Base + "/api" + p }

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

// Header returns the auth header for API calls.
func (c *Client) Header() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.Token)
	return h
}

// HTTPClient is c.HTTP (or the default client) with redirects not followed,
// so a redirect never carries the credential to another route.
func (c *Client) HTTPClient() *http.Client {
	hc := http.Client{}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

// Do sends a request with the credential, when there is one.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return nil, c.NetError(err)
	}
	return resp, nil
}

// JSON sends in (when not nil) as JSON to the API route path and decodes a
// 2xx answer into out (when not nil). Other answers come back as *APIError.
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	return c.JSONWithin(ctx, Timeout, method, path, in, out)
}

// Timeout is how long JSON waits for an answer.
var Timeout = 30 * time.Second

// JSONWithin is JSON waiting up to d, for a request the server works on for
// longer.
func (c *Client) JSONWithin(ctx context.Context, d time.Duration, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.API(path), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.ResponseError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s answered with something that isn't the Deskyard API (%v): check the URL", c.Base, err)
	}
	return nil
}

// Info fetches the computer's description and state; it also checks the
// credential and the computer's name.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL("/status"), nil)
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
		return nil, fmt.Errorf("%s answered with something that isn't the Deskyard API (%v): check the URL", c.Base, err)
	}
	return &info, nil
}

// APIError is an error answer from the supervisor's API, already phrased with what
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
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Not followed, so the credential never goes elsewhere.
		to := resp.Header.Get("Location")
		if u, err := resp.Location(); err == nil {
			to = u.String()
			if t, err := ParseURL(u.Scheme + "://" + u.Host); err == nil {
				return &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("%s redirected the request to %s (%d): sign in with the server's final address, e.g. `deskyard login %s`", c.Base, to, resp.StatusCode, t.Base)}
			}
		}
		return &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("%s redirected the request to %q (%d): use the server's final address", c.Base, to, resp.StatusCode)}
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
		if msg == "" {
			msg = "the credential was rejected"
		}
		login := (&Target{Base: c.Base, Computer: c.Computer}).URL()
		if c.Alias != "" && c.Alias != DefaultName(c.Base) {
			login += " --name " + c.Alias
		}
		return fmt.Sprintf("%s (401): run `deskyard login %s` again", msg, login)
	case http.StatusConflict:
		if msg == "" {
			msg = name + " is not running; someone with access can start it"
		}
		return msg + " (409)"
	case http.StatusNotFound:
		switch msg {
		case "":
			return fmt.Sprintf("%s has no such API (404): check the URL (expected https://host/%s)", c.Base, name)
		case "no such computer":
			if c.Computer != "" {
				return fmt.Sprintf("%s has no computer %q you can use (404): check the name, or sign in as an account with access to it (deskyard login)", c.Base, c.Computer)
			}
		}
		return msg + " (404)"
	case http.StatusForbidden:
		if msg == "" {
			msg = "forbidden"
		}
		return msg + " (403)"
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	if status >= 500 {
		return fmt.Sprintf("the supervisor failed (%d): %s; try again, and if it keeps failing tell someone who runs the supervisor", status, msg)
	}
	return fmt.Sprintf("%s (%d)", msg, status)
}

// NetError phrases a transport error.
func (c *Client) NetError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("can't reach %s: %v. Check the URL and your network", c.Base, err)
}
