package server

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

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
