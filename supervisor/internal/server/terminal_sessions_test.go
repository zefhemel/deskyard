package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

// terminalSessionsEngine adds the session helpers to the terminal engine.
type terminalSessionsEngine struct {
	*terminalEngine
	mu       sync.Mutex
	list     string
	created  string
	killCode int
	onKill   func() // runs as the session ends, before the helper returns
	listHook func() // runs during each list
	cmds     [][]string
}

func newTerminalSessionsEngine() *terminalSessionsEngine {
	return &terminalSessionsEngine{terminalEngine: newTerminalEngine(), created: "terminal-2\n"}
}

func (e *terminalSessionsEngine) Exec(ctx context.Context, id string, s engine.ExecSpec) (*engine.Process, error) {
	if len(s.Cmd) < 2 || s.Cmd[0] != "deskyard" {
		panic("unexpected exec")
	}
	out, code := "", 0
	e.mu.Lock()
	switch s.Cmd[1] {
	case "terminal-list":
		out = e.list
		if e.listHook != nil {
			defer e.listHook()
		}
	case "terminal-rename":
		code = e.killCode
	case "terminal-create":
		out = e.created
	case "terminal-kill":
		code = e.killCode
		if e.onKill != nil {
			e.onKill()
		}
	default:
		e.mu.Unlock()
		return e.terminalEngine.Exec(ctx, id, s)
	}
	e.calls.Add(1)
	e.cmds = append(e.cmds, s.Cmd)
	e.mu.Unlock()
	if s.User != "user" || s.Tty || s.Stdin || s.WorkDir != "/workspace" {
		panic("unsafe session helper invocation")
	}
	return &engine.Process{Stdout: strings.NewReader(out), Stderr: strings.NewReader(""), Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
}

func (e *terminalSessionsEngine) ran(argv ...string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.ContainsFunc(e.cmds, func(c []string) bool { return slices.Equal(c, argv) })
}

func (e *terminalSessionsEngine) setList(out string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = out
}

func computerTerminals(t *testing.T, c *http.Client, url string) []terminal.Session {
	t.Helper()
	resp, body := get(t, c, url+"/api/computers/alice")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var v struct {
		Terminals *[]terminal.Session `json:"terminals"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Terminals == nil {
		t.Fatalf("terminals missing or null: %s", body)
	}
	return *v.Terminals
}

func TestTerminalSessionsJSONCarriesDetails(t *testing.T) {
	e := newTerminalSessionsEngine()
	e.list = "main\t3\tvim\nterminal-1\t0\tbash\n_deskyard_" + strings.Repeat("a", 32) + "\t1\tbash\n"
	_, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	resp, body := get(t, admin, ts.URL+"/api/computers/alice/terminal/sessions")
	if resp.StatusCode != 200 || strings.TrimSpace(body) != `{"sessions":[{"name":"main","clients":3,"command":"vim"},{"name":"terminal-1","clients":0,"command":"bash"}]}` {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	want := []terminal.Session{{Name: "main", Clients: 3, Command: "vim"}, {Name: "terminal-1", Clients: 0, Command: "bash"}}
	if got := computerTerminals(t, admin, ts.URL); !slices.Equal(got, want) {
		t.Fatalf("computer JSON terminals %+v", got)
	}
}

func TestCreateSessionRoute(t *testing.T) {
	for _, tc := range []struct {
		name, user, state, created string
		want                       int
	}{
		{"created", "admin", "running", "terminal-2\n", 201},
		{"stopped", "admin", "exited", "", 409},
		{"no access", "anna", "running", "", 404},
		{"bad helper output", "admin", "running", "bad.name\n", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTerminalSessionsEngine()
			e.state = tc.state
			e.created = tc.created
			_, ts := newTestServerWith(t, e)
			req, _ := http.NewRequest("POST", ts.URL+"/api/computers/alice/terminal/sessions", nil)
			req.Header.Set("Origin", ts.URL)
			resp, err := login(t, ts, tc.user).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.want {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			if tc.want == 201 && (strings.TrimSpace(string(b)) != `{"name":"terminal-2"}` || !e.ran("deskyard", "terminal-create")) {
				t.Fatalf("created %s", b)
			}
			if tc.want != 201 && tc.want != 502 && e.calls.Load() != 0 {
				t.Fatal("exec without permission or a running desktop")
			}
		})
	}
}

func TestDeleteSessionRoute(t *testing.T) {
	for _, tc := range []struct {
		name, user, state, session string
		code, want                 int
	}{
		{"ended", "admin", "running", "main", 0, 204},
		{"missing", "admin", "running", "main", 3, 404},
		{"stopped", "admin", "exited", "main", 0, 409},
		{"invalid name", "admin", "running", "a.b", 0, 400},
		{"no access", "anna", "running", "main", 0, 404},
		{"failed", "admin", "running", "main", 1, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTerminalSessionsEngine()
			e.state = tc.state
			e.killCode = tc.code
			_, ts := newTestServerWith(t, e)
			if got := status(t, login(t, ts, tc.user), "DELETE", ts.URL+"/api/computers/alice/terminal/sessions/"+tc.session, ts.URL); got != tc.want {
				t.Fatalf("status %d want %d", got, tc.want)
			}
			ran := e.ran("deskyard", "terminal-kill", tc.session)
			if ran != (tc.want == 204 || tc.want == 404 && tc.code == 3 || tc.want == 502) {
				t.Fatalf("kill ran %v", ran)
			}
		})
	}
}

// Ending a session closes this supervisor's browser attachments to it with
// "ended", whether or not their tmux client has exited first, and leaves
// attachments to other sessions alone.
func TestDeleteSessionClosesAttachments(t *testing.T) {
	for _, clientExits := range []bool{false, true} {
		t.Run(map[bool]string{false: "attached", true: "client exits first"}[clientExits], func(t *testing.T) {
			e := newTerminalSessionsEngine()
			_, ts := newTestServerWith(t, e)
			admin := login(t, ts, "admin")
			ws := terminalDial(t, ts, admin)
			terminalReady(t, ws)
			if got := status(t, admin, "DELETE", ts.URL+"/api/computers/alice/terminal/sessions/other", ts.URL); got != 204 {
				t.Fatal(got)
			}
			if clientExits {
				// tmux detaches the browser's client as its session is killed.
				e.onKill = func() { e.outW.Close(); time.Sleep(50 * time.Millisecond) }
			}
			if got := status(t, admin, "DELETE", ts.URL+"/api/computers/alice/terminal/sessions/main", ts.URL); got != 204 {
				t.Fatal(got)
			}
			typ, b := terminalRead(t, ws)
			if typ != websocket.MessageText || b != `{"ended":true}` {
				t.Fatalf("got %q", b)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("close %v", err)
			}
			select {
			case <-e.closed:
			case <-time.After(3 * time.Second):
				t.Fatal("attachment process retained")
			}
		})
	}
}

func TestAttachJoinOnlyByDefault(t *testing.T) {
	for _, tc := range []struct {
		query string
		join  bool
	}{{"session=main", true}, {"session=main&create=1", false}, {"session=build-2&create=0", true}} {
		t.Run(tc.query, func(t *testing.T) {
			e := newTerminalEngine()
			_, ts := newTestServerWith(t, e)
			ws := terminalDialQuery(t, ts, login(t, ts, "admin"), tc.query)
			terminalReady(t, ws)
			argv := <-e.leases
			if len(argv) < 4 || argv[1] != "terminal-lease" || !terminal.ValidAttachment(argv[3]) {
				t.Fatal(argv)
			}
			if got := len(argv) == 5 && argv[4] == "join"; got != tc.join || len(argv) > 5 || !tc.join && len(argv) != 4 {
				t.Fatalf("join %v: %v", tc.join, argv)
			}
		})
	}
	t.Run("missing session", func(t *testing.T) {
		e := newTerminalEngine()
		e.leaseOut = "ended\n"
		_, ts := newTestServerWith(t, e)
		ws := terminalDial(t, ts, login(t, ts, "admin"))
		terminalWrite(t, ws, websocket.MessageText, `{"cols":80,"rows":24}`)
		if _, b := terminalRead(t, ws); b != `{"ended":true}` {
			t.Fatalf("got %q", b)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
			t.Fatalf("close %v", err)
		}
		select {
		case s := <-e.spec:
			t.Fatalf("attached to a missing session: %v", s.Cmd)
		default:
		}
	})
}

func TestTerminalCacheClearedWhenStopped(t *testing.T) {
	e := newTerminalSessionsEngine()
	e.list = "main\t1\tbash\n"
	s, ts := newTestServerWith(t, e)
	ctx := context.Background()
	s.terminalSessions.poll(ctx)
	if got := s.terminalSessions.get("alice"); len(got) != 1 || got[0].Name != "main" {
		t.Fatalf("running %+v", got)
	}
	// A restart is a new lifetime: its predecessor's sessions are gone.
	c, err := s.Computers.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	c.StartedAt = c.StartedAt.Add(time.Second)
	if got := s.terminalSessions.forComputer(c); len(got) != 0 {
		t.Fatalf("restarted %+v", got)
	}
	e.state = "exited"
	calls := e.calls.Load()
	s.terminalSessions.poll(ctx)
	if got := s.terminalSessions.get("alice"); len(got) != 0 {
		t.Fatalf("stopped %+v", got)
	}
	if e.calls.Load() != calls {
		t.Fatal("polled a stopped computer")
	}
	if got := computerTerminals(t, login(t, ts, "admin"), ts.URL); len(got) != 0 {
		t.Fatalf("stopped JSON %+v", got)
	}
}

func TestTerminalCachePollsOnlyCapableImages(t *testing.T) {
	e := newTerminalSessionsEngine()
	e.supported = false
	e.list = "main\t1\tbash\n"
	s, _ := newTestServerWith(t, e)
	s.terminalSessions.poll(context.Background())
	if e.calls.Load() != 0 || len(s.terminalSessions.get("alice")) != 0 {
		t.Fatal("polled an image without shared terminals")
	}
}

func TestTerminalCachePublishesOnlyChanges(t *testing.T) {
	e := newTerminalSessionsEngine()
	s, _ := newTestServerWith(t, e)
	sub := &subscriber{username: "admin", c: make(chan event, 16), refresh: make(chan struct{}, 1), overflow: make(chan struct{}, 1)}
	s.events.mu.Lock()
	s.events.subs[sub] = true
	s.events.mu.Unlock()
	published := func() int {
		n := 0
		for {
			select {
			case ev := <-sub.c:
				if ev.kind == "computer" && ev.computer == "alice" {
					n++
				}
			default:
				return n
			}
		}
	}
	ctx := context.Background()
	s.terminalSessions.poll(ctx)
	if n := published(); n != 0 {
		t.Fatalf("empty list published %d", n)
	}
	e.setList("main\t0\tbash\n")
	s.terminalSessions.poll(ctx)
	s.terminalSessions.poll(ctx)
	if n := published(); n != 1 {
		t.Fatalf("one change published %d", n)
	}
	e.setList("main\t1\tbash\n")
	s.terminalSessions.poll(ctx)
	if n := published(); n != 1 {
		t.Fatalf("client change published %d", n)
	}
	// A restart keeping the container ID lists equal sessions: pages showed
	// none for the new lifetime, so they must hear about them.
	c, err := s.Computers.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	c.StartedAt = c.StartedAt.Add(time.Minute)
	if _, err := s.terminalSessions.refresh(ctx, c); err != nil {
		t.Fatal(err)
	}
	if n := published(); n != 1 {
		t.Fatalf("restart with an equal list published %d", n)
	}
	if got := s.terminalSessions.forComputer(c); len(got) != 1 {
		t.Fatalf("restarted lifetime %+v", got)
	}
}

// Members of an ending are the attachments registered before it finished:
// those present, and joins made while it ran, but not creating attachments,
// which may start a new session of the name, nor anything registered after.
func TestTerminalEndingMembership(t *testing.T) {
	conns := newTerminalConnections()
	attach := func(user, session string, join bool) *terminalConnection {
		c := &terminalConnection{claims: auth.SessionClaims{ID: user, Username: user}, computer: "alice", session: session, join: join, cancel: func() {}}
		if !conns.add(c) {
			t.Fatal("register")
		}
		return c
	}
	before := attach("a", "main", true)
	other := attach("b", "other", true)
	e := conns.beginEnding("alice", "main")
	joined := attach("c", "main", true)
	created := attach("d", "main", false)
	e.finish(true)
	after := attach("e", "main", true)
	for _, tc := range []struct {
		name  string
		c     *terminalConnection
		ended bool
	}{{"before", before, true}, {"other session", other, false}, {"joined meanwhile", joined, true}, {"created meanwhile", created, false}, {"after", after, false}} {
		if isClosed(tc.c.end) != tc.ended || tc.c.ending.Load() != 0 {
			t.Errorf("%s: ended %v, ending %d", tc.name, isClosed(tc.c.end), tc.c.ending.Load())
		}
	}

	// A failed ending wakes its waiters at once, without ending them.
	waiting := attach("f", "main", true)
	failed := conns.beginEnding("alice", "main")
	result := make(chan bool, 1)
	go func() { result <- waiting.waitEnding(context.Background()) }()
	failed.finish(false)
	select {
	case ended := <-result:
		if ended {
			t.Fatal("aborted ending reported as ended")
		}
	case <-time.After(time.Second):
		t.Fatal("aborted ending left its waiter waiting")
	}
}

// The kill runs to completion even when the requesting page goes away, and
// attachments are still told that the session ended.
func TestDeleteSessionOutlivesRequest(t *testing.T) {
	e := newTerminalSessionsEngine()
	_, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	ws := terminalDial(t, ts, admin)
	terminalReady(t, ws)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.onKill = func() { cancel(); time.Sleep(100 * time.Millisecond) }
	req, _ := http.NewRequestWithContext(ctx, "DELETE", ts.URL+"/api/computers/alice/terminal/sessions/main", nil)
	req.Header.Set("Origin", ts.URL)
	if resp, err := admin.Do(req); err == nil {
		resp.Body.Close()
	}
	if typ, b := terminalRead(t, ws); typ != websocket.MessageText || b != `{"ended":true}` {
		t.Fatalf("got %q", b)
	}
}

// A poll lists at most a few computers at a time.
func TestTerminalCachePollBounded(t *testing.T) {
	e := newTerminalSessionsEngine()
	for i := range 9 {
		name := fmt.Sprintf("env%d", i)
		e.others = append(e.others, engine.Container{ID: "o" + name, Name: "deskyard-" + name, State: "running", ImageID: "sha256:abc",
			Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: name, computers.LabelRevision: "1"}})
	}
	var running, peak atomic.Int32
	e.listHook = func() {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
	}
	s, _ := newTestServerWith(t, e)
	s.terminalSessions.poll(context.Background())
	if got := e.calls.Load(); got != 10 {
		t.Fatalf("listed %d computers", got)
	}
	if p := peak.Load(); p > terminalSessionsParallel || p < 2 {
		t.Fatalf("peak concurrency %d", p)
	}
}

func TestTerminalCacheStopsWithContext(t *testing.T) {
	e := newTerminalSessionsEngine()
	s, _ := newTestServerWith(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.terminalSessions.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("poller outlived its context")
	}
}

func TestComputerJSONHasTerminals(t *testing.T) {
	for _, state := range []string{"running", "exited"} {
		t.Run(state, func(t *testing.T) {
			e := newTerminalSessionsEngine()
			e.state = state
			_, ts := newTestServerWith(t, e)
			if got := computerTerminals(t, login(t, ts, "admin"), ts.URL); len(got) != 0 {
				t.Fatalf("%+v", got)
			}
			resp, body := get(t, login(t, ts, "admin"), ts.URL+"/api/computers")
			if resp.StatusCode != 200 || !strings.Contains(body, `"terminals":[]`) {
				t.Fatalf("list %s", body)
			}
		})
	}
}

func TestTerminalPageRouteRemoved(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	admin.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, path := range []string{"/alice/terminal", "/alice/terminal/", "/alice/terminal/?session=build-2"} {
		if got := status(t, admin, "GET", ts.URL+path, ""); got != 404 {
			t.Errorf("%s: %d", path, got)
		}
	}
	// The computer page is the one address, with its tab in the query.
	if resp, body := get(t, admin, ts.URL+"/alice/?terminal=main"); resp.StatusCode != 200 || !strings.Contains(body, "<!doctype html>") {
		t.Errorf("computer page: %d %s", resp.StatusCode, body)
	}
}

// A desktop's change events each ask for a list; those arriving while one
// runs fold into a single further list, which sees the latest sessions.
func TestTerminalChangesCoalesce(t *testing.T) {
	e := newTerminalSessionsEngine()
	s, _ := newTestServerWith(t, e)
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	e.listHook = func() {
		entered <- struct{}{}
		<-release
	}
	lists := func() int {
		e.mu.Lock()
		defer e.mu.Unlock()
		n := 0
		for _, c := range e.cmds {
			if c[1] == "terminal-list" {
				n++
			}
		}
		return n
	}
	ctx := context.Background()
	s.terminalSessions.changed(ctx, "alice")
	<-entered
	e.setList("main\t0\tbash\n")
	for range 5 {
		s.terminalSessions.changed(ctx, "alice")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for len(s.terminalSessions.get("alice")) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("change never listed: %d lists", lists())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(2 * terminalChangeGap)
	if n := lists(); n != 2 {
		t.Fatalf("%d lists for six changes", n)
	}
	s.terminalSessions.mu.Lock()
	defer s.terminalSessions.mu.Unlock()
	if len(s.terminalSessions.changing) != 0 {
		t.Fatal("change refresh still marked running")
	}
}

func TestRenameSessionRoute(t *testing.T) {
	for _, tc := range []struct {
		name, user, state, next string
		code, want              int
	}{
		{"renamed", "admin", "running", "build", 0, 200},
		{"invalid", "admin", "running", "bad.name", 0, 400},
		{"no access", "anna", "running", "build", 0, 404},
		{"stopped", "admin", "exited", "build", 0, 409},
		{"missing", "admin", "running", "build", 3, 404},
		{"collision", "admin", "running", "build", 1, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTerminalSessionsEngine()
			e.state = tc.state
			e.killCode = tc.code
			_, ts := newTestServerWith(t, e)
			req, _ := http.NewRequest("PATCH", ts.URL+"/api/computers/alice/terminal/sessions/main", strings.NewReader(fmt.Sprintf(`{"name":%q}`, tc.next)))
			req.Header.Set("Origin", ts.URL)
			resp, err := login(t, ts, tc.user).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("%d: %s", resp.StatusCode, b)
			}
			ran := e.ran("deskyard", "terminal-rename", "main", tc.next)
			if ran != (tc.user == "admin" && tc.state == "running" && tc.next == "build") {
				t.Fatalf("unexpected helper invocation: %v", ran)
			}
		})
	}
}

func TestRenamedTerminalConnectionEndsUnderNewName(t *testing.T) {
	connections := newTerminalConnections()
	c := &terminalConnection{computer: "alice", session: "main"}
	if !connections.add(c) {
		t.Fatal("register")
	}
	connections.rename("alice", "main", "build")
	connections.beginEnding("alice", "main").finish(true)
	if isClosed(c.end) {
		t.Fatal("old name ended renamed attachment")
	}
	connections.beginEnding("alice", "build").finish(true)
	if !isClosed(c.end) {
		t.Fatal("new name did not end attachment")
	}
}
