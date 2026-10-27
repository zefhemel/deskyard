package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

func TestRewriteURL(t *testing.T) {
	const local = "127.0.0.1:9222"
	cases := []struct{ in, want string }{
		{"wss://host/api/environments/alice/cdp/devtools/browser/abc", "ws://127.0.0.1:9222/devtools/browser/abc"},
		{"ws://host:8080/pre/api/environments/alice/cdp/devtools/page/P1", "ws://127.0.0.1:9222/devtools/page/P1"},
		{"ws://localhost:9221/devtools/page/P1", "ws://127.0.0.1:9222/devtools/page/P1"},
		{"/api/environments/alice/cdp/devtools/inspector.html?ws=host/api/environments/alice/cdp/devtools/page/P1",
			"/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/P1"},
		{"/devtools/inspector.html?wss=host/api/environments/alice/cdp/devtools/page/P1",
			"/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/P1"},
		{"https://chrome-devtools-frontend.appspot.com/serve_rev/@abc/inspector.html?ws=host/api/environments/alice/cdp/devtools/page/P1",
			"https://chrome-devtools-frontend.appspot.com/serve_rev/@abc/inspector.html?ws=127.0.0.1:9222/devtools/page/P1"},
		{"http://host/pre/api/environments/bob-2/cdp/devtools/inspector.html?ws=host/pre/api/environments/bob-2/cdp/devtools/page/P1",
			"http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/P1"},
		{"about:blank", "about:blank"},
		{"https://example.com/?ws=1", "https://example.com/?ws=1"},
		{"page", "page"},
	}
	for _, c := range cases {
		if got := RewriteURL(c.in, local); got != c.want {
			t.Errorf("RewriteURL(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRewriteJSON(t *testing.T) {
	in := `[{"id":"P1","url":"https://example.com/","webSocketDebuggerUrl":"wss://h/api/environments/alice/cdp/devtools/page/P1","n":1.5}]`
	out := RewriteJSON([]byte(in), "127.0.0.1:9333")
	var v []map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v[0]["webSocketDebuggerUrl"] != "ws://127.0.0.1:9333/devtools/page/P1" || v[0]["url"] != "https://example.com/" {
		t.Errorf("rewritten: %s", out)
	}
	if string(RewriteJSON([]byte("not json"), "x")) != "not json" {
		t.Errorf("non-JSON should pass through")
	}
}

func forwarder(t *testing.T) (*clitest.Fake, *httptest.Server) {
	f := clitest.New(t, "alice")
	c := &remote.Client{Alias: "alice", Base: f.URL, Environment: "alice", Token: clitest.Credential}
	fw := &Forwarder{Client: c}
	srv := httptest.NewServer(fw)
	fw.Local = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(srv.Close)
	return f, srv
}

func TestForwarderHTTPAndWebSocket(t *testing.T) {
	_, srv := forwarder(t)
	resp, err := http.Get(srv.URL + "/json/version")
	if err != nil {
		t.Fatal(err)
	}
	var ver map[string]string
	json.NewDecoder(resp.Body).Decode(&ver)
	resp.Body.Close()
	local := strings.TrimPrefix(srv.URL, "http://")
	wsURL := ver["webSocketDebuggerUrl"]
	if wsURL != "ws://"+local+"/devtools/browser/b1" {
		t.Fatalf("webSocketDebuggerUrl = %q", wsURL)
	}

	resp, err = http.Get(srv.URL + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"devtoolsFrontendUrl": "/devtools/inspector.html?ws=`+local+`/devtools/page/p1"`) {
		t.Errorf("list = %s", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.Write(ctx, websocket.MessageText, []byte(`{"id":1}`))
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `/devtools/browser/b1 {"id":1}` {
		t.Errorf("echo = %q", data)
	}
}

func TestForwarderRefusesForeignHostAndOrigin(t *testing.T) {
	_, srv := forwarder(t)
	req, _ := http.NewRequest("GET", srv.URL+"/json/version", nil)
	req.Host = "evil.example:9222"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: status %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", "https://evil.example")
	_, _, err = websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/devtools/browser/b1", &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		t.Errorf("a cross-origin WebSocket was accepted")
	}
}

func TestForwarderPassesErrors(t *testing.T) {
	f, srv := forwarder(t)
	f.Stopped = true
	resp, err := http.Get(srv.URL + "/json/version")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "alice is stopped") {
		t.Errorf("status %d body %s", resp.StatusCode, body)
	}
}

func TestProbeAndServe(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &remote.Client{Alias: "alice", Base: f.URL, Environment: "alice", Token: clitest.Credential}
	ver, err := Probe(context.Background(), c)
	if err != nil || ver["Browser"] != "Chrome/141.0" {
		t.Fatalf("probe: %v %v", ver, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, c, 0, func(a string) { addrc <- a }, nil) }()
	addr := <-addrc
	resp, err := http.Get("http://" + addr + "/json/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
}

// TestForwarderRefusesDotSegments: the forwarder holds the account's
// credential, so a path that climbs out of /cdp/ must never reach another
// API route, directly or through the supervisor's redirect to the cleaned
// path.
func TestForwarderRefusesDotSegments(t *testing.T) {
	var mu sync.Mutex
	var leaked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/environments/alice/cdp/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		leaked = append(leaked, r.URL.Path)
		mu.Unlock()
		w.Write([]byte("secret"))
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)
	fw := &Forwarder{Client: &remote.Client{Base: up.URL, Environment: "alice", Token: clitest.Credential}}
	srv := httptest.NewServer(fw)
	t.Cleanup(srv.Close)
	fw.Local = strings.TrimPrefix(srv.URL, "http://")

	for _, p := range []string{"/../../me", "/%2e%2e/%2e%2e/me", "/json/../../../me", "/./json", "/json/%2E%2E/x", "/json%2f..%2f..%2fme", "//json"} {
		conn, err := net.Dial("tcp", fw.Local)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", p, fw.Local)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d body %q, want 400", p, resp.StatusCode, body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leaked) > 0 {
		t.Errorf("requests reached non-cdp routes: %v", leaked)
	}
}
