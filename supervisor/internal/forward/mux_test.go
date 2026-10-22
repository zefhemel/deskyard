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

// ---- hostile peer ----

type rawFrame struct {
	typ     byte
	id      uint32
	payload string
}

// rawPeer talks frames (length-prefixed) straight to a serving desktop mux.
type rawPeer struct {
	t      *testing.T
	c      net.Conn
	desk   *Mux
	frames chan rawFrame
	served chan error
}

func newRawPeer(t *testing.T) *rawPeer {
	t.Helper()
	a, b := net.Pipe()
	p := &rawPeer{t: t, c: a, desk: NewMux(b, false, Desktop), frames: make(chan rawFrame, 1<<16), served: make(chan error, 1)}
	go func() { p.served <- p.desk.Serve(context.Background()) }()
	go func() {
		buf := make([]byte, maxFrame)
		for {
			f, err := readFrame(a, false, buf)
			if err != nil {
				return
			}
			typ, id, payload, _ := Peek(f)
			select {
			case p.frames <- rawFrame{typ, id, string(payload)}:
			default:
			}
		}
	}()
	t.Cleanup(func() { a.Close(); p.desk.Close() })
	return p
}

func (p *rawPeer) send(typ byte, id uint32, payload []byte) {
	p.t.Helper()
	if err := writeFrame(p.c, false, encodeFrame(typ, id, payload)); err != nil {
		p.t.Fatal(err)
	}
}

// waitReset waits for a Reset on id and returns its message.
func (p *rawPeer) waitReset(id uint32) string {
	p.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f := <-p.frames:
			if f.typ == TypeReset && f.id == id {
				return f.payload
			}
		case <-timeout:
			p.t.Fatalf("no Reset for stream %d", id)
		}
	}
}

func (p *rawPeer) waitShutdown() {
	p.t.Helper()
	select {
	case err := <-p.served:
		if err == nil {
			p.t.Fatal("want a protocol error from Serve")
		}
	case <-time.After(10 * time.Second):
		p.t.Fatal("mux was not shut down")
	}
}

func stallServer(t *testing.T) net.Listener {
	ln := tcpListen(t)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	return ln
}

func waitCount(t *testing.T, what string, n func() int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for n() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d entries left", what, n())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *Mux) streamCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.streams)
}

func TestDataBeyondWindowIsRejected(t *testing.T) {
	p := newRawPeer(t)
	p.send(TypeOpen, 1, portPayload(portOf(stallServer(t))))
	chunk := make([]byte, maxData)
	// Far more than window plus whatever the kernel buffers absorb.
	for i := 0; i < 1024; i++ {
		p.send(TypeData, 1, chunk)
	}
	if msg := p.waitReset(1); !strings.Contains(msg, "window") {
		t.Fatalf("reset: %q", msg)
	}
	waitCount(t, "desktop streams", p.desk.streamCount)
}

func TestStreamCap(t *testing.T) {
	p := newRawPeer(t)
	port := portPayload(portOf(stallServer(t)))
	for i := 0; i < maxStreams+20; i++ {
		p.send(TypeOpen, uint32(2*i+1), port)
	}
	if msg := p.waitReset(uint32(2*maxStreams + 1)); !strings.Contains(msg, "too many streams") {
		t.Fatalf("reset: %q", msg)
	}
	if n := p.desk.streamCount(); n > maxStreams {
		t.Fatalf("%d streams", n)
	}
}

func TestListenerCap(t *testing.T) {
	p := newRawPeer(t)
	var ports []int
	var lns []net.Listener
	for i := 0; i < maxListeners+1; i++ {
		ln := tcpListen(t)
		lns = append(lns, ln)
		ports = append(ports, portOf(ln))
	}
	for _, ln := range lns {
		ln.Close()
	}
	for i, port := range ports {
		p.send(TypeListen, uint32(2*i+1), portPayload(port))
	}
	if msg := p.waitReset(uint32(2*maxListeners + 1)); !strings.Contains(msg, "too many listeners") {
		t.Fatalf("reset: %q", msg)
	}
	p.desk.mu.Lock()
	n := len(p.desk.tcpLns)
	p.desk.mu.Unlock()
	if n > maxListeners {
		t.Fatalf("%d listeners", n)
	}
}

func TestControlQueueBounded(t *testing.T) {
	a, b := net.Pipe() // nobody reads a's replies
	desk := NewMux(b, false, Desktop)
	served := make(chan error, 1)
	go func() { served <- desk.Serve(context.Background()) }()
	defer a.Close()
	go func() {
		for i := 0; i < 4*maxCtrlQueue; i++ {
			if writeFrame(a, false, encodeFrame(TypeListen, uint32(2*i+1), portPayload(80))) != nil {
				return
			}
		}
	}()
	select {
	case err := <-served:
		if err == nil {
			t.Fatal("want an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("queue grew without bound")
	}
}

func TestPeerIDsValidated(t *testing.T) {
	cases := map[string]func(p *rawPeer, port int){
		"even id from the CLI": func(p *rawPeer, port int) { p.send(TypeOpen, 2, portPayload(port)) },
		"zero id":              func(p *rawPeer, port int) { p.send(TypeOpen, 0, portPayload(port)) },
		"duplicate open": func(p *rawPeer, port int) {
			p.send(TypeOpen, 1, portPayload(port))
			p.send(TypeOpen, 1, portPayload(port))
		},
		"duplicate listen": func(p *rawPeer, port int) {
			p.send(TypeListen, 1, portPayload(freePort(p.t)))
			p.send(TypeListen, 1, portPayload(freePort(p.t)))
		},
		"listen reusing a stream id": func(p *rawPeer, port int) {
			p.send(TypeOpen, 1, portPayload(port))
			p.send(TypeListen, 1, portPayload(freePort(p.t)))
		},
		"even listen id": func(p *rawPeer, port int) { p.send(TypeListen, 4, portPayload(freePort(p.t))) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			p := newRawPeer(t)
			run(p, portOf(stallServer(t)))
			p.waitShutdown()
		})
	}
}

func TestDesktopRejectsOpenOfOwnID(t *testing.T) {
	// A desktop-allocated (even) id must not be takeable by the peer.
	p := newRawPeer(t)
	port := freePort(t)
	p.send(TypeListen, 1, portPayload(port))
	p.send(TypeOpen, 2, portPayload(1234))
	p.waitShutdown()
}

func TestPortRange(t *testing.T) {
	cli, _ := pair(t, true)
	for _, port := range []int{0, -1, 65536, 70000} {
		if _, err := cli.Listen(port); err == nil {
			t.Errorf("Listen(%d) should fail", port)
		}
		if _, err := cli.Dial(port); err == nil {
			t.Errorf("Dial(%d) should fail", port)
		}
	}
}

func TestLocalCloseTearsDownWhileRemoteStreams(t *testing.T) {
	eachFraming(t, func(t *testing.T, framed bool) {
		cli, desk := pair(t, framed)
		srv := tcpListen(t)
		go func() {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			chunk := make([]byte, 16<<10)
			for {
				if _, err := c.Write(chunk); err != nil {
					return
				}
			}
		}()
		c, err := cli.Dial(portOf(srv))
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(c, make([]byte, 64<<10)); err != nil {
			t.Fatal(err)
		}
		c.Close()
		waitCount(t, "cli streams", cli.streamCount)
		waitCount(t, "desktop streams", desk.streamCount)
	})
}

func TestLocalTCPResetTearsDownPeer(t *testing.T) {
	cli, desk := pair(t, false)
	p := freePort(t)
	l, err := cli.Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tc, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(p)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() { // the CLI side keeps writing
		chunk := make([]byte, 16<<10)
		for {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}()
	tc.(*net.TCPConn).SetLinger(0)
	time.Sleep(50 * time.Millisecond)
	tc.Close() // RST on the desktop's local connection
	waitCount(t, "desktop streams", desk.streamCount)
	waitCount(t, "cli streams", cli.streamCount)
}

func (m *Mux) queuedBytes() int {
	m.qmu.Lock()
	defer m.qmu.Unlock()
	return m.qBytes
}

// A peer that never reads the transport must not make the desktop buffer
// more than maxQueueBytes (plus one chunk per stream), however much window
// credit it holds (40 streams hold 10 MiB).
func TestOutboundQueueBounded(t *testing.T) {
	a, b := net.Pipe()
	desk := NewMux(b, false, Desktop)
	served := make(chan error, 1)
	go func() { served <- desk.Serve(context.Background()) }()
	defer a.Close()

	srv := tcpListen(t)
	go func() {
		for {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				chunk := make([]byte, 32<<10)
				for {
					if _, err := c.Write(chunk); err != nil {
						return
					}
				}
			}()
		}
	}()

	const streams = 40 // 40 * 256 KiB of window is well over the bound
	port := portPayload(portOf(srv))
	for i := 0; i < streams; i++ {
		if err := writeFrame(a, false, encodeFrame(TypeOpen, uint32(2*i+1), port)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Second)
	got := desk.queuedBytes()
	limit := maxQueueBytes + streams*maxData
	if got > limit {
		t.Fatalf("queued %d bytes, limit %d", got, limit)
	}
	if got < maxQueueBytes/2 {
		t.Fatalf("only %d bytes queued: the attack did not run", got)
	}

	// Now the peer reads: everything flows, nothing deadlocks.
	total := 0
	buf := make([]byte, maxFrame)
	a.SetReadDeadline(time.Now().Add(20 * time.Second))
	for total < 8<<20 {
		f, err := readFrame(a, false, buf)
		if err != nil {
			t.Fatalf("after %d bytes: %v", total, err)
		}
		if typ, _, payload, _ := Peek(f); typ == TypeData {
			total += len(payload)
		}
	}
	a.Close()
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the peer closed")
	}
}

func TestBlockedWriteReleasedByClose(t *testing.T) {
	a, b := net.Pipe() // a is never read, so the queue fills
	cli := NewMux(b, false, CLI)
	defer a.Close()
	c, err := cli.Dial(1234)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the transport queue with other streams' data first.
	var conns []net.Conn
	for i := 0; i < 20; i++ {
		x, _ := cli.Dial(1234)
		conns = append(conns, x)
		go x.Write(make([]byte, initialWindow))
	}
	go c.Write(make([]byte, initialWindow))
	time.Sleep(300 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.Write(make([]byte, 1<<20)); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cli.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("blocked Write not released by Close")
	}
}
