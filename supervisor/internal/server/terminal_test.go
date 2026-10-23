package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

type terminalEngine struct {
	fakeEngine
	calls     atomic.Int32
	spec      chan engine.ExecSpec
	input     chan string
	resize    chan [2]uint
	closed    chan struct{}
	outR      *io.PipeReader
	outW      *io.PipeWriter
	state     string
	supported bool
}

func newTerminalEngine() *terminalEngine {
	r, w := io.Pipe()
	return &terminalEngine{spec: make(chan engine.ExecSpec, 1), input: make(chan string, 8), resize: make(chan [2]uint, 8), closed: make(chan struct{}), outR: r, outW: w, state: "running", supported: true}
}
func (e *terminalEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.fakeEngine.Inspect(ctx, id)
	c.State = e.state
	return c, err
}
func (e *terminalEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	labels := map[string]string{}
	if e.supported {
		labels["deskyard.terminal"] = "tmux-v1"
	}
	return engine.Image{Labels: labels}, nil
}

type terminalInput struct{ e *terminalEngine }

func (i terminalInput) Write(b []byte) (int, error) {
	select {
	case i.e.input <- string(b):
		return len(b), nil
	case <-i.e.closed:
		return 0, io.ErrClosedPipe
	}
}
func (i terminalInput) Close() error { return nil }
func (e *terminalEngine) Exec(ctx context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	e.calls.Add(1)
	if len(s.Cmd) > 1 && s.Cmd[1] == "terminal-lease" {
		return &engine.Process{Stdin: terminalDiscardInput{}, Stdout: strings.NewReader("prepared\nready\n"), Wait: func() (int, error) { return 0, nil }, Close: func() {}}, nil
	}
	e.spec <- s
	var once sync.Once
	closeIt := func() { once.Do(func() { close(e.closed); e.outR.Close(); e.outW.Close() }) }
	go func() {
		select {
		case <-ctx.Done():
			closeIt()
		case <-e.closed:
		}
	}()
	return &engine.Process{Stdin: terminalInput{e}, Stdout: io.MultiReader(strings.NewReader("\x1b"), e.outR), Resize: func(c, r uint) error { e.resize <- [2]uint{c, r}; return nil }, Wait: func() (int, error) { return 0, nil }, Close: closeIt}, nil
}
func terminalDial(t *testing.T, ts *httptest.Server, c *http.Client) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/desktops/alice/terminal", &websocket.DialOptions{HTTPClient: c, HTTPHeader: http.Header{"Origin": []string{ts.URL}}})
	if err != nil {
		t.Fatalf("dial %v %v", resp, err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}
func terminalRead(t *testing.T, ws *websocket.Conn) (websocket.MessageType, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	typ, b, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return typ, string(b)
}
func terminalWrite(t *testing.T, ws *websocket.Conn, typ websocket.MessageType, b string) {
	t.Helper()
	if err := ws.Write(context.Background(), typ, []byte(b)); err != nil {
		t.Fatal(err)
	}
}
func terminalReady(t *testing.T, ws *websocket.Conn) {
	terminalWrite(t, ws, websocket.MessageText, `{"cols":80,"rows":24}`)
	_, b := terminalRead(t, ws)
	if b != `{"generation":"c1:0001-01-01T00:00:00Z","ready":true}` {
		t.Fatalf("ready %q", b)
	}
	typ, b := terminalRead(t, ws)
	if typ != websocket.MessageBinary || b != "\x1b" {
		t.Fatalf("startup output %q", b)
	}
	terminalWrite(t, ws, websocket.MessageText, `{"ack":1}`)
}
func TestTerminalHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		name, user, origin, session, state string
		supported                          bool
		want                               int
	}{
		{"anonymous", "", "same", "", "running", true, 401}, {"missing origin", "admin", "", "", "running", true, 403}, {"wrong origin", "admin", "http://elsewhere", "", "running", true, 403}, {"invalid scheme", "admin", "ftp", "", "running", true, 403}, {"path origin", "admin", "path", "", "running", true, 403}, {"no access", "anna", "same", "", "running", true, 404}, {"bad name", "admin", "same", "a.b", "running", true, 400}, {"stopped", "admin", "same", "", "exited", true, 409}, {"old image", "admin", "same", "", "running", false, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTerminalEngine()
			e.state = tc.state
			e.supported = tc.supported
			_, ts := newTestServerWith(t, e)
			c := ts.Client()
			if tc.user != "" {
				c = login(t, ts, tc.user)
			}
			origin := tc.origin
			switch origin {
			case "same":
				origin = ts.URL
			case "ftp":
				origin = "ftp" + strings.TrimPrefix(ts.URL, "http")
			case "path":
				origin = ts.URL + "/bad"
			}
			if got := status(t, c, "GET", ts.URL+"/api/desktops/alice/terminal?session="+tc.session, origin); got != tc.want {
				t.Fatalf("status %d want %d", got, tc.want)
			}
			if e.calls.Load() != 0 {
				t.Fatal("unauthorized exec")
			}
		})
	}
}
func TestTerminalWireAndExit(t *testing.T) {
	e := newTerminalEngine()
	s, ts := newTestServerWith(t, e)
	ws := terminalDial(t, ts, login(t, ts, "admin"))
	terminalReady(t, ws)
	spec := <-e.spec
	if len(spec.Cmd) != 3 || spec.Cmd[0] != "deskyard" || spec.Cmd[1] != "terminal-attach" || !terminal.ValidAttachment(spec.Cmd[2]) || spec.User != "user" || spec.WorkDir != "/workspace" || !spec.Tty || !spec.Stdin || spec.Cols != 80 || spec.Rows != 24 {
		t.Fatalf("spec %+v", spec)
	}
	if !strings.Contains(strings.Join(spec.Env, " "), "DESKYARD_ACTIVITY=off") {
		t.Fatal(spec.Env)
	}
	if n, _ := s.viewers.snapshot("alice"); n != 1 {
		t.Fatal("missing viewer")
	}
	terminalWrite(t, ws, websocket.MessageBinary, "\x00hello\x03")
	if b := <-e.input; b != "\x00hello\x03" {
		t.Fatal("input changed")
	}
	go func() { e.outW.Write([]byte("\x1b[31mhello")) }()
	typ, b := terminalRead(t, ws)
	if typ != websocket.MessageBinary || b != "\x1b[31mhello" {
		t.Fatal("output changed")
	}
	terminalWrite(t, ws, websocket.MessageText, `{"ack":10}`)
	terminalWrite(t, ws, websocket.MessageText, `{"resize":[100,30]}`)
	select {
	case sz := <-e.resize:
		if sz != [2]uint{100, 30} {
			t.Fatal(sz)
		}
	case <-time.After(time.Second):
		t.Fatal("no resize")
	}
	e.outW.Close()
	_, b = terminalRead(t, ws)
	if b != `{"exit":0}` {
		t.Fatal(b)
	}
}
func TestTerminalInvalidHandshake(t *testing.T) {
	for _, b := range []string{`{"cols":1,"rows":24}`, `{"cols":80,"rows":301}`, `{"cols":80,"rows":24,"argv":["sh"]}`} {
		t.Run(b, func(t *testing.T) {
			e := newTerminalEngine()
			_, ts := newTestServerWith(t, e)
			ws := terminalDial(t, ts, login(t, ts, "admin"))
			terminalWrite(t, ws, websocket.MessageText, b)
			_, msg := terminalRead(t, ws)
			if !strings.Contains(msg, "error") {
				t.Fatal(msg)
			}
			if e.calls.Load() != 0 {
				t.Fatal("exec before valid size")
			}
		})
	}
}
func TestTerminalCancellation(t *testing.T) {
	for _, how := range []string{"disconnect", "logout", "password", "stop", "malformed ack"} {
		t.Run(how, func(t *testing.T) {
			e := newTerminalEngine()
			s, ts := newTestServerWith(t, e)
			client := login(t, ts, "admin")
			ws := terminalDial(t, ts, client)
			terminalReady(t, ws)
			switch how {
			case "disconnect":
				ws.CloseNow()
			case "logout":
				if got := status(t, client, "POST", ts.URL+"/api/logout", ts.URL); got != 204 {
					t.Fatal(got)
				}
			case "password":
				s.Accounts.SetPassword("admin", "new")
			case "stop":
				s.viewers.closeAll("alice")
			case "malformed ack":
				terminalWrite(t, ws, websocket.MessageText, `{"ack":1}`)
			}
			select {
			case <-e.closed:
			case <-time.After(3 * time.Second):
				t.Fatal("process not canceled")
			}
			deadline := time.Now().Add(time.Second)
			for {
				n, _ := s.viewers.snapshot("alice")
				if n == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("viewer leak")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestTerminalCapabilityJSON(t *testing.T) {
	for _, supported := range []bool{true, false} {
		for _, state := range []string{"running", "exited"} {
			e := newTerminalEngine()
			e.supported = supported
			e.state = state
			s, ts := newTestServerWith(t, e)
			c, err := s.Desktops.Get(context.Background(), "alice")
			if err != nil {
				t.Fatal(err)
			}
			a, _ := s.Accounts.Get("admin")
			b, _ := json.Marshal(s.desktopJSON(context.Background(), c, a))
			var v map[string]any
			json.Unmarshal(b, &v)
			if v["terminal"] != supported {
				t.Fatalf("capability %s %s", state, b)
			}
			_ = ts
		}
	}
}
func TestTerminalRuntimeGeneration(t *testing.T) {
	first := desktops.Desktop{Container: engine.Container{ID: "same"}, StartedAt: time.Unix(1, 0)}
	next := first
	next.StartedAt = time.Unix(2, 0)
	if terminalGeneration(first) == terminalGeneration(next) {
		t.Fatal("restart kept generation")
	}
	if terminalGeneration(first) != terminalGeneration(first) {
		t.Fatal("unstable generation")
	}
}
func TestTerminalCookieLogoutIsolation(t *testing.T) {
	e := newTerminalEngine()
	s, ts := newTestServerWith(t, e)
	first := login(t, ts, "admin")
	second := login(t, ts, "admin")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(first.Jar.Cookies(nil)[0])
	claims, _ := s.Sessions.Claims(req)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := &terminalConnection{claims: claims, desktopID: "id-alice", cancel: cancel}
	if !s.terminals.add(live) {
		t.Fatal("register")
	}
	defer s.terminals.remove(live)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(second.Jar.Cookies(nil)[0])
	claims2, _ := s.Sessions.Claims(req2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	other := &terminalConnection{claims: claims2, desktopID: "id-alice", cancel: cancel2}
	s.terminals.add(other)
	defer s.terminals.remove(other)
	rec := httptest.NewRecorder()
	s.Sessions.Refresh(rec, claims)
	refreshed := &http.Client{Jar: &cookieJar{cookies: rec.Result().Cookies()}}
	if got := status(t, refreshed, "POST", ts.URL+"/api/logout", ts.URL); got != 204 {
		t.Fatal(got)
	}
	if ctx.Err() == nil || ctx2.Err() != nil {
		t.Fatal("logout not isolated")
	}
	if s.terminals.add(&terminalConnection{claims: claims, desktopID: "id-alice", cancel: func() {}}) {
		t.Fatal("logout race permits new connection")
	}
}
func TestTerminalExpiry(t *testing.T) {
	e := newTerminalEngine()
	s, ts := newTestServerWith(t, e)
	// Issue an otherwise valid cookie whose absolute expiry is shortly ahead.
	s.Sessions.Now = func() time.Time { return time.Now().Add(-30*24*time.Hour + 2*time.Second) }
	rec := httptest.NewRecorder()
	s.Sessions.Issue(rec, "admin", 0)
	s.Sessions.Now = nil
	a, _ := s.Accounts.Get("admin")
	if a.SessionEpoch != 0 {
		t.Fatal("unexpected test epoch")
	}
	client := &http.Client{Jar: &cookieJar{cookies: rec.Result().Cookies()}}
	ws := terminalDial(t, ts, client)
	terminalReady(t, ws)
	select {
	case <-e.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("expiry did not cancel")
	}
}
func TestTerminalOriginProxyBoundary(t *testing.T) {
	s := &Server{Sessions: &auth.Sessions{}}
	for _, tc := range []struct {
		origin string
		secure bool
		want   bool
	}{{"https://example.com", false, true}, {"http://example.com", false, true}, {"http://example.com", true, false}, {"https://example.com", true, true}, {"https://evil@example.com", false, false}, {"https://example.com#x", false, false}, {"https://example.com?", false, false}} {
		s.Sessions.Secure = tc.secure
		r := httptest.NewRequest("GET", "http://example.com/", nil)
		r.Header.Set("Origin", tc.origin)
		if s.terminalOrigin(r) != tc.want {
			t.Fatal(tc)
		}
	}
}

func TestTerminalOutputCreditKeepsACKReaderResponsive(t *testing.T) {
	e := newTerminalEngine()
	e.input = make(chan string)
	_, ts := newTestServerWith(t, e)
	ws := terminalDial(t, ts, login(t, ts, "admin"))
	terminalReady(t, ws)
	terminalWrite(t, ws, websocket.MessageBinary, "blocked stdin")
	go func() { _, _ = e.outW.Write(bytes.Repeat([]byte("x"), 17*(16<<10))) }()
	for i := 0; i < 16; i++ {
		typ, b := terminalRead(t, ws)
		if typ != websocket.MessageBinary || len(b) != 16<<10 {
			t.Fatalf("chunk %d len %d", i, len(b))
		}
	}
	next := make(chan string, 1)
	go func() {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			next <- "error"
		} else {
			next <- string(b)
		}
	}()
	select {
	case <-next:
		t.Fatal("output exceeded credit")
	case <-time.After(30 * time.Millisecond):
	}
	terminalWrite(t, ws, websocket.MessageText, `{"ack":16384}`)
	select {
	case b := <-next:
		if len(b) != 16<<10 {
			t.Fatal("missing released output")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked stdin stalled ACK")
	}
	ws.CloseNow()
	select {
	case <-e.closed:
	case <-time.After(time.Second):
		t.Fatal("blocked input/output prevented cancellation")
	}
}
func TestTerminalAccessRevocationAndShutdown(t *testing.T) {
	for _, how := range []string{"revoke", "shutdown"} {
		t.Run(how, func(t *testing.T) {
			e := newTerminalEngine()
			s, ts := newTestServerWith(t, e)
			s.Accounts.SetAccess("id-alice", []string{"anna"})
			ws := terminalDial(t, ts, login(t, ts, "anna"))
			terminalReady(t, ws)
			if how == "revoke" {
				s.Accounts.SetAccess("id-alice", nil)
			} else {
				s.terminals.shutdown()
			}
			select {
			case <-e.closed:
			case <-time.After(time.Second):
				t.Fatal("connection retained")
			}
		})
	}
}
func TestTerminalLogoutOrigin(t *testing.T) {
	_, ts := newTestServer(t)
	c := login(t, ts, "admin")
	for _, o := range []string{"", "https://evil.test"} {
		if got := status(t, c, "POST", ts.URL+"/api/logout", o); got != 403 {
			t.Fatal(got)
		}
	}
}

func TestTerminalRefusesLifecycleJoin(t *testing.T) {
	e := newTerminalEngine()
	s, ts := newTestServerWith(t, e)
	if err := s.Desktops.Begin("alice", ""); err != nil {
		t.Fatal(err)
	}
	defer s.Desktops.Done("alice")
	if got := status(t, login(t, ts, "admin"), "GET", ts.URL+"/api/desktops/alice/terminal", ts.URL); got != 409 {
		t.Fatalf("join during stop got %d", got)
	}
	if e.calls.Load() != 0 {
		t.Fatal("exec during lifecycle operation")
	}
}

func TestTerminalOversizeInput(t *testing.T) {
	e := newTerminalEngine()
	_, ts := newTestServerWith(t, e)
	ws := terminalDial(t, ts, login(t, ts, "admin"))
	terminalReady(t, ws)
	_ = ws.Write(context.Background(), websocket.MessageBinary, bytes.Repeat([]byte("x"), (64<<10)+1))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("oversized input accepted")
	}
	select {
	case <-e.closed:
	case <-time.After(time.Second):
		t.Fatal("process retained")
	}
	select {
	case <-e.input:
		t.Fatal("oversized input forwarded")
	default:
	}
}

type terminalDiscardInput struct{}

func (terminalDiscardInput) Write(b []byte) (int, error) { return len(b), nil }
func (terminalDiscardInput) Close() error                { return nil }

func TestTerminalSessionRecognition(t *testing.T) {
	for _, argv := range [][]string{{"deskyard", "terminal-helper", "main"}, {"deskyard", "terminal-helper", "build-2"}} {
		if _, ok := terminalSession(argv, true); !ok {
			t.Fatal(argv)
		}
	}
	for _, argv := range [][]string{{"sh", "-c", "deskyard terminal-helper main"}, {"deskyard", "terminal-helper", "main", "extra"}, {"deskyard", "terminal-helper", "bad.name"}, {"/bin/deskyard", "terminal-helper", "main"}} {
		if _, ok := terminalSession(argv, true); ok {
			t.Fatal(argv)
		}
	}
	if _, ok := terminalSession([]string{"deskyard", "terminal-helper", "main"}, false); ok {
		t.Fatal("non-TTY generic exec hijacked")
	}
}
