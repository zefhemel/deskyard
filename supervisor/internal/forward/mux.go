package forward

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Side says which end of the mapping a Mux is. The CLI side picks odd stream
// ids and uses Listen and Dial; the Desktop side picks even ids and runs Serve.
type Side int

const (
	CLI Side = iota
	Desktop
)

// Limits that keep a hostile peer from exhausting memory. Frames from the
// other end are never trusted: a violation of the window, of stream-id
// ownership, or of the control-queue bound shuts the mux down (ids, queue) or
// resets the one stream (window); the caps below answer with a Reset.
const (
	maxStreams   = 256
	maxListeners = 64
	maxCtrlQueue = 1024 // queued non-Data frames
	// maxQueueBytes bounds Data queued for the transport across the whole mux.
	// Window credit is chosen by the peer, so it cannot bound our memory:
	// Write blocks while the queue is over this (streams may overshoot by one
	// chunk each).
	maxQueueBytes = 4 << 20
)

// Mux multiplexes listeners and TCP streams over one io.ReadWriteCloser.
type Mux struct {
	rw     io.ReadWriteCloser
	framed bool
	side   Side

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
	err       error // why done closed; guarded by mu

	mu        sync.Mutex
	nextID    uint32
	streams   map[uint32]*stream
	pending   map[uint32]chan error   // CLI: Listen calls waiting for Listening/Reset
	listeners map[uint32]*Listener    // CLI: by listener id
	byPort    map[int]*Listener       // CLI: by port, for incoming Open
	tcpLns    map[uint32]net.Listener // Desktop: by listener id

	qmu    sync.Mutex
	qc     *sync.Cond
	q      [][]byte
	ctrl   int // non-Data frames in q
	qBytes int // Data payload bytes in q
}

// NewMux wraps rw. framed is true when rw preserves message boundaries (a
// WebSocket); otherwise frames carry a u32 length prefix.
func NewMux(rw io.ReadWriteCloser, framed bool, side Side) *Mux {
	m := &Mux{
		rw: rw, framed: framed, side: side,
		done:      make(chan struct{}),
		nextID:    1,
		streams:   map[uint32]*stream{},
		pending:   map[uint32]chan error{},
		listeners: map[uint32]*Listener{},
		byPort:    map[int]*Listener{},
		tcpLns:    map[uint32]net.Listener{},
	}
	if side == Desktop {
		m.nextID = 2
	}
	m.qc = sync.NewCond(&m.qmu)
	return m
}

func (m *Mux) start() {
	m.startOnce.Do(func() {
		go m.writeLoop()
		go m.readLoop()
	})
}

func (m *Mux) allocID() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID += 2
	return id
}

// send queues a frame for the writer goroutine. Queuing never blocks, so the
// read loop can always answer (a blocking write there could deadlock two
// peers that both write from their read loops).
func (m *Mux) send(typ byte, id uint32, payload []byte) error {
	select {
	case <-m.done:
		return m.doneErr()
	default:
	}
	f := encodeFrame(typ, id, payload)
	m.qmu.Lock()
	over := false
	if typ != TypeData {
		m.ctrl++
		over = m.ctrl > maxCtrlQueue
	} else {
		m.qBytes += len(payload)
	}
	if !over {
		m.q = append(m.q, f)
	}
	m.qmu.Unlock()
	if over {
		// The peer is not reading our replies: stop, don't buffer forever.
		go m.shutdown(errors.New("forward: peer not reading; control queue full"))
		return m.doneErr()
	}
	m.qc.Signal()
	return nil
}

func (m *Mux) writeLoop() {
	for {
		m.qmu.Lock()
		for len(m.q) == 0 {
			select {
			case <-m.done:
				m.qmu.Unlock()
				return
			default:
			}
			m.qc.Wait()
		}
		f := m.q[0]
		m.q[0] = nil
		m.q = m.q[1:]
		wake := false
		if f[0] != TypeData {
			m.ctrl--
		} else {
			wake = m.qBytes >= maxQueueBytes && m.qBytes-(len(f)-headerLen) < maxQueueBytes
			m.qBytes -= len(f) - headerLen
		}
		m.qmu.Unlock()
		if wake {
			m.wakeWriters()
		}
		if err := writeFrame(m.rw, m.framed, f); err != nil {
			m.shutdown(err)
			return
		}
	}
}

func (m *Mux) queueFull() bool {
	m.qmu.Lock()
	defer m.qmu.Unlock()
	return m.qBytes >= maxQueueBytes
}

// wakeWriters nudges streams blocked in Write on a full queue. Taking each
// stream's lock orders this after any waiter's queueFull check.
func (m *Mux) wakeWriters() {
	m.mu.Lock()
	ss := make([]*stream, 0, len(m.streams))
	for _, s := range m.streams {
		ss = append(ss, s)
	}
	m.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

func (m *Mux) readLoop() {
	buf := make([]byte, maxFrame)
	for {
		f, err := readFrame(m.rw, m.framed, buf)
		if err != nil {
			m.shutdown(err)
			return
		}
		typ, id, payload, err := Peek(f)
		if err != nil {
			m.shutdown(err)
			return
		}
		m.dispatch(typ, id, payload)
	}
}

func (m *Mux) dispatch(typ byte, id uint32, payload []byte) {
	switch typ {
	case TypeListen:
		if m.side != Desktop {
			return
		}
		port, err := PortOf(payload)
		if err != nil {
			m.send(TypeReset, id, []byte("bad listen request"))
			return
		}
		if !m.validPeerID(id) {
			return
		}
		m.handleListen(id, port)
	case TypeListening:
		m.finishListen(id, nil)
	case TypeOpen:
		port, err := PortOf(payload)
		if err != nil {
			m.send(TypeReset, id, []byte("bad open request"))
			return
		}
		if !m.validPeerID(id) {
			return
		}
		if m.side == Desktop {
			m.handleOpen(id, port)
		} else {
			m.handleIncoming(id, port)
		}
	case TypeData:
		if s := m.stream(id); s != nil {
			s.onData(payload)
		}
	case TypeClose:
		if s := m.stream(id); s != nil {
			s.onClose()
			return
		}
		m.mu.Lock()
		ln := m.tcpLns[id]
		delete(m.tcpLns, id)
		m.mu.Unlock()
		if ln != nil {
			ln.Close()
		}
	case TypeReset:
		msg := string(payload)
		if m.finishListen(id, errors.New(msg)) {
			return
		}
		if s := m.stream(id); s != nil {
			s.onReset(msg)
		}
	case TypeWindow:
		if len(payload) >= 4 {
			if s := m.stream(id); s != nil {
				s.onWindow(int(uint32(payload[0])<<24 | uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])))
			}
		}
	}
}

// validPeerID checks that id has the parity the peer owns and is not in use;
// otherwise the peer is broken or hostile and the mux is shut down.
func (m *Mux) validPeerID(id uint32) bool {
	peerOdd := m.side == Desktop // the CLI side owns odd ids
	m.mu.Lock()
	_, inStream := m.streams[id]
	_, inLn := m.tcpLns[id]
	_, inListeners := m.listeners[id]
	m.mu.Unlock()
	if (id%2 == 1) != peerOdd || id == 0 || inStream || inLn || inListeners {
		m.shutdown(errors.New("forward: protocol violation: bad stream id"))
		return false
	}
	return true
}

func (m *Mux) stream(id uint32) *stream {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams[id]
}

func (m *Mux) finishListen(id uint32, err error) bool {
	m.mu.Lock()
	ch := m.pending[id]
	delete(m.pending, id)
	m.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- err
	return true
}

// newStream registers a stream, or returns nil when the mux already holds
// maxStreams.
func (m *Mux) newStream(id uint32, port int) *stream {
	s := &stream{m: m, id: id, port: port, wnd: initialWindow}
	s.cond = sync.NewCond(&s.mu)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.streams) >= maxStreams {
		return nil
	}
	m.streams[id] = s
	return s
}

func (m *Mux) doneErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err == nil || m.err == io.EOF {
		return net.ErrClosed
	}
	return m.err
}

// shutdown tears everything down once: the transport is gone or closed.
func (m *Mux) shutdown(err error) {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.err = err
		streams := m.streams
		m.streams = map[uint32]*stream{}
		lns := m.tcpLns
		m.tcpLns = map[uint32]net.Listener{}
		pend := m.pending
		m.pending = map[uint32]chan error{}
		m.mu.Unlock()
		close(m.done)
		m.rw.Close()
		m.qmu.Lock()
		m.q, m.qBytes, m.ctrl = nil, 0, 0
		m.qmu.Unlock()
		m.qc.Broadcast()
		for _, ch := range pend {
			ch <- net.ErrClosed
		}
		for _, ln := range lns {
			ln.Close()
		}
		for _, s := range streams {
			s.onReset(net.ErrClosed.Error())
		}
	})
}

// Close shuts the mux and its transport down; open conns and listeners fail.
func (m *Mux) Close() error {
	m.shutdown(net.ErrClosed)
	return nil
}

// Serve runs the mux until ctx ends or the transport closes; either end of a
// link may call it (the desktop's helper and the CLI both do). It honours the
// peer's Listen (refusing ports below 1024) and Open (dialing loopback). It
// returns nil when the peer hangs up cleanly.
func (m *Mux) Serve(ctx context.Context) error {
	m.start()
	select {
	case <-ctx.Done():
		m.Close()
		return ctx.Err()
	case <-m.done:
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.err == nil || m.err == io.EOF || errors.Is(m.err, net.ErrClosed) {
			return nil
		}
		return m.err
	}
}

// ---- desktop side ----

func (m *Mux) handleListen(id uint32, port int) {
	if port < 1024 {
		m.send(TypeReset, id, []byte("port below 1024"))
		return
	}
	m.mu.Lock()
	full := len(m.tcpLns) >= maxListeners
	m.mu.Unlock()
	if full {
		m.send(TypeReset, id, []byte("too many listeners"))
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		msg := err.Error()
		if errors.Is(err, syscall.EADDRINUSE) {
			msg = "port in use"
		}
		m.send(TypeReset, id, []byte(msg))
		return
	}
	m.mu.Lock()
	select {
	case <-m.done:
		m.mu.Unlock()
		ln.Close()
		return
	default:
	}
	if len(m.tcpLns) >= maxListeners {
		m.mu.Unlock()
		ln.Close()
		m.send(TypeReset, id, []byte("too many listeners"))
		return
	}
	m.tcpLns[id] = ln
	m.mu.Unlock()
	m.send(TypeListening, id, nil)
	go m.acceptLoop(ln, id, port)
}

func (m *Mux) acceptLoop(ln net.Listener, lid uint32, port int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if (errors.As(err, &ne) && ne.Timeout()) || errors.Is(err, syscall.EMFILE) ||
				errors.Is(err, syscall.ENFILE) || errors.Is(err, syscall.ECONNABORTED) { // temporary
				time.Sleep(100 * time.Millisecond)
				continue
			}
			m.mu.Lock()
			cur, ok := m.tcpLns[lid]
			if ok && cur == ln { // not closed by the peer or a shutdown: report it
				delete(m.tcpLns, lid)
			} else {
				ok = false
			}
			m.mu.Unlock()
			if ok {
				ln.Close()
				m.send(TypeReset, lid, []byte("the listener failed: "+err.Error()))
			}
			return
		}
		id := m.allocID()
		s := m.newStream(id, port)
		if s == nil {
			c.Close() // too many streams: refuse this connection
			continue
		}
		if err := m.send(TypeOpen, id, portPayload(port)); err != nil {
			c.Close()
			return
		}
		go bridge(s, c)
	}
}

func (m *Mux) handleOpen(id uint32, port int) {
	s := m.newStream(id, port) // registered first: Data may arrive while we dial
	if s == nil {
		m.send(TypeReset, id, []byte("too many streams"))
		return
	}
	go func() {
		d := net.Dialer{Timeout: 10 * time.Second}
		c, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			s.reset(err.Error())
			return
		}
		bridge(s, c)
	}()
}

// bridge copies between a stream and a TCP connection until both directions
// finish, honouring half-close.
func bridge(s *stream, c net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := io.Copy(s, c); err != nil {
			c.Close()
			s.reset("connection failed")
			return
		}
		s.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		if _, err := io.Copy(c, s); err != nil {
			c.Close()
			s.reset("connection failed")
			return
		}
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			c.Close()
		}
	}()
	wg.Wait()
	c.Close()
	s.Close()
}

// ---- CLI side ----

// Listener delivers connections as the desktop side accepts them.
type Listener struct {
	m      *Mux
	id     uint32
	port   int
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

// Listen asks the desktop to listen on 127.0.0.1:port. It fails with the
// desktop's reason ("port below 1024", "port in use", ...).
func (m *Mux) Listen(port int) (*Listener, error) {
	if m.side != CLI {
		return nil, errors.New("forward: Listen is for the CLI side")
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("port out of range")
	}
	m.start()
	id := m.allocID()
	l := &Listener{m: m, id: id, port: port, ch: make(chan net.Conn, 16), closed: make(chan struct{})}
	res := make(chan error, 1)
	m.mu.Lock()
	if _, taken := m.byPort[port]; taken {
		m.mu.Unlock()
		return nil, errors.New("port in use")
	}
	if len(m.byPort) >= maxListeners {
		m.mu.Unlock()
		return nil, errors.New("too many listeners")
	}
	m.pending[id] = res
	m.byPort[port] = l
	m.listeners[id] = l
	m.mu.Unlock()
	fail := func(err error) (*Listener, error) {
		m.mu.Lock()
		delete(m.pending, id)
		delete(m.byPort, port)
		delete(m.listeners, id)
		m.mu.Unlock()
		return nil, err
	}
	if err := m.send(TypeListen, id, portPayload(port)); err != nil {
		return fail(err)
	}
	select {
	case err := <-res:
		if err != nil {
			return fail(err)
		}
		return l, nil
	case <-m.done:
		return fail(m.doneErr())
	}
}

// handleIncoming is an Open from the desktop for one of our listeners.
func (m *Mux) handleIncoming(id uint32, port int) {
	m.mu.Lock()
	l := m.byPort[port]
	m.mu.Unlock()
	if l == nil {
		m.send(TypeReset, id, []byte("no listener"))
		return
	}
	s := m.newStream(id, port)
	if s == nil {
		m.send(TypeReset, id, []byte("too many streams"))
		return
	}
	select {
	case l.ch <- s:
	default:
		s.reset("accept queue full")
	}
}

func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-l.m.done:
		return nil, net.ErrClosed
	}
}

func (l *Listener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.m.mu.Lock()
		delete(l.m.byPort, l.port)
		delete(l.m.listeners, l.id)
		l.m.mu.Unlock()
		l.m.send(TypeClose, l.id, nil)
		for {
			select {
			case c := <-l.ch:
				c.Close()
			default:
				return
			}
		}
	})
	return nil
}

func (l *Listener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: l.port}
}

// Dial opens a connection whose far end dials 127.0.0.1:port on the desktop.
// It returns at once; a failed dial shows up as an error on the conn.
func (m *Mux) Dial(port int) (net.Conn, error) {
	if m.side != CLI {
		return nil, errors.New("forward: Dial is for the CLI side")
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("port out of range")
	}
	m.start()
	id := m.allocID()
	s := m.newStream(id, port)
	if s == nil {
		return nil, errors.New("too many streams")
	}
	if err := m.send(TypeOpen, id, portPayload(port)); err != nil {
		s.onReset(err.Error())
		return nil, err
	}
	return s, nil
}

// ---- streams ----

type fwdAddr string

func (fwdAddr) Network() string  { return "deskyard-forward" }
func (a fwdAddr) String() string { return string(a) }

// stream is a net.Conn over the mux with a per-direction flow-control window.
type stream struct {
	m    *Mux
	id   uint32
	port int

	mu        sync.Mutex
	cond      *sync.Cond
	rbuf      []byte
	ungranted int  // bytes consumed by Read, not yet granted back
	rcvd      int  // bytes received and not yet granted back; must stay within initialWindow
	peerDone  bool // peer half-closed
	wnd       int  // bytes we may still send
	wClosed   bool // we sent Close
	closed    bool // Close called: reads fail, incoming data is dropped
	err       error
	rd, wd    time.Time
}

func (s *stream) fullyDone() bool { return s.err != nil || (s.peerDone && s.wClosed) }

// removeIfDone must be called with s.mu held.
func (s *stream) removeIfDone() {
	if s.fullyDone() {
		s.m.mu.Lock()
		delete(s.m.streams, s.id)
		s.m.mu.Unlock()
	}
}

func (s *stream) onData(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	if s.rcvd+len(p) > initialWindow {
		// The peer sent more than we granted: abort this stream.
		s.resetLocked("window exceeded")
		return
	}
	s.rcvd += len(p)
	if s.closed || s.peerDone {
		s.resetLocked("data after close") // the peer sent after its Close
		return
	}
	s.rbuf = append(s.rbuf, p...)
	s.cond.Broadcast()
}

func (s *stream) onClose() {
	s.mu.Lock()
	s.peerDone = true
	s.cond.Broadcast()
	s.removeIfDone()
	s.mu.Unlock()
}

func (s *stream) onReset(msg string) {
	s.mu.Lock()
	if s.err == nil {
		s.err = errors.New(msg)
	}
	s.rbuf = nil
	s.cond.Broadcast()
	s.removeIfDone()
	s.mu.Unlock()
}

func (s *stream) onWindow(n int) {
	s.mu.Lock()
	if s.wnd+n > initialWindow { // n is a u32, never negative
		s.resetLocked("window overflow")
		s.mu.Unlock()
		return
	}
	s.wnd += n
	s.cond.Broadcast()
	s.mu.Unlock()
}

// reset aborts both directions and tells the peer.
func (s *stream) reset(msg string) {
	s.mu.Lock()
	s.resetLocked(msg)
	s.mu.Unlock()
}

func (s *stream) resetLocked(msg string) {
	if s.err != nil {
		return
	}
	s.err = errors.New(msg)
	s.rbuf, s.rcvd, s.ungranted = nil, 0, 0
	s.m.send(TypeReset, s.id, []byte(msg))
	s.cond.Broadcast()
	s.removeIfDone()
}

func (s *stream) grant(n int) {
	if n <= 0 {
		return
	}
	s.rcvd -= n
	b := []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	s.m.send(TypeWindow, s.id, b)
}

// waitLocked waits on the cond, waking at deadline d if set. It reports
// whether the deadline has passed.
func (s *stream) waitLocked(d time.Time) (timedOut bool) {
	if !d.IsZero() {
		left := time.Until(d)
		if left <= 0 {
			return true
		}
		t := time.AfterFunc(left, func() {
			s.mu.Lock()
			s.cond.Broadcast()
			s.mu.Unlock()
		})
		defer t.Stop()
	}
	s.cond.Wait()
	return false
}

func (s *stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		switch {
		case s.closed:
			return 0, net.ErrClosed
		case len(s.rbuf) > 0:
			n := copy(p, s.rbuf)
			s.rbuf = s.rbuf[n:]
			if len(s.rbuf) == 0 {
				s.rbuf = nil
			}
			s.ungranted += n
			if s.ungranted >= initialWindow/2 {
				s.grant(s.ungranted)
				s.ungranted = 0
			}
			return n, nil
		case s.err != nil:
			return 0, s.err
		case s.peerDone:
			return 0, io.EOF
		}
		if s.waitLocked(s.rd) {
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func (s *stream) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		s.mu.Lock()
		for {
			if s.err != nil {
				err := s.err
				s.mu.Unlock()
				return total, err
			}
			if s.closed || s.wClosed {
				s.mu.Unlock()
				return total, io.ErrClosedPipe
			}
			if s.wnd > 0 && !s.m.queueFull() {
				break
			}
			if s.waitLocked(s.wd) {
				s.mu.Unlock()
				return total, os.ErrDeadlineExceeded
			}
		}
		n := min(len(p), s.wnd, maxData)
		s.wnd -= n
		s.mu.Unlock()
		if err := s.m.send(TypeData, s.id, p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

// CloseWrite half-closes our direction; the peer reads EOF but can still write.
func (s *stream) CloseWrite() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wClosed || s.err != nil {
		return nil
	}
	s.wClosed = true
	s.m.send(TypeClose, s.id, nil)
	s.cond.Broadcast()
	s.removeIfDone()
	return nil
}

// Close is a full close. While the peer may still send, it aborts with Reset
// so a remote that keeps streaming stops at once; after the peer's half-close
// it just closes our direction.
func (s *stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.err != nil {
		return nil
	}
	if !s.peerDone {
		s.resetLocked("closed")
		return nil
	}
	s.rbuf, s.rcvd, s.ungranted = nil, 0, 0
	s.cond.Broadcast()
	if !s.wClosed {
		s.wClosed = true
		s.m.send(TypeClose, s.id, nil)
	}
	s.removeIfDone()
	return nil
}

func (s *stream) LocalAddr() net.Addr  { return fwdAddr("local") }
func (s *stream) RemoteAddr() net.Addr { return fwdAddr("127.0.0.1:" + strconv.Itoa(s.port)) }

func (s *stream) SetDeadline(t time.Time) error {
	s.SetReadDeadline(t)
	return s.SetWriteDeadline(t)
}

func (s *stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.rd = t
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

func (s *stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.wd = t
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}
