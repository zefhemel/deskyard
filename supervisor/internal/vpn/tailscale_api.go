package vpn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type tailscaleClient struct {
	baseURL, clientID, secret, tailnet string
	http                               *http.Client
}

func (s *Store) tailscaleClient(id string) (*tailscaleClient, error) {
	c, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if c.Tailscale == nil {
		return nil, invalid("provider", "Tailscale required")
	}
	files, err := s.SecretFiles(id)
	if err != nil {
		return nil, errors.New("Tailscale credentials unavailable")
	}
	secret, err := os.ReadFile(files.Password)
	if err != nil {
		return nil, errors.New("Tailscale credentials unavailable")
	}
	return &tailscaleClient{baseURL: "https://api.tailscale.com", clientID: c.Tailscale.ClientID, secret: string(secret), tailnet: c.Tailscale.Tailnet, http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *tailscaleClient) token(ctx context.Context) (string, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.clientID}, "client_secret": {c.secret}}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("Tailscale authentication request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var v struct {
		Token string `json:"access_token"`
	}
	if err = c.request(req, &v, false); err != nil {
		return "", err
	}
	if v.Token == "" || len(v.Token) > 16384 {
		return "", errors.New("Tailscale returned invalid authentication data")
	}
	return v.Token, nil
}
func (c *tailscaleClient) request(req *http.Request, out any, allowGone bool) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("Tailscale API is unavailable")
	}
	defer resp.Body.Close()
	if allowGone && resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		switch resp.StatusCode {
		case 401, 403:
			return errors.New("Tailscale authorization failed; check OAuth scopes and device tags")
		case 429:
			return errors.New("Tailscale API rate limit reached; retry later")
		default:
			return errors.New("Tailscale API request failed")
		}
	}
	if out == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 || json.Unmarshal(b, out) != nil {
		return errors.New("Tailscale returned invalid response data")
	}
	return nil
}
func (c *tailscaleClient) EnrollKey(ctx context.Context, tags []string) (string, error) {
	token, err := c.token(ctx)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{"expirySeconds": 600, "capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{"reusable": false, "ephemeral": false, "preauthorized": true, "tags": tags}}}})
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v2/tailnet/"+url.PathEscape(c.tailnet)+"/keys", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("Tailscale enrollment request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	var v struct {
		Key string `json:"key"`
	}
	if err = c.request(req, &v, false); err != nil {
		return "", err
	}
	if !strings.HasPrefix(v.Key, "tskey-auth-") || len(v.Key) > 4096 || strings.ContainsAny(v.Key, "\r\n\x00") {
		return "", errors.New("Tailscale returned invalid enrollment data")
	}
	return v.Key, nil
}
func (c *tailscaleClient) DeleteDevice(ctx context.Context, nodeID string) error {
	if nodeID == "" || len(nodeID) > 256 || strings.ContainsAny(nodeID, "/\\\x00\r\n") {
		return errors.New("Tailscale device identity is unavailable")
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/api/v2/device/"+url.PathEscape(nodeID), nil)
	if err != nil {
		return errors.New("Tailscale device removal request failed")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return c.request(req, nil, true)
}
