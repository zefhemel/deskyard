package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// fragment is a masked client frame with FIN as given.
func fragment(opcode byte, payload []byte, fin bool) []byte {
	f := wsFrame(opcode, payload, true)
	if !fin {
		f[0] &^= 0x80
	}
	return f
}

func observed(t *testing.T, frames ...[]byte) []byte {
	t.Helper()
	up := newFakeUpstream()
	f := newObserveFilter(up)
	for _, fr := range frames {
		if n, err := f.Write(fr); err != nil || n != len(fr) {
			t.Fatalf("write: %d of %d, %v", n, len(fr), err)
		}
	}
	return up.sent()
}

func TestObserveFilterDropsInput(t *testing.T) {
	for _, msg := range []string{
		"kd,65", "ku,65", "kr", "kh,65", "m,10,10,1,0", "m2,1,1,0,0", "cw,aGk=", "cr", "r,800x600", "s,96",
		`SETTINGS,{"displayId":"primary"}`, "STOP_VIDEO", "START_AUDIO", "STOP_AUDIO", "js,c,0,pad,4,16",
		"cmd,xterm", "p,1", "vp,0,0,1,1", "SET_NATIVE_CURSOR_RENDERING,1", "CLIENT_FRAME_ACK 1 0",
		"DECODE_PACE 30", "REQUEST_CLIPBOARD", "_stats,1", "co,end,a", "sf,1,1", "_pointer_echo,1",
		"START_VIDEO ", "start_video", "LOST_FRAME ", "LOST_FRAME 1x", "REQUEST_KEYFRAME,kd,65",
	} {
		if got := observed(t, wsFrame(wsText, []byte(msg), true)); len(got) != 0 {
			t.Errorf("%q reached Selkies: % x", msg, got)
		}
	}
	// Binary messages: the microphone, the webcam, gzipped text, uploads.
	for _, b := range [][]byte{{0x02, 1, 2}, {0x05, 0x1f, 0x8b}, {0x06, 0, 0}, bytes.Repeat([]byte{1}, 70000)} {
		if got := observed(t, wsFrame(wsBinary, b, true)); len(got) != 0 {
			t.Errorf("binary % x… reached Selkies", b[:3])
		}
	}
	// A message split over fragments is dropped whole, even an allowed one.
	if got := observed(t, fragment(wsText, []byte("START_"), false), fragment(0, []byte("VIDEO"), true)); len(got) != 0 {
		t.Errorf("fragmented message reached Selkies: % x", got)
	}
	// An allowed text over 125 bytes or compressed (RSV1) is not one we know.
	long := wsFrame(wsText, []byte("START_VIDEO"+strings.Repeat(" ", 200)), true)
	rsv := wsFrame(wsText, []byte("START_VIDEO"), true)
	rsv[0] |= 0x40
	unmasked := wsFrame(wsText, []byte("START_VIDEO"), false)
	if got := observed(t, long, rsv, unmasked); len(got) != 0 {
		t.Errorf("odd frames reached Selkies: % x", got)
	}
}

func TestObserveFilterPassesVideoControl(t *testing.T) {
	frames := [][]byte{
		wsFrame(wsText, []byte("START_VIDEO"), true),
		wsFrame(wsText, []byte("REQUEST_KEYFRAME"), true),
		wsFrame(wsText, []byte("LOST_FRAME 4711"), true),
		wsFrame(0x9, []byte("ping"), true),
		wsFrame(0xA, []byte("pong"), true),
		wsFrame(0x8, []byte{0x03, 0xe8}, true),
	}
	if got, want := observed(t, frames...), slices.Concat(frames...); !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}
	// Control frames pass in the middle of a dropped fragmented message.
	ping := wsFrame(0x9, nil, true)
	got := observed(t, fragment(wsText, []byte("kd,"), false), ping, fragment(0, []byte("65"), true),
		wsFrame(wsText, []byte("START_VIDEO"), true))
	if want := slices.Concat(ping, wsFrame(wsText, []byte("START_VIDEO"), true)); !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}
}

func TestObserveFilterHandlesSplitFrames(t *testing.T) {
	allowed := wsFrame(wsText, []byte("START_VIDEO"), true)
	for _, dropped := range [][]byte{
		wsFrame(wsText, []byte("kd,65"), true),
		wsFrame(wsBinary, bytes.Repeat([]byte{7}, 300), true), // 16-bit length
	} {
		stream := slices.Concat(dropped, allowed, dropped)
		for i := 0; i <= len(stream); i++ {
			if got := observed(t, stream[:i], stream[i:]); !bytes.Equal(got, allowed) {
				t.Fatalf("split at %d: got % x", i, got)
			}
		}
		// And a byte at a time.
		var bytewise [][]byte
		for i := range stream {
			bytewise = append(bytewise, stream[i:i+1])
		}
		if got := observed(t, bytewise...); !bytes.Equal(got, allowed) {
			t.Fatalf("bytewise: got % x", got)
		}
	}
}

// recordingUpstream is a fake desktop Caddy that records the text messages
// its stream receives.
type recordingUpstream struct {
	mu   sync.Mutex
	msgs []string
}

func (u *recordingUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	for {
		_, msg, err := c.Read(r.Context())
		if err != nil {
			return
		}
		u.mu.Lock()
		u.msgs = append(u.msgs, string(msg))
		u.mu.Unlock()
	}
}

func (u *recordingUpstream) got() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.msgs)
}

// TestObserverStreamIsFiltered streams as an observer and as a full user:
// only the observer's input is dropped.
func TestObserverStreamIsFiltered(t *testing.T) {
	for _, level := range []auth.Level{auth.LevelObserve, auth.LevelFull} {
		t.Run(string(level), func(t *testing.T) {
			up := &recordingUpstream{}
			s, _ := newTestServerWith(t, fakeEngine{others: []engine.Container{runningEnvironment(t, "bob", up)}})
			c, err := s.Environments.Get(context.Background(), "bob")
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.Accounts.Create(accounts.NewAccount{Username: "olga", Password: "pw",
				Grants: []accounts.Grant{{Environment: c.ID, Level: level}}})
			if err != nil {
				t.Fatal(err)
			}
			// Straight to serveStream, as the desktop's route hands it an
			// account allowed to watch.
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.serveStream(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: a})), c)
			}))
			defer ts.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/?tab=t1",
				&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.CloseNow()
			for _, m := range []string{"kd,65", "m,10,10,1,0", "START_VIDEO", "cw,aGk=", "r,800x600", "REQUEST_KEYFRAME"} {
				if err := ws.Write(ctx, websocket.MessageText, []byte(m)); err != nil {
					t.Fatal(err)
				}
			}
			if err := ws.Write(ctx, websocket.MessageBinary, []byte{0x02, 1}); err != nil {
				t.Fatal(err)
			}
			want := []string{"START_VIDEO", "REQUEST_KEYFRAME"}
			if level == auth.LevelFull {
				want = []string{"kd,65", "m,10,10,1,0", "START_VIDEO", "cw,aGk=", "r,800x600", "REQUEST_KEYFRAME", "\x02\x01"}
			}
			for range 100 {
				if len(up.got()) >= len(want) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Anything dropped wrongly would have arrived by now.
			time.Sleep(50 * time.Millisecond)
			if got := up.got(); !slices.Equal(got, want) {
				t.Fatalf("Selkies got %q, want %q", got, want)
			}
		})
	}
}

// TestStreamRechecksLevelOnceRegistered opens a stream with the account as
// it was authorized, full, after a downgrade to observe while it waited its
// turn to dial: it's refused rather than opened unfiltered.
func TestStreamRechecksLevelOnceRegistered(t *testing.T) {
	up := &recordingUpstream{}
	s, _ := newTestServerWith(t, fakeEngine{others: []engine.Container{runningEnvironment(t, "bob", up)}})
	c, err := s.Environments.Get(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := s.Accounts.Create(accounts.NewAccount{Username: "olga", Password: "pw",
		Grants: []accounts.Grant{{Environment: c.ID, Level: auth.LevelFull}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.Update("olga", func(a *accounts.Account) {
		a.Grants = []accounts.Grant{{Environment: c.ID, Level: auth.LevelObserve}}
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveStream(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: authorized})), c)
	}))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/?tab=t1",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err == nil {
		ws.CloseNow()
		t.Fatal("opened a stream at the level the account had before the downgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dial: %v %v", resp, err)
	}
	if got := s.viewers.observers("bob"); len(got) != 0 {
		t.Fatalf("the refused stream stayed registered: %v", got)
	}
}

// A browser login that ended (sign out everywhere, a password change) while
// its stream waited its turn doesn't get the stream.
func TestStreamRechecksSessionOnceRegistered(t *testing.T) {
	up := &recordingUpstream{}
	s, _ := newTestServerWith(t, fakeEngine{others: []engine.Container{runningEnvironment(t, "bob", up)}})
	c, err := s.Environments.Get(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	signedIn, err := s.Accounts.Create(accounts.NewAccount{Username: "olga", Password: "pw",
		Grants: []accounts.Grant{{Environment: c.ID, Level: auth.LevelFull}}})
	if err != nil {
		t.Fatal(err)
	}
	claims := &auth.SessionClaims{Username: "olga", AccountID: signedIn.ID, Epoch: signedIn.SessionEpoch}
	if _, err := s.Accounts.BumpSessionEpoch("olga"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveStream(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: signedIn, claims: claims})), c)
	}))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/?tab=t1",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err == nil {
		ws.CloseNow()
		t.Fatal("opened a stream for a browser login that ended")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("dial: %v %v", resp, err)
	}
}

// received is what an observer receives of Selkies' frames, given in the
// chunks shown.
func received(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	up := newFakeUpstream()
	go func() {
		for _, c := range chunks {
			for len(c) > 0 { // the fake's reads take what fits
				n := min(len(c), 4096)
				up.toRead <- c[:n]
				c = c[n:]
			}
		}
		close(up.toRead)
	}()
	got, err := io.ReadAll(newObserveFilter(up))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Selkies sends the desktop's clipboard, and printed documents, to every
// controller, which the supervisor's connection is: an observer doesn't get
// them.
func TestObserveFilterDropsClipboardFromSelkies(t *testing.T) {
	video := wsFrame(wsBinary, bytes.Repeat([]byte{9}, 70000), false)
	kept := [][]byte{
		wsFrame(wsText, []byte("VIDEO_STARTED"), false), video,
		wsFrame(wsText, []byte("cursor,{}"), false), wsFrame(0x9, nil, false),
	}
	dropped := [][]byte{
		wsFrame(wsText, []byte("clipboard,c2VjcmV0"), false),
		wsFrame(wsText, []byte("clipboard_secret"), false),
		wsFrame(wsText, []byte("clipboard_start,text/plain,9000"), false),
		wsFrame(wsText, []byte("clipboard_data,"+strings.Repeat("QUFB", 3000)), false),
		wsFrame(wsText, []byte("clipboard_finish"), false),
		wsFrame(wsText, []byte("clipboard_binary,image/png,iVBORw0K"), false),
		wsFrame(wsText, []byte("clipboard_reply,cw"), false),
		wsFrame(wsText, []byte(`{"type": "print_document", "name": "salaries.pdf", "size_bytes": 1}`), false),
		slices.Concat([]byte{0x01, 4}, []byte("clip"), []byte{0x80, 5}, []byte("board")), // fragmented
	}
	stream := slices.Concat(kept[0], dropped[0], kept[1], dropped[1], dropped[2], dropped[3], kept[2],
		dropped[4], dropped[5], dropped[6], dropped[7], dropped[8], kept[3])
	want := slices.Concat(kept...)
	if got := received(t, stream); !bytes.Equal(got, want) {
		t.Fatalf("whole: got %d bytes, want %d", len(got), len(want))
	}
	for _, at := range []int{1, 2, 3, 7, 20, 100, 4000, len(stream) - 1} {
		if got := received(t, stream[:at], stream[at:]); !bytes.Equal(got, want) {
			t.Fatalf("split at %d: got %d bytes, want %d", at, len(got), len(want))
		}
	}
	var bytewise [][]byte
	for i := range stream[:300] {
		bytewise = append(bytewise, stream[i:i+1])
	}
	if got := received(t, append(bytewise, stream[300:])...); !bytes.Equal(got, want) {
		t.Fatal("bytewise")
	}
}

// An observer's START_VIDEO starts its picture; repeated, it would make the
// shared stream send keyframes, so it passes at most every few seconds.
func TestObserveFilterThrottlesStartVideo(t *testing.T) {
	up := newFakeUpstream()
	f := newObserveFilter(up).(*observeFilter)
	now := time.Unix(1000, 0)
	f.now = func() time.Time { return now }
	start := wsFrame(wsText, []byte("START_VIDEO"), true)
	for range 100 {
		_, _ = f.Write(start)
	}
	if got := up.sent(); !bytes.Equal(got, start) {
		t.Fatalf("a burst forwarded %d bytes", len(got))
	}
	now = now.Add(observeStartGap)
	_, _ = f.Write(start)
	if got := up.sent(); !bytes.Equal(got, slices.Concat(start, start)) {
		t.Fatalf("one after the gap: %d bytes", len(got))
	}
}

// _gz,1 would have Selkies gzip its large messages to the observer, which
// the filter couldn't read.
func TestObserveFilterDropsGzip(t *testing.T) {
	if got := observed(t, wsFrame(wsText, []byte("_gz,1"), true)); len(got) != 0 {
		t.Fatalf("_gz,1 reached Selkies")
	}
}
