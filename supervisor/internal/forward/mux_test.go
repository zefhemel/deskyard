package forward

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pair connects a CLI mux to a serving desktop mux over net.Pipe.
func pair(t *testing.T, framed bool) (cli, desk *Mux) {
	t.Helper()
	a, b := net.Pipe()
	cli = NewMux(a, framed, CLI)
	desk = NewMux(b, framed, Desktop)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() { desk.Serve(ctx); close(served) }()
	t.Cleanup(func() { cancel(); cli.Close(); desk.Close(); <-served })
	return cli, desk
}

func eachFraming(t *testing.T, f func(t *testing.T, framed bool)) {
	for name, framed := range map[string]bool{"framed": true, "prefixed": false} {
		t.Run(name, func(t *testing.T) { f(t, framed) })
	}
}

func tcpListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func freePort(t *testing.T) int {
	t.Helper()
	ln := tcpListen(t)
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}

func portOf(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }

func echoServer(t *testing.T) net.Listener {
	ln := tcpListen(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln
}

// roundTrip writes data and reads the same amount back, concurrently.
func roundTrip(t *testing.T, c net.Conn, data []byte) {
	t.Helper()
	go func() { c.Write(data) }()
	got := make([]byte, len(data))
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("data corrupted")
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func TestDialEcho(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		echo := echoServer(t)
		c, err := cli.Dial(portOf(echo))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		roundTrip(t, c, randBytes(1<<20))
	})
}

func TestDialFailure(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		c, err := cli.Dial(freePort(t))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil || err == io.EOF {
			t.Fatalf("want a dial error, got %v", err)
		}
	})
}

func TestListenAccept(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		p := freePort(t)
		l, err := cli.Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(p)))
			if err != nil {
				return
			}
			defer c.Close()
			c.Write([]byte("hello"))
			io.Copy(io.Discard, c)
		}()
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		buf := make([]byte, 5)
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
			t.Fatalf("got %q, %v", buf, err)
		}
		if _, err := c.Write([]byte("world")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestListenAcceptEcho(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		p := freePort(t)
		l, err := cli.Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { io.Copy(c, c); c.Close() }()
			}
		}()
		c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(p)))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		roundTrip(t, c, randBytes(1<<20))
	})
}

func TestListenErrors(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		if _, err := cli.Listen(80); err == nil || !strings.Contains(err.Error(), "below 1024") {
			t.Fatalf("port 80: %v", err)
		}
		busy := tcpListen(t)
		if _, err := cli.Listen(portOf(busy)); err == nil || !strings.Contains(err.Error(), "in use") {
			t.Fatalf("busy port: %v", err)
		}
		l, err := cli.Listen(freePort(t))
		if err != nil {
			t.Fatalf("a free mapping on the same mux: %v", err)
		}
		l.Close()
	})
}

func TestListenerClose(t *testing.T) {
	cli, _ := pair(t, true)
	p := freePort(t)
	l, err := cli.Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := l.Accept(); err == nil {
		t.Fatal("Accept after Close should fail")
	}
	// The desktop releases the port, so it can be mapped again.
	deadline := time.Now().Add(5 * time.Second)
	for {
		l2, err := cli.Listen(p)
		if err == nil {
			l2.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("port never released: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBackpressure(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		// Stalled: accepts and never reads.
		stall := tcpListen(t)
		go func() {
			c, err := stall.Accept()
			if err == nil {
				t.Cleanup(func() { c.Close() })
			}
		}()
		echo := echoServer(t)

		slow, err := cli.Dial(portOf(stall))
		if err != nil {
			t.Fatal(err)
		}
		defer slow.Close()
		writing := make(chan struct{})
		go func() {
			slow.Write(make([]byte, 64<<20)) // far beyond window plus socket buffers: blocks
			close(writing)
		}()
		time.Sleep(200 * time.Millisecond)

		fast, err := cli.Dial(portOf(echo))
		if err != nil {
			t.Fatal(err)
		}
		defer fast.Close()
		roundTrip(t, fast, randBytes(1<<20))
		select {
		case <-writing:
			t.Fatal("stalled stream should still be blocked")
		default:
		}
	})
}

func TestHalfClose(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, _ := pair(t, framed)
		srv := tcpListen(t)
		go func() {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			got, _ := io.ReadAll(c) // returns on the CLI's half-close
			c.Write(append([]byte("got:"), got...))
		}()
		c, err := cli.Dial(portOf(srv))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		if err := c.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		got, err := io.ReadAll(c)
		if err != nil || string(got) != "got:ping" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}

func TestMuxCloseFailsConns(t *testing.T) {
	cli, desk := pair(t, false)
	echo := echoServer(t)
	c, err := cli.Dial(portOf(echo))
	if err != nil {
		t.Fatal(err)
	}
	desk.Close()
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || err == io.EOF {
		t.Fatalf("want an error after the peer hung up, got %v", err)
	}
	if _, err := cli.Dial(1234); err == nil {
		t.Fatal("Dial on a dead mux should fail")
	}
}

func TestPeek(t *testing.T) {
	f := encodeFrame(TypeListen, 7, portPayload(8080))
	typ, id, payload, err := Peek(f)
	if err != nil || typ != TypeListen || id != 7 {
		t.Fatalf("%d %d %v", typ, id, err)
	}
	if port, err := PortOf(payload); err != nil || port != 8080 {
		t.Fatalf("%d %v", port, err)
	}
	if _, _, _, err := Peek([]byte{1, 2}); err == nil {
		t.Fatal("short frame should fail")
	}
}

func TestWebSocketMessages(t *testing.T) {
	deskCh := make(chan *Mux, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		m := NewMux(NewWSMessages(r.Context(), c), true, Desktop)
		deskCh <- m
		m.Serve(r.Context())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cli := NewMux(NewWSMessages(ctx, c), true, CLI)
	defer cli.Close()
	echo := echoServer(t)
	conn, err := cli.Dial(portOf(echo))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	roundTrip(t, conn, randBytes(1<<20))
	(<-deskCh).Close()
}

func itoa(n int) string { return strconv.Itoa(n) }
