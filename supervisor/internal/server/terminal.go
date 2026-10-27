package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

// A host-preserving TLS proxy may forward HTTPS over HTTP. HTTPS origins are
// therefore accepted on plain listeners; TLS/Secure listeners require HTTPS.
// Forwarded headers are deliberately not trusted.
func (s *Server) terminalOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery && (!(s.Sessions.Secure || r.TLS != nil) || u.Scheme == "https")
}
func terminalGeneration(c environments.Environment) string {
	return c.Container.ID + ":" + c.StartedAt.UTC().Format(time.RFC3339Nano)
}

func terminalSize(cols, rows uint) bool { return cols >= 2 && cols <= 500 && rows >= 1 && rows <= 300 }
func terminalJSON(b []byte, v any) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return false
	}
	return d.Decode(new(any)) == io.EOF
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.terminalOrigin(r) {
		writeError(w, 403, "cross-origin request refused")
		return
	}
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	// Only explicit opens create a session; deep links and reconnects join,
	// so that a reconnect can never revive a session someone ended.
	session := r.URL.Query().Get("session")
	create := r.URL.Query().Get("create") == "1"
	if !terminal.ValidSession(session) {
		writeError(w, 400, "invalid terminal session name")
		return
	}
	if c.State != "running" {
		writeError(w, 409, "start the environment before opening a terminal")
		return
	}
	if c.ImageLabels["deskyard.terminal"] != "tmux-v1" {
		writeError(w, 409, "upgrade this desktop image to use the shared terminal (requires deskyard.terminal=tmux-v1)")
		return
	}
	// The terminal takes the login cookie's claims as authenticated for this
	// request; a credential request carries none and is refused.
	who, ok := s.principal(r)
	if !ok || who.claims == nil {
		writeError(w, 401, "sign in first")
		return
	}
	claims := *who.claims
	ctx, cancel := context.WithDeadline(r.Context(), claims.Expires)
	defer cancel()
	live := &terminalConnection{claims: claims, environment: c.ID, session: session, join: !create, cancel: cancel}
	if !s.terminals.add(live) {
		writeError(w, 429, "terminal login ended or connection limit reached")
		return
	}
	defer s.terminals.remove(live)
	viewer := &conn{username: claims.Username, accountID: claims.AccountID, epoch: claims.Epoch, environment: c.ID, level: auth.LevelFull, cancel: cancel}
	s.viewers.add(c.Name, viewer)
	s.publishViewers(c.Name, c.ID)
	defer func() { s.viewers.remove(c.Name, viewer); s.publishViewers(c.Name, c.ID) }()
	// Recheck after registration: account/lifecycle changes before joining cannot
	// disappear between the initial permission check and cancellation hooks.
	current, err := s.Environments.Get(ctx, c.Name)
	if !s.terminalAllowed(live) || ctx.Err() != nil {
		writeError(w, 401, "terminal login ended")
		return
	}
	_, busy := s.Environments.Busy()[c.Name]
	if busy || err != nil || current.State != "running" || terminalGeneration(current) != terminalGeneration(c) {
		writeError(w, 409, "environment changed; reopen the terminal")
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 10)
	stopClose := context.AfterFunc(ctx, func() { ws.CloseNow() })
	defer stopClose()
	write := func(typ websocket.MessageType, b []byte) error {
		wctx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		return ws.Write(wctx, typ, b)
	}
	control := func(v any) { b, _ := json.Marshal(v); _ = write(websocket.MessageText, b) }
	fail := func(msg string) { control(map[string]string{"error": msg}) }
	// The session no longer exists, so the page closes its tab rather than
	// reconnecting.
	ended := sync.OnceFunc(func() {
		control(map[string]bool{"ended": true})
		_ = ws.Close(websocket.StatusNormalClosure, "session ended")
		cancel()
	})
	startCtx, startCancel := context.WithTimeout(ctx, 5*time.Second)
	typ, b, err := ws.Read(startCtx)
	startCancel()
	if err != nil {
		return
	}
	var size struct {
		Cols uint `json:"cols"`
		Rows uint `json:"rows"`
	}
	if typ != websocket.MessageText || !terminalJSON(b, &size) || !terminalSize(size.Cols, size.Rows) {
		fail("first message must contain valid cols (2–500) and rows (1–300)")
		return
	}
	p, err := s.terminalProcess(ctx, c.Container.ID, session, !create, size.Cols, size.Rows)
	if errors.Is(err, terminal.ErrNoSession) {
		ended()
		return
	}
	if err != nil {
		fail("could not start shared terminal; upgrade the desktop image if its helper is unavailable")
		return
	}
	closeProcess := sync.OnceFunc(p.Close)
	defer closeProcess()
	stopProcess := context.AfterFunc(ctx, closeProcess)
	defer stopProcess()
	control(map[string]any{"ready": true, "generation": terminalGeneration(c)})
	// Attaching adds a client and may have created the session; detaching
	// removes the client.
	s.terminalSessions.refreshLater(ctx, c)
	defer s.terminalSessions.refreshLater(ctx, c)
	endDone := make(chan struct{})
	go func() {
		defer close(endDone)
		select {
		case <-live.end:
			ended()
		case <-ctx.Done():
		}
	}()
	flow := newTerminalFlow()
	// Input and resize are pumped independently: blocked PTY writes never stop
	// ACK handling. The bounded queue fails closed rather than accumulating input.
	type input struct {
		data   []byte
		resize []uint
	}
	// Sixteen, not fewer: a terminal answers tmux's attach-time queries in a
	// burst while the shell's stdin may not be reading yet.
	inputs := make(chan input, 16)
	readerDone := make(chan struct{})
	pumpDone := make(chan struct{})
	defer func() {
		cancel()
		closeProcess()
		ws.CloseNow()
		<-readerDone
		<-pumpDone
		<-endDone
	}()
	go func() {
		defer close(pumpDone)
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-inputs:
				var err error
				if in.resize != nil {
					err = p.Resize(in.resize[0], in.resize[1])
				} else {
					_, err = p.Stdin.Write(in.data)
				}
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			typ, b, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var in input
			switch typ {
			case websocket.MessageBinary:
				in.data = b
			case websocket.MessageText:
				var ctl struct {
					Resize []uint `json:"resize"`
					Ack    *int   `json:"ack"`
				}
				if !terminalJSON(b, &ctl) || (ctl.Ack != nil) == (ctl.Resize != nil) {
					fail("invalid terminal control message")
					return
				}
				if ctl.Ack != nil {
					if !flow.ack(*ctl.Ack) {
						fail("invalid terminal acknowledgement")
						return
					}
					continue
				}
				if len(ctl.Resize) != 2 || !terminalSize(ctl.Resize[0], ctl.Resize[1]) {
					fail("invalid terminal size")
					return
				}
				in.resize = ctl.Resize
			default:
				return
			}
			select {
			case inputs <- in:
			case <-ctx.Done():
				return
			default:
				fail("terminal input queue is full")
				return
			}
		}
	}()
	buf := make([]byte, 16<<10)
	for {
		n, readErr := p.Stdout.Read(buf)
		if n > 0 {
			if flow.reserve(ctx, n) != nil || write(websocket.MessageBinary, buf[:n]) != nil {
				return
			}
		}
		if readErr != nil {
			if readErr != io.EOF && ctx.Err() == nil {
				fail("terminal output ended unexpectedly")
				return
			}
			break
		}
	}
	code, err := p.Wait()
	// Its tmux client exits when someone ends the session.
	if live.waitEnding(ctx) {
		ended()
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		fail("terminal process failed")
		return
	}
	control(map[string]int{"exit": code})
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
