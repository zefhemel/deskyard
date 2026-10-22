package server

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/forward"
)

// relayHarness runs relayForward between a WebSocket and an in-process
// helper end (helperIn: what the relay writes to the helper; helperOut:
// what the helper writes to the relay).
type relayHarness struct {
	client    *websocket.Conn
	lk        *forwardLink
	changes   atomic.Int32
	result    chan error
	helperIn  io.Reader      // relay -> helper
	helperOut io.WriteCloser // helper -> relay
}

func newRelayHarness(t *testing.T) *relayHarness {
	t.Helper()
	h := &relayHarness{lk: newForwardLink("bot", []int{8765}), result: make(chan error, 1)}
	toHelperR, toHelperW := io.Pipe()
	fromHelperR, fromHelperW := io.Pipe()
	h.helperIn, h.helperOut = toHelperR, fromHelperW
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.SetReadLimit(64 << 10)
		err = relayForward(r.Context(), ws, toHelperW, fromHelperR, h.lk, func() { h.changes.Add(1) })
		toHelperW.Close()
		fromHelperR.Close()
		h.result <- err
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	h.client = c
	return h
}

type pipeRW struct {
	io.Reader
	io.WriteCloser
}

func (h *relayHarness) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-h.result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the relay didn't end")
		return nil
	}
}

func TestForwardRelayTracksListens(t *testing.T) {
	h := newRelayHarness(t)
	ctx := context.Background()
	helper := forward.NewMux(pipeRW{h.helperIn, h.helperOut}, false, forward.Desktop)
	go helper.Serve(ctx)
	cli := forward.NewMux(forward.NewWSMessages(ctx, h.client), true, forward.CLI)
	defer cli.Close()

	// A free desktop port: the helper really listens on it.
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	l, err := cli.Listen(port)
	if err != nil {
		t.Fatal(err)
	}
	want := []forwardJSON{{"bot", "in", port}, {"bot", "out", 8765}}
	if got := sorted(h.lk); !reflect.DeepEqual(got, want) {
		t.Errorf("after Listen: %v, want %v", got, want)
	}
	if h.changes.Load() != 1 {
		t.Errorf("changes = %d, want 1", h.changes.Load())
	}
	// A refused Listen is never listed.
	if _, err := cli.Listen(80); err == nil || !strings.Contains(err.Error(), "below 1024") {
		t.Errorf("Listen(80): %v", err)
	}
	// Data flows: a connection to the desktop port reaches the CLI.
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Write([]byte("hi"))
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	c.Close()
	if string(b) != "hi" {
		t.Errorf("read %q", b)
	}
	l.Close()
	deadline := time.Now().Add(2 * time.Second)
	for len(sorted(h.lk)) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := sorted(h.lk); !reflect.DeepEqual(got, []forwardJSON{{"bot", "out", 8765}}) {
		t.Errorf("after Close: %v", got)
	}
	cli.Close()
	if err := h.wait(t); errors.Is(err, errBadFrame) {
		t.Errorf("clean close ended as %v", err)
	}
}

func sorted(lk *forwardLink) []forwardJSON {
	f := newForwards()
	f.tryAdd("d", lk)
	return f.list("d")
}

func TestForwardRelayDropsMalformedFrames(t *testing.T) {
	frame := func(typ byte, id uint32, payload ...byte) []byte {
		b := []byte{typ, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], id)
		return append(b, payload...)
	}
	for _, tc := range []struct {
		name string
		typ  websocket.MessageType
		msg  []byte
	}{
		{"short", websocket.MessageBinary, []byte{4, 0}},
		{"unknown type", websocket.MessageBinary, frame(9, 1)},
		{"text", websocket.MessageText, []byte(`{"forwards":[]}`)},
		{"too big", websocket.MessageBinary, frame(forward.TypeData, 1, make([]byte, forward.MaxFrame)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRelayHarness(t)
			go io.Copy(io.Discard, h.helperIn)
			if err := h.client.Write(context.Background(), tc.typ, tc.msg); err != nil {
				t.Fatal(err)
			}
			if err := h.wait(t); !errors.Is(err, errBadFrame) {
				t.Errorf("relay ended with %v, want a malformed frame", err)
			}
		})
	}
	t.Run("too many listens", func(t *testing.T) {
		h := newRelayHarness(t)
		go io.Copy(io.Discard, h.helperIn)
		for i := 0; i <= maxForwardListens; i++ {
			h.client.Write(context.Background(), websocket.MessageBinary, frame(forward.TypeListen, uint32(2*i+1), 0x1f, 0x90))
		}
		if err := h.wait(t); !errors.Is(err, errBadFrame) {
			t.Errorf("relay ended with %v", err)
		}
	})
	t.Run("helper frame too long", func(t *testing.T) {
		h := newRelayHarness(t)
		go io.Copy(io.Discard, h.helperIn)
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], forward.MaxFrame+1)
		go h.helperOut.Write(l[:])
		if err := h.wait(t); !errors.Is(err, errBadFrame) {
			t.Errorf("relay ended with %v", err)
		}
	})
	t.Run("helper ends", func(t *testing.T) {
		h := newRelayHarness(t)
		h.helperOut.Close()
		if err := h.wait(t); !errors.Is(err, errHelperEnded) {
			t.Errorf("relay ended with %v", err)
		}
	})
}

func TestForwardListedOncePerTokenSorted(t *testing.T) {
	f := newForwards()
	a := newForwardLink("zed", []int{9000})
	b := newForwardLink("bot", []int{5173, 3000})
	c := newForwardLink("bot", []int{3000})
	b.in[1] = 4000
	for _, l := range []*forwardLink{a, b, c} {
		f.tryAdd("alice", l)
	}
	want := []forwardJSON{{"bot", "in", 4000}, {"bot", "out", 3000}, {"bot", "out", 5173}, {"zed", "out", 9000}}
	if got := f.list("alice"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, l := range []*forwardLink{a, b, c} {
		f.remove("alice", l)
	}
	if got := f.list("alice"); got == nil || len(got) != 0 {
		t.Errorf("empty list is %#v, want []", got)
	}
}

func TestParseForwardStart(t *testing.T) {
	outs, err := parseForwardStart([]byte(`{"forwards":[{"direction":"in","port":3000},{"direction":"out","port":5173}]}`))
	if err != nil || !reflect.DeepEqual(outs, []int{5173}) {
		t.Errorf("got %v %v", outs, err)
	}
	for _, bad := range []string{`{}`, `nope`, `{"forwards":[{"direction":"up","port":1}]}`, `{"forwards":[{"direction":"in","port":0}]}`, `{"forwards":[{"direction":"in","port":70000}]}`} {
		if _, err := parseForwardStart([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

func TestThrottleCoalescesBurst(t *testing.T) {
	const interval = 100 * time.Millisecond
	th := newThrottle(interval)
	var mu sync.Mutex
	var ran []int
	record := func(n int) func() {
		return func() { mu.Lock(); ran = append(ran, n); mu.Unlock() }
	}
	snapshot := func() []int { mu.Lock(); defer mu.Unlock(); return append([]int(nil), ran...) }

	for i := 1; i <= 50; i++ {
		th.do("alice", record(i))
	}
	if got := snapshot(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("right after the burst ran %v, want only the immediate call [1]", got)
	}
	// A different key isn't held back by alice's interval.
	th.do("bob", record(100))
	if got := snapshot(); !reflect.DeepEqual(got, []int{1, 100}) {
		t.Fatalf("a second key ran %v, want [1 100]", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(2 * interval) // nothing more may follow the trailing run
	if got := snapshot(); !reflect.DeepEqual(got, []int{1, 100, 50}) {
		t.Fatalf("burst ran %v, want the first call, bob's, then the latest (50) once", got)
	}
}

// forwardEngine is a running "alice" whose Exec calls are counted; the
// helper it starts stays up until the link is closed.
type forwardEngine struct {
	fakeEngine
	execs atomic.Int32
}

func (e *forwardEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.fakeEngine.Inspect(ctx, id)
	c.State = "running"
	return c, err
}

func (e *forwardEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	c := alice
	c.State = "running"
	return []engine.Container{c}, nil
}

func (e *forwardEngine) Exec(context.Context, string, engine.ExecSpec) (*engine.Process, error) {
	e.execs.Add(1)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go io.Copy(io.Discard, inR)
	return &engine.Process{Stdin: inW, Stdout: outR,
		Wait:  func() (int, error) { return 0, nil },
		Close: func() { inW.Close(); outW.Close(); outR.Close() }}, nil
}

func TestForwardLinkCapPerToken(t *testing.T) {
	if maxLinksPerToken != 4 {
		t.Fatalf("maxLinksPerToken is %d; the documented cap is 4", maxLinksPerToken)
	}
	t.Run("tryAdd", func(t *testing.T) {
		f := newForwards()
		var links []*forwardLink
		for i := 0; i < maxLinksPerToken; i++ {
			l := newForwardLink("bot", nil)
			if !f.tryAdd("alice", l) {
				t.Fatalf("link %d of %d was refused", i+1, maxLinksPerToken)
			}
			links = append(links, l)
		}
		if f.tryAdd("alice", newForwardLink("bot", nil)) {
			t.Fatalf("link %d for one token was accepted", maxLinksPerToken+1)
		}
		if !f.tryAdd("alice", newForwardLink("other", nil)) {
			t.Error("another token's link was refused")
		}
		if !f.tryAdd("bob", newForwardLink("bot", nil)) {
			t.Error("the same token on another desktop was refused")
		}
		f.remove("alice", links[0])
		if !f.tryAdd("alice", newForwardLink("bot", nil)) {
			t.Error("a new link was refused after one was removed")
		}
		if f.tryAdd("alice", newForwardLink("bot", nil)) {
			t.Error("the cap didn't hold after the replacement")
		}
	})

	t.Run("handler refuses before exec", func(t *testing.T) {
		dir := t.TempDir()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		accts, _ := accounts.Load(dir)
		_, _ = accts.EnsureAdmin("admin", "pw")
		e := &forwardEngine{}
		s, err := New(Options{Log: log, Eng: e, Accounts: accts, StateDir: dir, DefaultImage: "img",
			Desktops: desktops.New(e, desktops.Config{DataDir: dir, Subnet: "10.0.0.0/16"}, log),
			UI:       fstest.MapFS{"index.html": {Data: []byte("ui")}}})
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(s)
		t.Cleanup(srv.Close)
		_, bot, err := s.tokens.create("bot", "id-alice", "admin", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, other, err := s.tokens.create("other", "id-alice", "admin", nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		dial := func(tok string) (*websocket.Conn, string) {
			t.Helper()
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+agentPrefix+"/forward",
				&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ws.CloseNow() })
			if err := ws.Write(ctx, websocket.MessageText, []byte(`{"forwards":[]}`)); err != nil {
				t.Fatal(err)
			}
			// An accepted link stays quiet; a refused one gets an error and a close.
			// (Cancelling a Read closes the connection, so read in the background.)
			msgs := make(chan string, 1)
			go func() {
				if typ, b, err := ws.Read(ctx); err == nil && typ == websocket.MessageText {
					msgs <- string(b)
				}
			}()
			select {
			case m := <-msgs:
				return ws, m
			case <-time.After(300 * time.Millisecond):
				return ws, ""
			}
		}
		waitExecs := func(n int32) {
			t.Helper()
			deadline := time.Now().Add(5 * time.Second)
			for e.execs.Load() != n && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if got := e.execs.Load(); got != n {
				t.Fatalf("%d execs, want %d", got, n)
			}
		}
		var open []*websocket.Conn
		for i := 0; i < maxLinksPerToken; i++ {
			ws, msg := dial(bot)
			if msg != "" {
				t.Fatalf("link %d refused: %s", i+1, msg)
			}
			open = append(open, ws)
		}
		waitExecs(maxLinksPerToken)
		if _, msg := dial(bot); !strings.Contains(msg, "already has") {
			t.Fatalf("the 5th link got %q, want a refusal", msg)
		}
		waitExecs(maxLinksPerToken) // refused before any exec
		if _, msg := dial(other); msg != "" {
			t.Fatalf("another token's link refused: %s", msg)
		}
		waitExecs(maxLinksPerToken + 1)
		open[0].CloseNow()
		deadline := time.Now().Add(5 * time.Second)
		for {
			ws, msg := dial(bot)
			if msg == "" {
				open = append(open, ws)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no new link accepted after one closed: %s", msg)
			}
		}
		waitExecs(maxLinksPerToken + 2)
	})
}

func TestForwardListCappedPerToken(t *testing.T) {
	if maxListedPerToken != 64 {
		t.Fatalf("maxListedPerToken is %d; the documented cap is 64", maxListedPerToken)
	}
	f := newForwards()
	many := newForwardLink("bot", nil)
	for i := 0; i < maxListedPerToken+36; i++ {
		many.in[uint32(i)] = 10000 + i
	}
	f.tryAdd("alice", many)
	f.tryAdd("alice", newForwardLink("zed", []int{9000}))
	f.tryAdd("alice", newForwardLink("abe", []int{1}))
	got := f.list("alice")
	perToken := map[string]int{}
	for _, e := range got {
		perToken[e.Token]++
	}
	if perToken["bot"] != maxListedPerToken {
		t.Errorf("listed %d mappings for the busy token, want %d", perToken["bot"], maxListedPerToken)
	}
	if perToken["zed"] != 1 || perToken["abe"] != 1 {
		t.Errorf("other tokens listed %v, want one each (sorted before and after the busy one)", perToken)
	}
	if len(got) != maxListedPerToken+2 {
		t.Errorf("%d mappings in all, want %d", len(got), maxListedPerToken+2)
	}
}
