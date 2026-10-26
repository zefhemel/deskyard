package server

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// fakeEngine knows one stopped desktop, "alice", plus any others a test
// adds, and nothing else. Every image holds the Selkies client in fakeClient.
type fakeEngine struct {
	engine.Engine
	others []engine.Container
}

var alice = engine.Container{ID: "c1", Name: "deskyard-alice", State: "exited", ImageID: "sha256:abc",
	Labels: map[string]string{environments.LabelManaged: "true", environments.LabelName: "alice", environments.LabelRevision: "1"}}

func (e fakeEngine) all() []engine.Container { return append([]engine.Container{alice}, e.others...) }

func (e fakeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	for _, c := range e.all() {
		if id == c.Name || id == c.ID {
			return c, nil
		}
	}
	return engine.Container{}, engine.ErrNotFound
}
func (e fakeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return e.all(), nil
}

// fakeClient is the Selkies web client folder of the fake images.
var fakeClient = map[string]string{
	"index.html":          `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Selkies</title><script type="module" crossorigin src="./assets/index-X.js"></script></head><body><div id="app"></div></body></html>`,
	"assets/index-X.js":   "console.log('selkies')",
	"src/selkies-core.js": "// core",
}

func (fakeEngine) CopyFromImage(_ context.Context, _, p string) (io.ReadCloser, error) {
	if p != clientPath {
		return nil, engine.ErrNotFound
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "selkies_web/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range fakeClient {
		_ = tw.WriteHeader(&tar.Header{Name: "selkies_web/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	return io.NopCloser(&buf), nil
}
func (fakeEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{}, engine.ErrNotFound
}
func (fakeEngine) Info(context.Context) (engine.HostInfo, error) {
	return engine.HostInfo{CPUs: 4}, nil
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	return newTestServerWith(t, fakeEngine{})
}

func newTestServerWith(t *testing.T, e engine.Engine) (*Server, *httptest.Server) {
	return newTestServerCfg(t, e, environments.Config{Subnet: "10.0.0.0/16"})
}

// newTestServerCfg is newTestServerWith with the desktops' config (DataDir is set here).
func newTestServerCfg(t *testing.T, e engine.Engine, cfg environments.Config) (*Server, *httptest.Server) {
	dir := t.TempDir()
	cfg.DataDir = dir
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	accts, _ := accounts.Load(dir)
	_, _ = accts.EnsureAdmin("admin", "pw")
	_, _ = accts.Create("anna", "pw", false, nil)
	s, err := New(Options{Log: log, Eng: e, Accounts: accts, Layout: config.NewLayout(dir), DefaultImage: "img",
		Environments: environments.New(e, cfg, log),
		UI:           fstest.MapFS{"index.html": {Data: []byte("<!doctype html>ui")}, "assets/a.js": {Data: []byte("js")}}})
	if err != nil {
		t.Fatal(err)
	}
	defineEnvironments(t, s)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

// defineEnvironments defines every environment container the engine has
// that isn't defined yet, as if the supervisor had created it. An engine
// that can't list (as some tests make it) defines nothing.
func defineEnvironments(t *testing.T, s *Server) {
	t.Helper()
	cs, err := s.Eng.List(context.Background(), nil)
	if err != nil {
		return
	}
	for _, c := range cs {
		name := c.Labels[environments.LabelName]
		if !environments.IsEnvironment(c.Labels) || name == "" {
			continue
		}
		if _, ok := s.Environments.Defs.Get(name); ok {
			continue
		}
		revision, _ := strconv.ParseUint(c.Labels[environments.LabelRevision], 10, 64)
		def := environments.Definition{Name: name, Created: time.Now().UTC(), Revision: max(revision, 1),
			Image:   environments.ImageRef{Ref: cmp.Or(c.Image, "img"), ID: c.ImageID},
			Network: environments.Network{Kind: environments.NetworkIsolated}, MemoryReclaim: true, Power: environments.PowerStopped}
		if c.Running() {
			def.Power = environments.PowerRunning
		}
		if conn := c.Labels[vpn.LabelConnection]; conn != "" {
			def.Network = environments.Network{Kind: environments.NetworkVPN, Connection: conn}
		}
		if err := s.Environments.Defs.Put(def); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(s.Environments.Layout.HomeOf(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
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
func (j *cookieJar) Cookies(*url.URL) []*http.Cookie          { return j.cookies }

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
	for _, p := range []string{"/api/me", "/api/environments", "/api/events", "/api/accounts"} {
		if got := status(t, anon, "GET", ts.URL+p, ""); got != 401 {
			t.Errorf("%s: %d", p, got)
		}
	}
	for _, p := range []string{"/", "/alice/", "/environments/alice", "/accounts"} {
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
	if got := status(t, admin, "POST", ts.URL+"/api/environments/alice/start", "https://evil.example"); got != 403 {
		t.Errorf("foreign origin: %d", got)
	}
	if got := status(t, admin, "POST", ts.URL+"/api/environments/alice/start", ""); got != 403 {
		t.Errorf("no origin: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/api/environments/alice", ""); got != 404 {
		t.Errorf("anna sees alice: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/alice/screen/", ""); got != 404 {
		t.Errorf("anna gets alice's client: %d", got)
	}
	if got := status(t, admin, "GET", ts.URL+"/api/environments/alice", ""); got != 200 {
		t.Errorf("admin: %d", got)
	}
	if got := status(t, admin, "GET", ts.URL+"/nosuch/", ""); got != 404 {
		t.Errorf("missing desktop: %d", got)
	}
	// Stopped: the desktop WebSocket can't be proxied.
	req, _ := http.NewRequest("GET", ts.URL+"/alice/screen/api/websockets", nil)
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	resp, _ := admin.Do(req)
	if resp.StatusCode != 409 {
		t.Errorf("stopped stream: %d", resp.StatusCode)
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
