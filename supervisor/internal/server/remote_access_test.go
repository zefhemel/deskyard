package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// remoteEngine is a running "alice" whose Chromium is served on port and
// whose commands run until their context ends; `deskyard size` answers at
// once.
type remoteEngine struct {
	fakeEngine
	port  int
	execs atomic.Int32
}

func (e *remoteEngine) running() engine.Container {
	c := alice
	c.State = "running"
	c.IP = map[string]string{"deskyard-alice": "127.0.0.1"}
	return c
}

func (e *remoteEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	if c := e.running(); id == c.Name || id == c.ID {
		return c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

func (e *remoteEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return []engine.Container{e.running()}, nil
}

func (e *remoteEngine) Exec(ctx context.Context, _ string, spec engine.ExecSpec) (*engine.Process, error) {
	if len(spec.Cmd) == 2 && spec.Cmd[0] == "deskyard" && spec.Cmd[1] == "size" {
		return &engine.Process{Stdin: nopWriteCloser{}, Stdout: strings.NewReader("1280 800\n"),
			Wait: func() (int, error) { return 0, nil }, Close: func() {}}, nil
	}
	e.execs.Add(1)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go io.Copy(io.Discard, inR)
	stop := context.AfterFunc(ctx, func() { outW.Close() })
	return &engine.Process{Stdin: inW, Stdout: outR,
		Wait:  func() (int, error) { return 0, ctx.Err() },
		Close: func() { stop(); inW.Close(); outW.Close(); outR.Close() }}, nil
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(b []byte) (int, error) { return len(b), nil }
func (nopWriteCloser) Close() error                { return nil }

// newRemoteServer serves a running alice that anna may use fully and bob
// may observe.
func newRemoteServer(t *testing.T) (*Server, *httptest.Server, *remoteEngine) {
	t.Helper()
	e := &remoteEngine{}
	s, ts := newTestServerCfg(t, e, environments.Config{Subnet: "10.0.0.0/16", DesktopPort: e.port})
	observeAlice(t, s)
	return s, ts, e
}

func accountNamed(t *testing.T, s *Server, username string) accounts.Account {
	t.Helper()
	a, ok := s.Accounts.Get(username)
	if !ok {
		t.Fatalf("no account %s", username)
	}
	return a
}

// startExec opens an exec of a long-running command with the bearer and
// waits until the command runs.
func startExec(t *testing.T, ctx context.Context, ts *httptest.Server, e *remoteEngine, bearer string) *websocket.Conn {
	t.Helper()
	before := e.execs.Load()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/environments/alice/exec",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + bearer}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"argv":["sleep","100"]}`)); err != nil {
		t.Fatal(err)
	}
	for e.execs.Load() == before {
		if ctx.Err() != nil {
			t.Fatal("the command never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ws
}

// closedSoon fails the test unless the exec WebSocket ends before ctx.
func closedSoon(t *testing.T, ctx context.Context, ws *websocket.Conn, what string) {
	t.Helper()
	for {
		typ, b, err := ws.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatalf("%s left the exec open", what)
			}
			return
		}
		if typ == websocket.MessageText && strings.Contains(string(b), `"exit"`) {
			return
		}
	}
}

func TestRemoteExecNeedsCredential(t *testing.T) {
	s, ts, e := newRemoteServer(t)
	execURL := ts.URL + "/api/environments/alice/exec"

	if code := status(t, &http.Client{}, "GET", execURL, ""); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	resp, err := login(t, ts, "anna").Get(execURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(string(body), "use a client credential (deskyard login)") {
		t.Errorf("a session cookie: %d %s", resp.StatusCode, body)
	}
	if code, body, _ := bearerRequest(t, "GET", execURL, "dyc_00000000_nope"); code != 401 || !strings.Contains(body, "unknown, revoked or expired") {
		t.Errorf("a bad credential: %d %s", code, body)
	}

	_, bob := issueCredential(t, s, accountNamed(t, s, "bob"))
	if code, body, _ := bearerRequest(t, "GET", execURL, bob); code != 403 || !strings.Contains(body, "observe-only access can't do that") {
		t.Errorf("an observer: %d %s", code, body)
	}
	_, carol := issueCredential(t, s, createAccount(t, s, "carol"))
	if code, _, _ := bearerRequest(t, "GET", execURL, carol); code != 404 {
		t.Errorf("no grant: %d", code)
	}

	_, anna := issueCredential(t, s, accountNamed(t, s, "anna"))
	if code, _, _ := bearerRequest(t, "GET", execURL, anna); code != http.StatusUpgradeRequired && code != http.StatusBadRequest {
		t.Errorf("a full credential without a WebSocket upgrade: %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	startExec(t, ctx, ts, e, anna)

	for _, old := range []string{"/api/agent/v1/exec", "/api/agent/v1/", "/api/environments/alice/tokens"} {
		if code, _, _ := bearerRequest(t, "GET", ts.URL+old, anna); code != 404 {
			t.Errorf("removed route %s: %d", old, code)
		}
	}
}

func TestRevokingCredentialClosesExec(t *testing.T) {
	s, ts, e := newRemoteServer(t)
	c, anna := issueCredential(t, s, accountNamed(t, s, "anna"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws := startExec(t, ctx, ts, e, anna)
	if err := s.credentials.remove(func(o credential) bool { return o.ID == c.ID }); err != nil {
		t.Fatal(err)
	}
	closedSoon(t, ctx, ws, "deleting the credential")
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/status", anna); code != 401 {
		t.Errorf("a deleted credential: %d", code)
	}
}

func TestRemovingGrantClosesExec(t *testing.T) {
	for name, grant := range map[string]map[string]auth.Level{
		"removed": nil,
		"observe": {"anna": auth.LevelObserve},
	} {
		t.Run(name, func(t *testing.T) {
			s, ts, e := newRemoteServer(t)
			_, anna := issueCredential(t, s, accountNamed(t, s, "anna"))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ws := startExec(t, ctx, ts, e, anna)
			if err := s.Accounts.SetAccess("alice", grant); err != nil {
				t.Fatal(err)
			}
			closedSoon(t, ctx, ws, "a lowered grant")
			if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/exec", anna); code != 403 && code != 404 {
				t.Errorf("after the change: %d", code)
			}
		})
	}
}

// A change while the environment is looked up, before the connection is
// registered, still refuses it.
func TestRemoteAccessRechecksAfterRegistering(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(s *Server, c credential) error
		want   int
	}{
		{"grant removed", func(s *Server, _ credential) error { return s.Accounts.SetAccess("alice", nil) }, 404},
		{"credential deleted", func(s *Server, c credential) error {
			return s.credentials.remove(func(o credential) bool { return o.ID == c.ID })
		}, 401},
		{"account deleted", func(s *Server, _ credential) error { return s.Accounts.Delete("anna") }, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
				t.Fatal(err)
			}
			c, bearer := issueCredential(t, s, accountNamed(t, s, "anna"))
			s.Eng = &accessChangeEngine{change: func() {
				if err := tc.change(s, c); err != nil {
					t.Error(err)
				}
			}}
			s.Environments.Eng = s.Eng
			req := httptest.NewRequest(http.MethodGet, "/api/environments/alice/exec", nil)
			req.Header.Set("Authorization", "Bearer "+bearer)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("changed during lookup: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// accessChangeEngine runs change on the first lookup of alice (running).
type accessChangeEngine struct {
	fakeEngine
	change func()
}

func (e *accessChangeEngine) lookup() engine.Container {
	if e.change != nil {
		f := e.change
		e.change = nil
		f()
	}
	c := alice
	c.State = "running"
	return c
}

func (e *accessChangeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return []engine.Container{e.lookup()}, nil
}

func (e *accessChangeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	if c := e.lookup(); id == c.Name || id == c.ID {
		return c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

func TestRemoteStatus(t *testing.T) {
	s, ts, _ := newRemoteServer(t)
	_, anna := issueCredential(t, s, accountNamed(t, s, "anna"))
	code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/status", anna)
	var st struct {
		Name, State string
		API         int
		Screen      *struct{ Width, Height int }
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil || code != 200 {
		t.Fatalf("status: %d %s", code, body)
	}
	if st.Name != "alice" || st.State != "running" || st.API != 1 || st.Screen == nil || st.Screen.Width != 1280 {
		t.Errorf("status: %s", body)
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/beta/status", anna); code != 404 {
		t.Errorf("an unknown environment: %d", code)
	}
}

func TestRemoteRoutesNeedRunningEnvironment(t *testing.T) {
	s, ts := newTestServer(t) // alice is stopped
	_, admin := issueCredential(t, s, accountNamed(t, s, "admin"))
	for _, route := range []string{"/exec", "/forward", "/cdp", "/cdp/json/list"} {
		code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice"+route, admin)
		if code != 409 || !strings.Contains(body, "alice is stopped; someone with access can start it") {
			t.Errorf("%s on a stopped environment: %d %s", route, code, body)
		}
	}
	if code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/status", admin); code != 200 || !strings.Contains(body, `"screen":null`) {
		t.Errorf("status of a stopped environment: %d %s", code, body)
	}
}

func TestRemoteCDPRewritesURLs(t *testing.T) {
	var paths atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths.Store(r.URL.Path + " auth=" + r.Header.Get("Authorization") + " cookie=" + r.Header.Get("Cookie"))
		fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://localhost/devtools/browser/x","devtoolsFrontendUrl":"/devtools/inspector.html?ws=localhost/devtools/page/y"}`)
	}))
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	e := &remoteEngine{port: port}
	s, ts := newTestServerCfg(t, e, environments.Config{Subnet: "10.0.0.0/16", DesktopPort: port})
	_, admin := issueCredential(t, s, accountNamed(t, s, "admin"))
	host := strings.TrimPrefix(ts.URL, "http://")
	for route, upstreamPath := range map[string]string{"/cdp": "/_cdp/json/version", "/cdp/json/list": "/_cdp/json/list"} {
		code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice"+route, admin)
		if code != 200 {
			t.Fatalf("%s: %d %s", route, code, body)
		}
		if got := paths.Load().(string); got != upstreamPath+" auth= cookie=" {
			t.Errorf("%s reached Chromium as %q", route, got)
		}
		if !strings.Contains(body, `"ws://`+host+`/api/environments/alice/cdp/devtools/browser/x"`) ||
			!strings.Contains(body, `ws=`+host+`/api/environments/alice/cdp/devtools/page/y`) {
			t.Errorf("%s URLs not rewritten: %s", route, body)
		}
	}
	if p := s.activity.Present("alice"); len(p) != 1 || p[0].Owner != "admin" || p[0].Name != "deskyard CLI on test" {
		t.Errorf("CDP use not recorded under the credential: %#v", p)
	}
}
