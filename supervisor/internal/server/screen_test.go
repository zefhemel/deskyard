package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// Every other spelling of the page goes to the one with our additions.
	for _, p := range []string{"/alice/screen", "/alice/screen/index.html/", "/alice/screen/./index.html",
		"/alice/screen//index.html", "/alice/screen/assets/../index.html", "/alice/screen/./"} {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "/alice/screen/" {
			t.Errorf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Location"))
		}
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
// Like Selkies, it accepts a stream within 500 ms of the previous one (all
// come from one address) and then closes it with 4029.
type upstream struct {
	mu    sync.Mutex
	reqs  []*http.Request
	dials []time.Time // when each stream dial arrived
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
	u.mu.Lock()
	now := time.Now()
	tooSoon := len(u.dials) > 0 && now.Sub(u.dials[len(u.dials)-1]) < 500*time.Millisecond
	u.dials = append(u.dials, now)
	u.mu.Unlock()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	if tooSoon {
		c.Close(4029, "Rate limited: reconnecting too quickly")
		return
	}
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
	if v.setCapture("d", "zef", "a1", captureMic, true, 0) {
		t.Error("zef switched anna's tab")
	}
	if !v.setCapture("d", "anna", "a1", captureMic, true, 0) || !v.setCapture("d", "zef", "z1", captureMic, true, 0) ||
		!v.setCapture("d", "anna", "a2", captureMic, true, 0) {
		t.Fatal("setCapture refused a stream's own tab")
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

func TestStreamDialsSpacedOut(t *testing.T) {
	up := &upstream{}
	_, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{runningDesktop(t, "bob", up)}})
	admin := login(t, ts, "admin")
	hdr := http.Header{"Origin": {ts.URL}, "Cookie": {admin.Jar.Cookies(nil)[0].String()}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Three viewers open the desktop at the same moment: all get the stream.
	const n = 3
	errs := make(chan error, n)
	for i := range n {
		go func() {
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/bob/screen/api/websockets?tab=t"+strconv.Itoa(i),
				&websocket.DialOptions{HTTPHeader: hdr})
			if err != nil {
				errs <- err
				return
			}
			defer ws.CloseNow()
			_, msg, err := ws.Read(ctx)
			if err == nil && string(msg) != "hello" {
				err = fmt.Errorf("got %q", msg)
			}
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.dials) != n {
		t.Fatalf("%d dials reached the desktop", len(up.dials))
	}
	for i := 1; i < n; i++ {
		if gap := up.dials[i].Sub(up.dials[i-1]); gap < streamDialGap {
			t.Errorf("dial %d came %v after the previous", i, gap)
		}
	}
}

func TestDialGateFreesOnCancel(t *testing.T) {
	g := newDialGates()
	release, err := g.wait(context.Background(), "d")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.wait(ctx, "d"); err == nil {
		t.Fatal("a second dial got the turn while the first held it")
	}
	release()
	release() // twice is harmless
	start := time.Now()
	r2, err := g.wait(context.Background(), "d")
	if err != nil {
		t.Fatal(err)
	}
	r2()
	if waited := time.Since(start); waited < streamDialGap-10*time.Millisecond {
		t.Errorf("the next dial waited only %v", waited)
	}
	other, err := g.wait(context.Background(), "e") // other desktops don't wait
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestDesktopReportsWebcam(t *testing.T) {
	cam := runningDesktop(t, "bob", &upstream{})
	cam.Devices = []string{"/dev/video0"}
	_, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{cam, runningDesktop(t, "carol", &upstream{})}})
	admin := login(t, ts, "admin")
	for name, want := range map[string]bool{"bob": true, "carol": false, "alice": false} {
		_, body := get(t, admin, ts.URL+"/api/desktops/"+name)
		var d struct {
			Webcam *bool `json:"webcam"`
		}
		if err := json.Unmarshal([]byte(body), &d); err != nil || d.Webcam == nil || *d.Webcam != want {
			t.Errorf("%s: %s (want webcam %v)", name, body, want)
		}
	}
}

func TestCaptureReportsInOrder(t *testing.T) {
	v := newViewers()
	v.add("d", &conn{username: "anna", tab: "a1", cancel: func() {}})
	// A page turned its mic off (seq 1) then on (seq 2); the off arrives last.
	v.setCapture("d", "anna", "a1", captureMic, true, 2)
	if !v.setCapture("d", "anna", "a1", captureMic, false, 1) {
		t.Fatal("a late report is still the tab's")
	}
	if got := v.mics("d"); !slices.Equal(got, []string{"anna"}) {
		t.Errorf("a late off undid a newer on: %v", got)
	}
	// The camera is its own badge, with its own order.
	v.setCapture("d", "anna", "a1", captureCam, true, 1)
	if got := v.cams("d"); !slices.Equal(got, []string{"anna"}) {
		t.Errorf("cams: %v", got)
	}
	v.setCapture("d", "anna", "a1", captureMic, false, 3)
	if got, cams := v.mics("d"), v.cams("d"); len(got) != 0 || !slices.Equal(cams, []string{"anna"}) {
		t.Errorf("mic off: mics %v, cams %v", got, cams)
	}
}

func TestCamBadgeEndpoint(t *testing.T) {
	up := &upstream{}
	_, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{runningDesktop(t, "bob", up)}})
	admin := login(t, ts, "admin")
	hdr := http.Header{"Origin": {ts.URL}, "Cookie": {admin.Jar.Cookies(nil)[0].String()}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/bob/screen/api/websockets?tab=t1",
		&websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	api := ts.URL + "/api/desktops/bob"
	waitState(t, admin, api, viewersState{Viewers: []string{"admin"}, Mics: []string{}})
	post := func(path, body string) int {
		req, _ := http.NewRequest("POST", api+path, strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("/cam", `{"on":true,"tab":"t1","seq":4}`); got != 204 {
		t.Fatalf("cam on: %d", got)
	}
	if got := post("/cam", `{"on":false,"tab":"t1","seq":3}`); got != 204 {
		t.Fatalf("late cam off: %d", got)
	}
	if got := post("/cam", `{"on":true,"tab":"t1","seq":-1}`); got != 400 {
		t.Fatalf("negative seq: %d", got)
	}
	_, body := get(t, admin, api)
	var d struct {
		Mics []string `json:"mics"`
		Cams []string `json:"cams"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil || !slices.Equal(d.Cams, []string{"admin"}) || d.Mics == nil {
		t.Errorf("%s", body)
	}
}

func TestCamerasForAdmins(t *testing.T) {
	sys := t.TempDir()
	for _, n := range []string{"video0", "video1"} {
		if err := os.MkdirAll(filepath.Join(sys, "devices", "virtual", "video4linux", n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bob := runningDesktop(t, "bob", &upstream{})
	bob.Devices = []string{"/dev/video0"}
	e := fakeEngine{others: []engine.Container{bob, runningDesktop(t, "carol", &upstream{})}}
	_, ts := newTestServerCfg(t, e, desktops.Config{SysDir: sys, LocalEngine: true})
	admin := login(t, ts, "admin")

	_, body := get(t, admin, ts.URL+"/api/host")
	var host struct {
		Cameras []struct {
			Device  string `json:"device"`
			Taken   bool   `json:"taken"`
			Desktop string `json:"desktop"`
		} `json:"cameras"`
	}
	if err := json.Unmarshal([]byte(body), &host); err != nil || len(host.Cameras) != 2 ||
		host.Cameras[0].Device != "/dev/video0" || !host.Cameras[0].Taken || host.Cameras[0].Desktop != "bob" || host.Cameras[1].Taken {
		t.Fatalf("%s", body)
	}
	_, body = get(t, admin, ts.URL+"/api/desktops/bob")
	if !strings.Contains(body, `"camera":"/dev/video0"`) || !strings.Contains(body, `"webcam":true`) {
		t.Errorf("bob: %s", body)
	}

	patch := func(name, body string) int {
		req, _ := http.NewRequest("PATCH", ts.URL+"/api/desktops/"+name, strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := patch("carol", `{"camera":"/dev/video0"}`); got != 409 {
		t.Errorf("carol taking bob's camera: %d", got)
	}
	if got := patch("carol", `{"camera":"/dev/video9"}`); got != 400 {
		t.Errorf("a camera the host hasn't: %d", got)
	}
}

func TestNoCamerasFromARemoteEngine(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	if _, body := get(t, admin, ts.URL+"/api/host"); !strings.Contains(body, `"cameras":null`) {
		t.Errorf("%s", body)
	}
}
