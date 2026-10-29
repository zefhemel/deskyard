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
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// fullAccess gives each of usernames full access, for SetAccess.
func fullAccess(usernames ...string) map[string]auth.Level {
	out := map[string]auth.Level{}
	for _, u := range usernames {
		out[u] = auth.LevelFull
	}
	return out
}

// fakeEngine knows one stopped desktop, "alice", plus any others a test
// adds, and nothing else. Every image holds the Selkies client in fakeClient.
type fakeEngine struct {
	engine.Engine
	others []engine.Container
}

var alice = engine.Container{ID: "c1", Name: "deskyard-alice", State: "exited", ImageID: "sha256:abc",
	Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: "alice", computers.LabelRevision: "1"}}

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
	return newTestServerCfg(t, e, computers.Config{Subnet: "10.0.0.0/16"})
}

// newTestServerCfg is newTestServerWith with the desktops' config (DataDir is set here).
func newTestServerCfg(t *testing.T, e engine.Engine, cfg computers.Config) (*Server, *httptest.Server) {
	dir := t.TempDir()
	cfg.DataDir = dir
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	accts, _ := accounts.Load(dir)
	_, _ = accts.EnsureAdmin("admin", "pw")
	_, _ = accts.Create(accounts.NewAccount{Username: "anna", Password: "pw"})
	s, err := New(Options{Log: log, Eng: e, Accounts: accts, Layout: config.NewLayout(dir), DefaultImage: "img",
		Computers: computers.New(e, cfg, log),
		UI:        fstest.MapFS{"index.html": {Data: []byte("<!doctype html>ui")}, "assets/a.js": {Data: []byte("js")}}})
	if err != nil {
		t.Fatal(err)
	}
	defineComputers(t, s)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts
}

// defineComputers defines every computer container the engine has
// that isn't defined yet, as if the supervisor had created it. An engine
// that can't list (as some tests make it) defines nothing.
func defineComputers(t *testing.T, s *Server) {
	t.Helper()
	cs, err := s.Eng.List(context.Background(), nil)
	if err != nil {
		return
	}
	for _, c := range cs {
		name := c.Labels[computers.LabelName]
		if !computers.IsComputer(c.Labels) || name == "" {
			continue
		}
		if _, ok := s.Computers.Defs.Get(name); ok {
			continue
		}
		revision, _ := strconv.ParseUint(c.Labels[computers.LabelRevision], 10, 64)
		def := computers.Definition{Name: name, Created: time.Now().UTC(), Revision: max(revision, 1),
			Image:   computers.ImageRef{Ref: cmp.Or(c.Image, "img"), ID: c.ImageID},
			Network: computers.Network{Kind: computers.NetworkIsolated}, MemoryReclaim: true, Power: computers.PowerStopped}
		if c.Running() {
			def.Power = computers.PowerRunning
		}
		if conn := c.Labels[vpn.LabelConnection]; conn != "" {
			def.Network = computers.Network{Kind: computers.NetworkVPN, Connection: conn}
		}
		if err := s.Computers.Defs.Put(def); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(s.Computers.Layout.HomeOf(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// restart starts another server on s's data folder, as a supervisor restart
// would.
func restart(t *testing.T, s *Server) *Server {
	t.Helper()
	persistedAccounts, err := accounts.Load(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Options{Log: s.Log, Eng: s.Eng, Computers: s.Computers, Accounts: persistedAccounts, Layout: s.Layout, DefaultImage: "img"})
	if err != nil {
		t.Fatal(err)
	}
	return restarted
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

// cookieJar keeps the latest cookie of each name, as a browser does.
type cookieJar struct{ cookies []*http.Cookie }

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	for _, c := range cs {
		j.cookies = slices.DeleteFunc(j.cookies, func(o *http.Cookie) bool { return o.Name == c.Name })
		if c.MaxAge >= 0 {
			j.cookies = append(j.cookies, c)
		}
	}
}
func (j *cookieJar) Cookies(*url.URL) []*http.Cookie { return j.cookies }

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

// The link and CLI login pages open signed out: the recipient of a sharing
// link has no session yet, and a redirect would lose the token fragment.
func TestSignedOutPages(t *testing.T) {
	_, ts := newTestServer(t)
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/link", "/cli-login"} {
		resp, err := anon.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(b) != "<!doctype html>ui" {
			t.Errorf("%s: %d %q", p, resp.StatusCode, b)
		}
	}
	for _, p := range []string{"/account", "/linked", "/cli-login-x"} {
		resp, err := anon.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), "/login?next=") {
			t.Errorf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
		}
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
	if got := status(t, anna, "GET", ts.URL+"/alice/screen/", ""); got != 404 {
		t.Errorf("anna gets alice's client: %d", got)
	}
	if got := status(t, admin, "GET", ts.URL+"/api/computers/alice", ""); got != 200 {
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

func TestCookieOfDeletedAccountRejectedAfterRecreate(t *testing.T) {
	s, ts := newTestServer(t)
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "bob", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	bob := login(t, ts, "bob")
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "bob", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if got := status(t, bob, "GET", ts.URL+"/api/me", ""); got != 401 {
		t.Fatalf("old cookie on recreated bob: %d", got)
	}
}

func TestTokenAccountCannotPasswordLogin(t *testing.T) {
	s, ts := newTestServer(t)
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "sanne"}); err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{"", "pw"} {
		req, _ := http.NewRequest("POST", ts.URL+"/api/login", strings.NewReader(`{"username":"sanne","password":"`+pw+`"}`))
		req.Header.Set("Origin", ts.URL)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 401 || !strings.Contains(string(body), "wrong username or password") || len(resp.Cookies()) != 0 {
			t.Fatalf("password %q: %d %s", pw, resp.StatusCode, body)
		}
	}
}

func TestExpiredAccountSessionRejected(t *testing.T) {
	s, ts := newTestServer(t)
	anna := login(t, ts, "anna")
	past := time.Now().Add(-time.Minute)
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Expires = &past }); err != nil {
		t.Fatal(err)
	}
	if got := status(t, anna, "GET", ts.URL+"/api/me", ""); got != 401 {
		t.Fatalf("expired account: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/", ""); got != 302 {
		t.Fatalf("expired account's page: %d", got)
	}
}

func TestSignOutEverywhere(t *testing.T) {
	_, ts := newTestServer(t)
	here, there := login(t, ts, "anna"), login(t, ts, "anna")
	if got := status(t, here, "POST", ts.URL+"/api/me/sign-out-everywhere", ""); got != 403 {
		t.Fatalf("no origin: %d", got)
	}
	if got := status(t, here, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL); got != 204 {
		t.Fatalf("sign out everywhere: %d", got)
	}
	if got := status(t, there, "GET", ts.URL+"/api/me", ""); got != 401 {
		t.Fatalf("other browser still signed in: %d", got)
	}
	if got := status(t, here, "GET", ts.URL+"/api/me", ""); got != 200 {
		t.Fatalf("this browser signed out: %d", got)
	}
}

// The session of an account that expires never outlives it.
func TestSessionCookieEndsWithTheAccount(t *testing.T) {
	s, ts := newTestServer(t)
	until := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "temp", Password: "pw", Expires: &until}); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "temp")
	cookies := c.Jar.Cookies(nil)
	if len(cookies) != 1 || !cookies[0].Expires.Equal(until) {
		t.Fatalf("session cookie %v, want it to expire at %v", cookies, until)
	}
}

// Signing out everywhere closes the desktop streams of the account's browser
// sessions; connections held with a client credential stay.
func TestSignOutEverywhereClosesDesktopStreams(t *testing.T) {
	s, ts := newTestServer(t)
	here := login(t, ts, "admin")
	admin, _ := s.Accounts.Get("admin")
	stream, closeStream := context.WithCancel(context.Background())
	defer closeStream()
	cn := &conn{username: "admin", accountID: admin.ID, epoch: admin.SessionEpoch, computer: "alice", level: auth.LevelFull, cancel: closeStream}
	s.viewers.add("alice", cn)
	defer s.viewers.remove("alice", cn)
	watch, closeWatch := context.WithCancel(context.Background())
	defer closeWatch()
	cli := &conn{username: "admin", accountID: admin.ID, epoch: noSession, computer: "alice", level: auth.LevelFull, cancel: closeWatch}
	s.screenWatchers.add("alice", cli)
	defer s.screenWatchers.remove("alice", cli)
	if got := status(t, here, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL); got != 204 {
		t.Fatalf("sign out everywhere: %d", got)
	}
	if stream.Err() == nil {
		t.Fatal("a desktop stream of an ended browser session stayed open")
	}
	if watch.Err() != nil {
		t.Fatal("signing out everywhere closed a client credential's connection")
	}
}
