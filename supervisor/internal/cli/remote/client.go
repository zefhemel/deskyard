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

// Client talks to a supervisor's API, and to one environment's remote access
// API, with a client credential.
type Client struct {
	Alias       string // server name, for messages ("deskyard.example.com")
	Base        string
	Environment string // the environment URL and WSURL address; may be empty
	Token       string // client credential (dyc_…); empty for unauthenticated calls
	HTTP        *http.Client
}

// FromServer builds a client for a saved server and one of its environments.
func FromServer(s *Server, environment string) *Client {
	return &Client{Alias: s.Name, Base: s.URL, Environment: environment, Token: s.Credential}
}

// Info is GET /api/environments/{name}/status.
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

// Label names the environment in messages.
func (c *Client) Label() string {
	if c.Environment != "" {
		return c.Environment
	}
	if c.Alias != "" {
		return c.Alias
	}
	return "the environment"
}

// URL is the environment's remote access route p (/exec, /cdp/…).
func (c *Client) URL(p string) string {
	return c.Base + "/api/environments/" + url.PathEscape(c.Environment) + p
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

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Do sends a request with the credential, when there is one.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, c.NetError(err)
	}
	return resp, nil
}

// JSON sends in (when not nil) as JSON to the API route path and decodes a
// 2xx answer into out (when not nil). Other answers come back as *APIError.
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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

// Info fetches the environment's description and state; it also checks the
// credential and the environment's name.
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
		login := (&Target{Base: c.Base, Environment: c.Environment}).URL()
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
		case "no such environment":
			if c.Environment != "" {
				return fmt.Sprintf("%s has no environment %q you can use (404): check the name, or sign in as an account with access to it (deskyard login)", c.Base, c.Environment)
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
