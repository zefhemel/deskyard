package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func get(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func upgrade(t *testing.T, c *http.Client, url, origin string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "MDEyMzQ1Njc4OWFiY2RlZg==")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestScreenRoutes(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// KasmVNC's routes are gone.
	for _, p := range []string{"/alice/websockify", "/alice/vnc/vnc.html"} {
		if got := status(t, admin, "GET", ts.URL+p, ""); got != 404 {
			t.Errorf("%s: %d", p, got)
		}
	}

	// The client page comes from the image, with our additions first.
	for _, p := range []string{"/alice/screen/", "/alice/screen/index.html"} {
		resp, body := get(t, admin, ts.URL+p)
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" ||
			!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s: %d %v", p, resp.StatusCode, resp.Header)
		}
		tag := strings.Index(body, `<script src="deskyard.js"></script>`)
		if tag < 0 || tag > strings.Index(body, `<script type="module"`) {
			t.Errorf("%s: additions not loaded before the client: %s", p, body)
		}
	}
	if got := status(t, admin, "GET", ts.URL+"/alice/screen", ""); got != 302 {
		t.Errorf("/alice/screen: %d", got)
	}
	resp, body := get(t, admin, ts.URL+"/alice/screen/deskyard.js")
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") || !strings.Contains(body, "deskyardScreen") {
		t.Errorf("deskyard.js: %d %v", resp.StatusCode, resp.Header)
	}
	resp, body = get(t, admin, ts.URL+"/alice/screen/assets/index-X.js")
	if resp.StatusCode != 200 || body != fakeClient["assets/index-X.js"] {
		t.Errorf("client file: %d %q", resp.StatusCode, body)
	}
	for _, p := range []string{"/alice/screen/assets/", "/alice/screen/nosuch.js", "/alice/screen/assets/../../x"} {
		if got := status(t, admin, "GET", ts.URL+p, ""); got != 404 {
			t.Errorf("%s: %d", p, got)
		}
	}
	if got := status(t, admin, "POST", ts.URL+"/alice/screen/assets/index-X.js", ts.URL); got != 405 {
		t.Errorf("POST a client file: %d", got)
	}

	// Selkies' other HTTP API is refused, whatever the desktop's state.
	for _, p := range []string{"api/upload", "api/status", "api/screenshot", "api/files/x", "api/health", "api", "api/websocketsx"} {
		if got := status(t, admin, "GET", ts.URL+"/alice/screen/"+p, ""); got != 404 {
			t.Errorf("%s: %d", p, got)
		}
	}

	// The stream: session, access, same origin; then the desktop must run.
	ws := ts.URL + "/alice/screen/api/websockets"
	if got := upgrade(t, anon, ws, ts.URL); got != 401 {
		t.Errorf("anonymous stream: %d", got)
	}
	if got := upgrade(t, anna, ws, ts.URL); got != 404 {
		t.Errorf("anna's stream: %d", got)
	}
	if got := status(t, anna, "GET", ts.URL+"/alice/screen/assets/index-X.js", ""); got != 404 {
		t.Errorf("anna gets alice's client: %d", got)
	}
	if got := upgrade(t, admin, ws, "https://evil.example"); got != 403 {
		t.Errorf("foreign origin: %d", got)
	}
	if got := upgrade(t, admin, ws, ""); got != 403 {
		t.Errorf("no origin: %d", got)
	}
	if got := upgrade(t, admin, ws, ts.URL); got != 409 {
		t.Errorf("stopped: %d", got)
	}
	// The client's reconnect probe (a plain GET) waits on a stopped desktop:
	// never 409, which makes Selkies' client switch to WebRTC.
	if got := status(t, admin, "GET", ws, ""); got != 503 {
		t.Errorf("probe on a stopped desktop: %d", got)
	}
}

// upstream is a fake desktop Caddy: it records the stream requests it gets.
type upstream struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.reqs = append(u.reqs, r.Clone(context.Background()))
	u.mu.Unlock()
	if r.URL.Path != "/api/websockets" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	_ = c.Write(r.Context(), websocket.MessageText, []byte("hello"))
	for {
		if _, _, err := c.Read(r.Context()); err != nil {
			return
		}
	}
}

func (u *upstream) last() *http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.reqs[len(u.reqs)-1]
}

func runningDesktop(t *testing.T, name string, up http.Handler) engine.Container {
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return engine.Container{ID: "c-" + name, Name: "deskyard-" + name, State: "running", ImageID: "sha256:abc", LocalPort: p,
		Env:    map[string]string{desktops.TokenEnv: "tok-" + name},
		Labels: map[string]string{desktops.LabelManaged: "true", desktops.LabelName: name, desktops.LabelID: "id-" + name}}
}

type viewersState struct {
	Viewers []string `json:"viewers"`
	Mics    []string `json:"mics"`
}

func desktopState(t *testing.T, c *http.Client, url string) viewersState {
	t.Helper()
	_, body := get(t, c, url)
	var v viewersState
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return v
}

func waitState(t *testing.T, c *http.Client, url string, want viewersState) {
	t.Helper()
	var got viewersState
	for range 50 {
		if got = desktopState(t, c, url); slices.Equal(got.Viewers, want.Viewers) && slices.Equal(got.Mics, want.Mics) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("got %+v, want %+v", got, want)
}

func postMic(t *testing.T, c *http.Client, base, name, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/desktops/"+name+"/mic", strings.NewReader(body))
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestStreamProxyAndMics(t *testing.T) {
	up := &upstream{}
	_, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{runningDesktop(t, "bob", up)}})
	admin := login(t, ts, "admin")
	cookie := admin.Jar.Cookies(nil)[0]
	api := ts.URL + "/api/desktops/bob"

	// The reconnect probe reaches Selkies, without the session.
	if got := status(t, admin, "GET", ts.URL+"/bob/screen/api/websockets", ""); got != 204 {
		t.Errorf("probe: %d", got)
	}
	if r := up.last(); r.Header.Get("Cookie") != "" || r.Header.Get("X-Deskyard-Token") != "tok-bob" {
		t.Errorf("probe headers: %v", r.Header)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hdr := http.Header{"Origin": {ts.URL}, "Cookie": {cookie.String()}}
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/bob/screen/api/websockets?tab=t1",
		&websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if _, msg, err := ws.Read(ctx); err != nil || string(msg) != "hello" {
		t.Fatalf("stream: %q %v", msg, err)
	}
	r := up.last()
	if r.URL.Path != "/api/websockets" || r.URL.RawQuery != "" {
		t.Errorf("upstream URL: %s", r.URL)
	}
	if r.Header.Get("X-Deskyard-Token") != "tok-bob" || r.Header.Get("Cookie") != "" ||
		r.Header.Get("Origin") != "http://supervisor.deskyard.invalid" {
		t.Errorf("upstream headers: %v", r.Header)
	}
	waitState(t, admin, api, viewersState{Viewers: []string{"admin"}, Mics: []string{}})

	// The mic badge belongs to the stream of that tab.
	if got := postMic(t, admin, ts.URL, "bob", `{"on":true,"tab":"other"}`); got != 409 {
		t.Errorf("mic for a tab without a stream: %d", got)
	}
	if got := postMic(t, admin, ts.URL, "bob", `{"on":true}`); got != 400 {
		t.Errorf("mic without a tab: %d", got)
	}
	if got := postMic(t, admin, ts.URL, "bob", `{"on":true,"tab":"t1"}`); got != 204 {
		t.Fatalf("mic on: %d", got)
	}
	waitState(t, admin, api, viewersState{Viewers: []string{"admin"}, Mics: []string{"admin"}})
	if got := postMic(t, admin, ts.URL, "bob", `{"on":false,"tab":"t1"}`); got != 204 {
		t.Fatalf("mic off: %d", got)
	}
	waitState(t, admin, api, viewersState{Viewers: []string{"admin"}, Mics: []string{}})
	if got := postMic(t, admin, ts.URL, "bob", `{"on":true,"tab":"t1"}`); got != 204 {
		t.Fatalf("mic on again: %d", got)
	}
	waitState(t, admin, api, viewersState{Viewers: []string{"admin"}, Mics: []string{"admin"}})

	// The stream closes: the viewer and their mic go.
	ws.Close(websocket.StatusNormalClosure, "")
	waitState(t, admin, api, viewersState{Viewers: []string{}, Mics: []string{}})
	if got := postMic(t, admin, ts.URL, "bob", `{"on":true,"tab":"t1"}`); got != 409 {
		t.Errorf("mic after the stream closed: %d", got)
	}
}

func TestMicsPerStream(t *testing.T) {
	v := newViewers()
	a1 := &conn{username: "anna", tab: "a1", cancel: func() {}}
	a2 := &conn{username: "anna", tab: "a2", cancel: func() {}}
	z := &conn{username: "zef", tab: "z1", cancel: func() {}}
	for _, c := range []*conn{a1, a2, z} {
		v.add("d", c)
	}
	if got := v.mics("d"); len(got) != 0 {
		t.Fatalf("mics at start: %v", got)
	}
	if v.setMic("d", "zef", "a1", true) {
		t.Error("zef switched anna's tab")
	}
	if !v.setMic("d", "anna", "a1", true) || !v.setMic("d", "zef", "z1", true) || !v.setMic("d", "anna", "a2", true) {
		t.Fatal("setMic refused a stream's own tab")
	}
	if got := v.mics("d"); !slices.Equal(got, []string{"anna", "zef"}) {
		t.Errorf("mics: %v", got)
	}
	v.remove("d", a1) // anna's other tab still has its mic on
	if got := v.mics("d"); !slices.Equal(got, []string{"anna", "zef"}) {
		t.Errorf("after one of anna's tabs closed: %v", got)
	}
	v.remove("d", a2)
	v.remove("d", z)
	if got := v.mics("d"); len(got) != 0 {
		t.Errorf("after the streams closed: %v", got)
	}
}
