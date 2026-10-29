package server

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

type terminalConnection struct {
	claims   auth.SessionClaims
	computer string
	session  string
	join     bool // a join-only attachment, which never creates its session
	cancel   context.CancelFunc

	// ending counts the requests ending this session that it is part of; end
	// closes once one of them has ended it, and wake signals a withdrawal.
	ending  atomic.Int32
	end     chan struct{}
	endOnce sync.Once
	wake    chan struct{}
}

// waitEnding waits, while the connection's session is being ended, until that
// finishes, and reports whether it ended.
func (c *terminalConnection) waitEnding(ctx context.Context) bool {
	limit := time.NewTimer(15 * time.Second)
	defer limit.Stop()
	for c.ending.Load() > 0 {
		select {
		case <-c.end:
			return true
		case <-c.wake:
		case <-ctx.Done():
			return isClosed(c.end)
		case <-limit.C:
			return isClosed(c.end)
		}
	}
	return isClosed(c.end)
}

type terminalSessionKey struct{ computer, session string }

// terminalEnding is one request ending a session. Its members are the
// attachments to the session registered before it finished: those present
// when it began, and joins made while the session was being ended. A
// creating attachment registered meanwhile may start a new session of the
// name, so it isn't a member.
type terminalEnding struct {
	t       *terminalConnections
	key     terminalSessionKey
	members []*terminalConnection
}

func (e *terminalEnding) mark(c *terminalConnection) {
	c.ending.Add(1)
	e.members = append(e.members, c)
}

// finish tells the members whether their session ended, and stops marking
// new attachments.
func (e *terminalEnding) finish(ended bool) {
	t := e.t
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endings[e.key] = slices.DeleteFunc(t.endings[e.key], func(o *terminalEnding) bool { return o == e })
	if len(t.endings[e.key]) == 0 {
		delete(t.endings, e.key)
	}
	for _, c := range e.members {
		if ended {
			c.endOnce.Do(func() { close(c.end) })
		}
		c.ending.Add(-1)
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// Registration and logout share a lock so logout also rejects in-flight joins.
// Revoked IDs live only until the last possible issued cookie expires.
type terminalConnections struct {
	mu      sync.Mutex
	active  map[*terminalConnection]bool
	revoked map[string]time.Time
	stopped bool
	endings map[terminalSessionKey][]*terminalEnding
}

func newTerminalConnections() *terminalConnections {
	return &terminalConnections{active: map[*terminalConnection]bool{}, revoked: map[string]time.Time{}, endings: map[terminalSessionKey][]*terminalEnding{}}
}
func (t *terminalConnections) add(c *terminalConnection) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, until := range t.revoked {
		if time.Now().After(until) {
			delete(t.revoked, id)
		}
	}
	if t.stopped || !t.revoked[c.claims.ID].IsZero() {
		return false
	}
	users, desktops := 0, 0
	for live := range t.active {
		if live.claims.Username == c.claims.Username {
			users++
		}
		if live.computer == c.computer {
			desktops++
		}
	}
	if users >= 16 || desktops >= 32 {
		return false
	}
	c.end = make(chan struct{})
	c.wake = make(chan struct{}, 1)
	t.active[c] = true
	if c.join {
		for _, e := range t.endings[terminalSessionKey{c.computer, c.session}] {
			e.mark(c)
		}
	}
	return true
}

// beginEnding marks the attachments to a computer's session as being
// ended, until the returned ending finishes.
func (t *terminalConnections) beginEnding(computer, session string) *terminalEnding {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := &terminalEnding{t: t, key: terminalSessionKey{computer, session}}
	for c := range t.active {
		if c.computer == computer && c.session == session {
			e.mark(c)
		}
	}
	t.endings[e.key] = append(t.endings[e.key], e)
	return e
}
func (t *terminalConnections) remove(c *terminalConnection) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, c)
}
func (t *terminalConnections) logout(c auth.SessionClaims) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked[c.ID] = time.Now().Add(30 * 24 * time.Hour)
	for live := range t.active {
		if live.claims.ID == c.ID {
			live.cancel()
		}
	}
}
func (t *terminalConnections) revalidate(s *Server, username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for c := range t.active {
		if c.claims.Username == username && !s.terminalAllowed(c) {
			c.cancel()
		}
	}
}

// closeAccount closes every terminal of the account, as when it should be
// gone but deleting it failed.
func (t *terminalConnections) closeAccount(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for c := range t.active {
		if c.claims.Username == username {
			c.cancel()
		}
	}
}
func (t *terminalConnections) shutdown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	for c := range t.active {
		c.cancel()
	}
}
func (s *Server) terminalAllowed(c *terminalConnection) bool {
	a, ok := s.accountNow(c.claims.Username, c.claims.AccountID, time.Now())
	return ok && a.SessionEpoch == c.claims.Epoch &&
		auth.Can(a, auth.View, c.computer) && time.Now().Before(c.claims.Expires)
}
