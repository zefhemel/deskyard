package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
	"github.com/zefhemel/deskyard/supervisor/internal/forward"
)

// ---- forward

// forwardBackoff is the wait before reconnect (or re-listen) attempt n,
// counting from 0: 1, 2, 4, 8, 16, then 30 s. Tests shorten it.
var forwardBackoff = func(n int) time.Duration {
	if n >= 5 {
		return 30 * time.Second
	}
	return time.Second << n
}

// forwardLinkWait is how long a local connection for a reverse mapping waits
// for the link before it is closed.
const forwardLinkWait = 10 * time.Second

// portMap is one mapping. Forward (not reverse): this machine's
// 127.0.0.1:local is reachable in the computer as localhost:env. Reverse:
// the computer's localhost:env is reachable here as 127.0.0.1:local.
type portMap struct {
	reverse    bool
	local, env int

	// Guarded by forwarder.mu.
	bound     bool   // reverse: the local listener is bound
	announced int    // the link generation it was last announced live on
	failed    string // the failure last reported, so each is reported once
}

func (m *portMap) direction() string {
	if m.reverse {
		return "out"
	}
	return "in"
}

func parsePort(s string) (int, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 65535
}

// parseForwardArgs reads PORT[:PORT] mappings; -R (or --reverse) makes the
// next one reverse.
// Limits on one forward command; the supervisor and the computer's helper
// enforce the same ones.
const (
	maxForwardMappings = 256
	maxForwardIns      = 64
)

func parseForwardArgs(cmd *Command, args []string) ([]*portMap, error) {
	var maps []*portMap
	reverse := false
	ins := 0
	for _, arg := range args {
		switch {
		case arg == "-R" || arg == "--reverse":
			if reverse {
				return nil, usagef(cmd, "-R twice: put one -R before each reverse mapping")
			}
			reverse = true
			continue
		case strings.HasPrefix(arg, "-"):
			return nil, usagef(cmd, "unknown option %q", arg)
		}
		a, b, two := strings.Cut(arg, ":")
		p1, ok := parsePort(a)
		p2 := p1
		if ok && two {
			p2, ok = parsePort(b)
		}
		if !ok {
			return nil, usagef(cmd, "%q isn't a mapping: want PORT or PORT:PORT, with ports from 1 to 65535", arg)
		}
		m := &portMap{reverse: reverse, local: p1, env: p2}
		if reverse {
			m.env, m.local = p1, p2
		}
		for _, o := range maps {
			if !reverse && !o.reverse && o.env == m.env {
				return nil, usagef(cmd, "computer port %d is mapped twice", m.env)
			}
			if reverse && o.reverse && o.local == m.local {
				return nil, usagef(cmd, "local port %d is mapped twice", m.local)
			}
		}
		maps = append(maps, m)
		reverse = false
		if len(maps) > maxForwardMappings {
			return nil, usagef(cmd, "at most %d mappings at once", maxForwardMappings)
		}
		if !m.reverse {
			if ins++; ins > maxForwardIns {
				return nil, usagef(cmd, "at most %d mappings into the computer (without -R) at once", maxForwardIns)
			}
		}
	}
	if reverse {
		return nil, usagef(cmd, "-R needs a mapping after it, e.g. -R 5173")
	}
	if len(maps) == 0 {
		return nil, usagef(cmd, "missing PORT")
	}
	return maps, nil
}

func runForward(a *App, c *call) error {
	maps, err := parseForwardArgs(c.cmd, c.args)
	if err != nil {
		return err
	}
	cl, err := a.target()
	if err != nil {
		return err
	}
	if cl == nil {
		return usagef(c.cmd, "forward maps ports between this machine and a computer; choose one with -c COMPUTER")
	}
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	fw := &forwarder{a: a, cl: cl, maps: maps, up: make(chan struct{})}
	var wg sync.WaitGroup
	// Local listeners first; they stay bound across reconnects.
	for _, m := range maps {
		if m.reverse {
			ln := fw.bindLocal(m)
			wg.Add(1)
			go func() {
				defer wg.Done()
				fw.serveLocal(ctx, m, ln)
			}()
		}
	}
	err = fw.run(ctx)
	cancel()
	wg.Wait()
	return err
}

// forwarder keeps the mappings of one `deskyard forward` and its link to the
// computer, reconnecting as needed.
type forwarder struct {
	a    *App
	cl   *remote.Client
	maps []*portMap

	mu  sync.Mutex    // state below, and output so lines don't interleave
	mux *forward.Mux  // the live link; nil while down
	gen int           // counts links
	up  chan struct{} // closed while a link is up
}

// sayLocked prints a state change on stderr.
func (fw *forwarder) sayLocked(format string, args ...any) {
	fmt.Fprintf(fw.a.Stderr, format+"\n", args...)
}

// fail reports a mapping's failure once (until it works again).
func (fw *forwarder) fail(m *portMap, where, msg string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if m.failed != msg {
		m.failed = msg
		fw.sayLocked("%s: %s", where, msg)
	}
}

// announceLocked prints a mapping on stdout once per link.
func (fw *forwarder) announceLocked(m *portMap) {
	if fw.mux == nil || m.announced == fw.gen {
		return
	}
	m.announced = fw.gen
	m.failed = ""
	if m.reverse {
		fmt.Fprintf(fw.a.Stdout, "out %d 127.0.0.1:%d\n", m.env, m.local)
	} else {
		fmt.Fprintf(fw.a.Stdout, "in %d %d\n", m.local, m.env)
	}
}

func (fw *forwarder) describe() string {
	var parts []string
	for _, m := range fw.maps {
		if m.reverse {
			parts = append(parts, fmt.Sprintf("%s:%d → 127.0.0.1:%d", fw.cl.Label(), m.env, m.local))
		} else {
			parts = append(parts, fmt.Sprintf("%d → %s:%d", m.local, fw.cl.Label(), m.env))
		}
	}
	return strings.Join(parts, ", ")
}

// run connects, serves the link until it drops, and reconnects with
// backoff. It returns nil when ctx ends and an error on a 401.
func (fw *forwarder) run(ctx context.Context) error {
	attempt := 0
	for {
		ws, err := fw.dial(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if remote.IsStatus(err, http.StatusUnauthorized) {
			return err
		}
		if err == nil {
			began := time.Now()
			err = fw.link(ctx, ws)
			if ctx.Err() != nil {
				return nil
			}
			if time.Since(began) >= 10*time.Second {
				attempt = 0
			}
		}
		d := forwardBackoff(attempt)
		attempt++
		fw.mu.Lock()
		fw.sayLocked("Lost %s (%s), retrying in %s s", fw.cl.Label(), lostReason(err), strconv.FormatFloat(d.Seconds(), 'f', -1, 64))
		fw.mu.Unlock()
		if !sleepCtx(ctx, d) {
			return nil
		}
	}
}

func lostReason(err error) string {
	var ce websocket.CloseError
	var ae *remote.APIError
	switch {
	case err == nil, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return "connection closed"
	case remote.IsStatus(err, http.StatusConflict):
		return "computer stopped"
	case errors.As(err, &ce):
		if ce.Reason != "" {
			return ce.Reason
		}
		return "connection closed"
	case errors.As(err, &ae):
		return ae.Msg
	}
	return err.Error()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type forwardStart struct {
	Forwards []forwardDecl `json:"forwards"`
}

type forwardDecl struct {
	Direction string `json:"direction"`
	Port      int    `json:"port"`
}

// dial opens the forward WebSocket and declares the mappings.
func (fw *forwarder) dial(ctx context.Context) (*websocket.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, fw.cl.WSURL("/forward"), &websocket.DialOptions{
		HTTPHeader: fw.cl.Header(),
		HTTPClient: fw.cl.HTTPClient(),
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, fw.cl.ResponseError(resp)
		}
		return nil, fw.cl.NetError(err)
	}
	ws.SetReadLimit(forward.MaxFrame)
	st := forwardStart{Forwards: []forwardDecl{}}
	for _, m := range fw.maps {
		st.Forwards = append(st.Forwards, forwardDecl{m.direction(), m.env})
	}
	b, _ := json.Marshal(st)
	if err := ws.Write(dctx, websocket.MessageText, b); err != nil {
		ws.CloseNow()
		return nil, fw.cl.NetError(err)
	}
	return ws, nil
}

// link serves one connection until it drops.
func (fw *forwarder) link(ctx context.Context, ws *websocket.Conn) error {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tr := &wsTransport{ctx: lctx, c: ws}
	mux := forward.NewMux(tr, true, forward.CLI)

	fw.mu.Lock()
	fw.gen++
	fw.mux = mux
	close(fw.up)
	fw.sayLocked("Linked to %s: %s", fw.cl.Label(), fw.describe())
	for _, m := range fw.maps {
		if m.reverse && m.bound {
			fw.announceLocked(m)
		}
	}
	fw.mu.Unlock()

	var wg sync.WaitGroup
	for _, m := range fw.maps {
		if !m.reverse {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fw.listenIn(lctx, mux, tr, m)
			}()
		}
	}
	go pingLink(lctx, ws)
	err := mux.Serve(lctx)
	cancel()
	mux.Close()
	fw.mu.Lock()
	fw.mux = nil
	fw.up = make(chan struct{})
	fw.mu.Unlock()
	wg.Wait()
	return err
}

// pingLink closes a link that stops answering, so a dead network is noticed.
func pingLink(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				ws.CloseNow()
				return
			}
		}
	}
}

// waitLink returns the live link, waiting up to d for one.
func (fw *forwarder) waitLink(ctx context.Context, d time.Duration) *forward.Mux {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		fw.mu.Lock()
		mux, up := fw.mux, fw.up
		fw.mu.Unlock()
		if mux != nil {
			return mux
		}
		select {
		case <-up:
		case <-t.C:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

// listenIn sets up a forward mapping on one link: the computer listens, and
// each connection there is carried to this machine's port. A refused listen
// is reported and retried with backoff while the link lasts.
func (fw *forwarder) listenIn(ctx context.Context, mux *forward.Mux, tr *wsTransport, m *portMap) {
	for attempt := 0; ; attempt++ {
		l, err := mux.Listen(m.env)
		if err != nil {
			if ctx.Err() != nil || tr.dead.Load() || errors.Is(err, net.ErrClosed) {
				return // the link is gone, not the mapping
			}
			fw.fail(m, fmt.Sprintf("%s:%d", fw.cl.Label(), m.env), err.Error())
			if strings.Contains(err.Error(), "below 1024") {
				return // won't change
			}
			if !sleepCtx(ctx, forwardBackoff(attempt)) {
				return
			}
			continue
		}
		fw.mu.Lock()
		fw.announceLocked(m)
		fw.mu.Unlock()
		go func() {
			<-ctx.Done()
			l.Close()
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				local, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.local)), 10*time.Second)
				if err != nil {
					c.Close()
					return
				}
				bridgeConns(c, local)
			}()
		}
	}
}

// bindLocal binds a reverse mapping's local listener, or reports why not.
func (fw *forwarder) bindLocal(m *portMap) net.Listener {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(m.local)))
	if err != nil {
		msg := err.Error()
		var oe *net.OpError
		switch {
		case errors.Is(err, syscall.EADDRINUSE):
			msg = "port in use"
		case errors.As(err, &oe) && oe.Err != nil:
			msg = oe.Err.Error()
		}
		fw.fail(m, fmt.Sprintf("127.0.0.1:%d", m.local), msg)
		return nil
	}
	fw.mu.Lock()
	m.bound = true
	m.failed = ""
	fw.announceLocked(m)
	fw.mu.Unlock()
	return ln
}

// serveLocal binds a reverse mapping (retrying with backoff while the port
// is taken) and carries each local connection over the link, for as long as
// the command runs.
func (fw *forwarder) serveLocal(ctx context.Context, m *portMap, ln net.Listener) {
	for attempt := 0; ln == nil; attempt++ {
		if !sleepCtx(ctx, forwardBackoff(attempt)) {
			return
		}
		ln = fw.bindLocal(m)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if !sleepCtx(ctx, 100*time.Millisecond) { // e.g. out of file descriptors
				return
			}
			continue
		}
		go func() {
			mux := fw.waitLink(ctx, forwardLinkWait)
			if mux == nil {
				c.Close()
				return
			}
			s, err := mux.Dial(m.env)
			if err != nil {
				c.Close()
				return
			}
			bridgeConns(c, s)
		}()
	}
}

// bridgeConns copies both ways until both directions end, passing
// half-closes on; an error closes both.
func bridgeConns(x, y net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			x.Close()
			y.Close()
			return
		}
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go cp(x, y)
	go cp(y, x)
	wg.Wait()
	x.Close()
	y.Close()
}

// wsTransport is the CLI side's framed transport: one binary message per
// frame. A text message is the supervisor's {"error"} before it closes.
type wsTransport struct {
	ctx  context.Context
	c    *websocket.Conn
	dead atomic.Bool
}

func (t *wsTransport) Read(p []byte) (int, error) {
	typ, data, err := t.c.Read(t.ctx)
	if err != nil {
		t.dead.Store(true)
		return 0, err
	}
	if typ == websocket.MessageText {
		t.dead.Store(true)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return 0, errors.New(e.Error)
		}
		return 0, errors.New("unexpected message from the supervisor")
	}
	if len(data) > len(p) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, data), nil
}

func (t *wsTransport) Write(p []byte) (int, error) {
	if err := t.c.Write(t.ctx, websocket.MessageBinary, p); err != nil {
		t.dead.Store(true)
		return 0, err
	}
	return len(p), nil
}

func (t *wsTransport) Close() error {
	t.dead.Store(true)
	return t.c.Close(websocket.StatusNormalClosure, "")
}

// ---- forward-helper (hidden; the supervisor runs it in the computer)

func runForwardHelper(a *App, c *call) error {
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := forward.NewMux(stdio{a.Stdin, a.Stdout}, false, forward.Desktop)
	if err := m.Serve(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

type stdio struct {
	io.Reader
	io.Writer
}

func (s stdio) Close() error {
	if c, ok := s.Reader.(io.Closer); ok {
		c.Close()
	}
	if c, ok := s.Writer.(io.Closer); ok {
		c.Close()
	}
	return nil
}
