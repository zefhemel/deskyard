package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/forward"
)

// forwardJSON is one active port mapping on a desktop, as viewers see it.
// Port is always the desktop's port.
type forwardJSON struct {
	Token     string `json:"token"`
	Direction string `json:"direction"` // in: into the desktop; out: out of it
	Port      int    `json:"port"`
}

// maxForwardListens bounds the Listen requests one link may have pending or
// active; the CLI's Mux never has more (forward's listener cap).
const maxForwardListens = 64

// forwardLink is one forward WebSocket: a client credential's mappings on a
// desktop. The out ports are as declared; the in ports are the ones the
// helper confirmed with Listening.
type forwardLink struct {
	token   string // the credential's name, as Computer.forwards shows it
	tokenID string // the credential's id: its cap and activity summary
	out     []int

	mu      sync.Mutex
	listens map[uint32]int // listener id -> port, Listen seen, no answer yet
	in      map[uint32]int // listener id -> port, listening
}

func newForwardLink(token, tokenID string, out []int) *forwardLink {
	return &forwardLink{token: token, tokenID: tokenID, out: out, listens: map[uint32]int{}, in: map[uint32]int{}}
}

// fromClient observes a frame on its way to the helper. It reports whether
// the in ports changed, and fails when the client asks for too many listeners.
func (l *forwardLink) fromClient(typ byte, id uint32, payload []byte) (changed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch typ {
	case forward.TypeListen:
		port, perr := forward.PortOf(payload)
		if perr != nil {
			return false, nil // the helper refuses it
		}
		if _, dup := l.listens[id]; dup {
			return false, nil // the helper ends the link on a reused id
		}
		if _, dup := l.in[id]; dup {
			return false, nil
		}
		if len(l.listens)+len(l.in) >= maxForwardListens {
			return false, errors.New("too many listeners")
		}
		l.listens[id] = port
	case forward.TypeClose:
		delete(l.listens, id)
		if _, ok := l.in[id]; ok {
			delete(l.in, id)
			return true, nil
		}
	}
	return false, nil
}

// fromHelper observes a frame on its way to the client.
func (l *forwardLink) fromHelper(typ byte, id uint32) (changed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch typ {
	case forward.TypeListening:
		if port, ok := l.listens[id]; ok {
			delete(l.listens, id)
			l.in[id] = port
			return true
		}
	case forward.TypeReset:
		delete(l.listens, id)
		if _, ok := l.in[id]; ok {
			delete(l.in, id)
			return true
		}
	}
	return false
}

func (l *forwardLink) entries() []forwardJSON {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []forwardJSON
	for _, p := range l.in {
		out = append(out, forwardJSON{l.token, "in", p})
	}
	for _, p := range l.out {
		out = append(out, forwardJSON{l.token, "out", p})
	}
	return out
}

// forwards tracks the open forward links per desktop (by name, like viewers).
type forwards struct {
	mu     sync.Mutex
	byName map[string]map[*forwardLink]bool
}

func newForwards() *forwards { return &forwards{byName: map[string]map[*forwardLink]bool{}} }

// maxLinksPerToken bounds the forward links (each one a docker exec) one
// credential may hold open on a desktop.
const maxLinksPerToken = 4

// maxListedPerToken bounds the mappings listed for one credential name. Mappings
// beyond it still work; viewers just don't see them.
const maxListedPerToken = 64

// tryAdd registers a link unless its credential already has maxLinksPerToken.
func (f *forwards) tryAdd(name string, l *forwardLink) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for o := range f.byName[name] {
		if o.tokenID == l.tokenID {
			n++
		}
	}
	if n >= maxLinksPerToken {
		return false
	}
	if f.byName[name] == nil {
		f.byName[name] = map[*forwardLink]bool{}
	}
	f.byName[name][l] = true
	return true
}

func (f *forwards) remove(name string, l *forwardLink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byName[name], l)
	if len(f.byName[name]) == 0 {
		delete(f.byName, name)
	}
}

// list is a desktop's active mappings, each once, sorted by token,
// direction and port.
func (f *forwards) list(name string) []forwardJSON {
	f.mu.Lock()
	links := make([]*forwardLink, 0, len(f.byName[name]))
	for l := range f.byName[name] {
		links = append(links, l)
	}
	f.mu.Unlock()
	out := []forwardJSON{}
	seen := map[forwardJSON]bool{}
	for _, l := range links {
		for _, e := range l.entries() {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Token != b.Token {
			return a.Token < b.Token
		}
		if a.Direction != b.Direction {
			return a.Direction < b.Direction
		}
		return a.Port < b.Port
	})
	capped := out[:0]
	perToken := map[string]int{}
	for _, e := range out {
		if perToken[e.Token]++; perToken[e.Token] <= maxListedPerToken {
			capped = append(capped, e)
		}
	}
	return capped
}

// listFor is the mappings of one credential's links on a desktop, each
// once, sorted by direction and port. Links with no id belong to no one.
func (f *forwards) listFor(name, tokenID string) []forwardJSON {
	if tokenID == "" {
		return nil
	}
	f.mu.Lock()
	var links []*forwardLink
	for l := range f.byName[name] {
		if l.tokenID == tokenID {
			links = append(links, l)
		}
	}
	f.mu.Unlock()
	var out []forwardJSON
	seen := map[forwardJSON]bool{}
	for _, l := range links {
		for _, e := range l.entries() {
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Direction != out[j].Direction {
			return out[i].Direction < out[j].Direction
		}
		return out[i].Port < out[j].Port
	})
	return out[:min(len(out), maxListedPerToken)]
}

type forwardStart struct {
	Forwards []struct {
		Direction string `json:"direction"`
		Port      int    `json:"port"`
	} `json:"forwards"`
}

const maxForwardMappings = 256

// parseForwardStart checks the first message and returns the declared out ports.
func parseForwardStart(b []byte) ([]int, error) {
	var st forwardStart
	if err := json.Unmarshal(b, &st); err != nil || st.Forwards == nil {
		return nil, errors.New(`the first message must be {"forwards":[{"direction":"in"|"out","port":n}]}`)
	}
	if len(st.Forwards) > maxForwardMappings {
		return nil, fmt.Errorf("at most %d mappings per connection", maxForwardMappings)
	}
	out := []int{}
	for _, m := range st.Forwards {
		if m.Port < 1 || m.Port > 65535 {
			return nil, fmt.Errorf("port %d is out of range (1-65535)", m.Port)
		}
		switch m.Direction {
		case "in":
		case "out":
			out = append(out, m.Port)
		default:
			return nil, fmt.Errorf(`direction %q: want "in" or "out"`, m.Direction)
		}
	}
	return out, nil
}

// agentForward relays the forward protocol (internal/forward) between the
// client's WebSocket and `deskyard forward-helper`, run in the desktop as
// user. Frames are checked for size and type only; Listen and Listening are
// observed so viewers see which ports are mapped.
func (s *Server) remoteForward(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 10) // the first message; frames are checked against forward.MaxFrame
	ctx := r.Context()
	defer ws.CloseNow()
	fail := func(msg string) {
		b, _ := json.Marshal(map[string]string{"error": msg})
		_ = ws.Write(ctx, websocket.MessageText, b)
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}
	typ, first, err := ws.Read(ctx)
	if err != nil {
		return
	}
	if typ != websocket.MessageText {
		fail(`the first message must be {"forwards":[{"direction":"in"|"out","port":n}]}`)
		return
	}
	outs, err := parseForwardStart(first)
	if err != nil {
		fail(err.Error())
		return
	}
	lk := newForwardLink(cred.Name, cred.ID, outs)
	if !s.forwards.tryAdd(c.Name, lk) {
		fail(fmt.Sprintf("this credential already has %d forward links open on this computer; close one first", maxLinksPerToken))
		return
	}
	// The live mappings (listFor) replace this text in the activity API.
	defer s.activity.ForwardOpen(refOf(c), s.agentOf(cred), "forward")()
	defer func() {
		s.forwards.remove(c.Name, lk)
		s.publishViewers(c.Name, c.ID)
	}()
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{
		Cmd: []string{"deskyard", "forward-helper"}, User: "user", DesktopSession: true, WorkDir: "/workspace",
		Env: execEnvFor(nil), Stdin: true,
	})
	if err != nil {
		fail(err.Error())
		return
	}
	defer p.Close()
	// Docker multiplexes stdout and stderr: stderr must be drained. Keep
	// its end for the error message.
	stderr := &tailBuffer{max: 512}
	stderrDone := make(chan struct{})
	if p.Stderr != nil {
		go func() {
			defer close(stderrDone)
			io.Copy(stderr, p.Stderr)
		}()
	} else {
		close(stderrDone)
	}
	s.publishViewers(c.Name, c.ID)
	s.Log.Info("remote forward", "computer", c.Name, "credential", cred.Name, "account", cred.Username, "out", outs)

	go keepAlive(ctx, ws)
	err = relayForward(ctx, ws, p.Stdin, p.Stdout, lk, func() { s.publishViewers(c.Name, c.ID) })
	_ = p.Stdin.Close()
	switch {
	case ctx.Err() != nil: // the client left, or its access was revoked
	case errors.Is(err, errHelperEnded):
		if cur, err := s.Computers.Get(ctx, c.Name); err != nil || !cur.Running() {
			_ = ws.Close(websocket.StatusGoingAway, "computer stopped")
			return
		}
		code, _ := p.Wait()
		select { // the helper has exited: its stderr ends shortly
		case <-stderrDone:
		case <-time.After(time.Second):
		}
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if code != 0 && msg != "" {
			fail("the forward helper failed: " + msg)
			return
		}
		_ = ws.Close(websocket.StatusGoingAway, "the computer side closed")
	case errors.Is(err, errBadFrame):
		s.Log.Info("remote forward closed", "computer", c.Name, "credential", cred.Name, "err", err)
		_ = ws.Close(websocket.StatusPolicyViolation, closeReason(err.Error()))
	}
}

var (
	errHelperEnded = errors.New("the forward helper ended")
	errBadFrame    = errors.New("malformed frame")
)

// relayForward copies frames between the client's WebSocket (one frame per
// binary message) and the helper's stdio (frames with a u32 length prefix)
// until either side ends. Neither side is trusted: a frame of the wrong size
// or type, or a text message, ends the relay with errBadFrame. The helper's
// end is errHelperEnded. changed is called when lk's in ports change.
func relayForward(ctx context.Context, ws *websocket.Conn, toHelper io.Writer, fromHelper io.Reader, lk *forwardLink, changed func()) error {
	done := make(chan error, 2)
	go func() { // client -> helper
		buf := make([]byte, 4+forward.MaxFrame)
		for {
			typ, b, err := ws.Read(ctx)
			if err != nil {
				done <- err
				return
			}
			if typ != websocket.MessageBinary {
				done <- fmt.Errorf("%w: text message after the first", errBadFrame)
				return
			}
			ftyp, id, payload, err := checkFrame(b)
			if err != nil {
				done <- err
				return
			}
			ch, err := lk.fromClient(ftyp, id, payload)
			if err != nil {
				done <- fmt.Errorf("%w: %v", errBadFrame, err)
				return
			}
			if ch {
				changed()
			}
			binary.BigEndian.PutUint32(buf, uint32(len(b)))
			n := copy(buf[4:], b)
			if _, err := toHelper.Write(buf[:4+n]); err != nil {
				done <- errHelperEnded
				return
			}
		}
	}()
	go func() { // helper -> client
		buf := make([]byte, forward.MaxFrame)
		var l [4]byte
		for {
			if _, err := io.ReadFull(fromHelper, l[:]); err != nil {
				done <- errHelperEnded
				return
			}
			n := binary.BigEndian.Uint32(l[:])
			if n > forward.MaxFrame {
				done <- fmt.Errorf("%w: from the helper, %d bytes", errBadFrame, n)
				return
			}
			b := buf[:n]
			if _, err := io.ReadFull(fromHelper, b); err != nil {
				done <- errHelperEnded
				return
			}
			ftyp, id, _, err := checkFrame(b)
			if err != nil {
				done <- err
				return
			}
			if lk.fromHelper(ftyp, id) {
				changed()
			}
			if err := ws.Write(ctx, websocket.MessageBinary, b); err != nil {
				done <- err
				return
			}
		}
	}()
	return <-done
}

// checkFrame decodes a frame and checks its size and type.
func checkFrame(b []byte) (typ byte, id uint32, payload []byte, err error) {
	if len(b) > forward.MaxFrame {
		return 0, 0, nil, fmt.Errorf("%w: %d bytes", errBadFrame, len(b))
	}
	typ, id, payload, err = forward.Peek(b)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("%w: %v", errBadFrame, err)
	}
	if typ < forward.TypeListen || typ > forward.TypeWindow {
		return 0, 0, nil, fmt.Errorf("%w: unknown type %d", errBadFrame, typ)
	}
	return typ, id, payload, nil
}

// keepAlive pings the client so a connection that silently died is closed
// (and its mappings dropped) within a minute.
func keepAlive(ctx context.Context, ws *websocket.Conn) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				ws.CloseNow()
				return
			}
		}
	}
}

// closeReason fits a WebSocket close reason (at most 123 bytes).
func closeReason(s string) string {
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
