package server

import (
	"context"
	"net/url"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// fakeEngine knows one stopped computer, "alice", and nothing else.
type fakeEngine struct{ engine.Engine }

var alice = engine.Container{ID: "c1", Name: "deskyard-alice", State: "exited", ImageID: "sha256:abc",
	Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: "alice", computers.LabelID: "id-alice"}}

func (fakeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	if id == "deskyard-alice" || id == "c1" {
		return alice, nil
	}
	return engine.Container{}, engine.ErrNotFound
}
func (fakeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return []engine.Container{alice}, nil
}
func (fakeEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{}, engine.ErrNotFound
}
func (fakeEngine) Info(context.Context) (engine.HostInfo, error) { return engine.HostInfo{CPUs: 4}, nil }

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	accts, _ := accounts.Load(dir)
	_, _ = accts.EnsureAdmin("admin", "pw")
	_, _ = accts.Create("anna", "pw", false, nil)
	e := fakeEngine{}
	s, err := New(Options{Log: log, Eng: e, Accounts: accts, StateDir: dir, DefaultImage: "img",
		Computers: computers.New(e, computers.Config{DataDir: dir, Subnet: "10.0.0.0/16"}, log),
		UI:        fstest.MapFS{"index.html": {Data: []byte("<!doctype html>ui")}, "assets/a.js": {Data: []byte("js")}}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

func login(t *testing.T, ts *httptest.Server, user string) *http.Client {
	jar := &cookieJar{}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("POST", ts.URL+"/api/login", strings.NewReader(`{"username":"`+user+`","password":"pw"}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login %s: %v %v", user, err, resp)
	}
	return c
}

type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.cookies = append(j.cookies, cs...) }
func (j *cookieJar) Cookies(*url.URL) []*http.Cookie         { return j.cookies }

func status(t *testing.T, c *http.Client, method, url, origin string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestUnauthenticated(t *testing.T) {
	_, ts := newTestServer(t)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/api/me", "/api/computers", "/api/events", "/api/accounts"} {
		if got := status(t, anon, "GET", ts.URL+p, ""); got != 401 {
			t.Errorf("%s: %d", p, got)
		}
	}
	for _, p := range []string{"/", "/alice/", "/computers/alice", "/accounts"} {
		if got := status(t, anon, "GET", ts.URL+p, ""); got != 302 {
			t.Errorf("%s: %d", p, got)
		}
	}
	if got := status(t, anon, "GET", ts.URL+"/login", ""); got != 200 {
		t.Errorf("/login: %d", got)
	}
	if got := status(t, anon, "GET", ts.URL+"/assets/a.js", ""); got != 200 {
		t.Errorf("assets: %d", got)
	}
}

func TestOriginAndPermissions(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if got := status(t, admin, "POST", ts.URL+"/api/computers/alice/start", "https://evil.example"); got != 403 {
		t.Errorf("foreign origin: %d", got)
	}
	if got := status(t, admin, "POST", ts.URL+"/api/computers/alice/start", ""); got != 403 {
		t.Errorf("no origin: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/api/computers/alice", ""); got != 404 {
		t.Errorf("anna sees alice: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/alice/vnc/vnc.html", ""); got != 404 {
		t.Errorf("anna gets alice's client: %d", got)
	}
	if got := status(t, admin, "GET", ts.URL+"/api/computers/alice", ""); got != 200 {
		t.Errorf("admin: %d", got)
	}
	if got := status(t, admin, "GET", ts.URL+"/nosuch/", ""); got != 404 {
		t.Errorf("missing computer: %d", got)
	}
	// Stopped: the desktop WebSocket can't be proxied.
	req, _ := http.NewRequest("GET", ts.URL+"/alice/websockify", nil)
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp, _ := admin.Do(req)
	if resp.StatusCode != 409 {
		t.Errorf("stopped websockify: %d", resp.StatusCode)
	}
}

func TestClientFilePathsStayInside(t *testing.T) {
	dir := t.TempDir()
	if err := untar(strings.NewReader(""), dir, "www"); err != nil {
		t.Fatal(err)
	}
	// serveClientFile cleans the path against "/" before joining.
	for _, p := range []string{"../x", "../../etc/passwd", "a/../../b"} {
		if c := cleanClientPath(p); strings.Contains(c, "..") {
			t.Errorf("%s → %s", p, c)
		}
	}
}
