package server

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// noSession is the epoch of a connection made with a client credential (or
// WebDAV's Basic auth), which session epochs don't end.
const noSession = -1

// conn is a live connection: a desktop stream or terminal (the desktop's
// viewers), a screen-events stream, a file transfer, an event stream, or a
// remote access or WebDAV request. It holds who opened it, what it needs,
// and how to close it, which happens once allowed refuses it: on every
// account change and sweep, and when its credential is deleted.
type conn struct {
	username   string
	accountID  string
	epoch      int       // the session epoch of the browser login it came from; noSession for a client credential
	credential string    // the ID of the client credential it came with, if any
	expires    time.Time // when its browser login ends (a terminal's); zero: with its account
	// environment is what it uses, "" for none: such a connection (an event
	// stream) checks its account's access itself, and closes only with
	// its sign-in.
	environment string
	// level, when set, is the account's level on the environment when it
	// opened, and another level closes it (the page reconnects at the new
	// one); without, it needs need on the environment.
	level  auth.Level
	need   auth.Action
	cancel context.CancelFunc

	// A desktop's viewer: a desktop stream or terminal, listed as on it.
	viewer bool
	tab    string // the page's id for its browser tab ("" if it sent none)
	// guarded by connections.mu: whether the tab's page last said its mic
	// is on, and the sequence number it said it with (a page numbers its
	// reports; older ones lose).
	mic    bool
	micSeq int64
}

// connOf is a live connection of the request's principal to the
// environment, needing full use of it (View), closed with cancel.
func (s *Server) connOf(r *http.Request, environment string, cancel context.CancelFunc) *conn {
	p, _ := s.principal(r)
	c := &conn{username: p.account.Username, accountID: p.account.ID, epoch: p.epoch(), environment: environment, need: auth.View, cancel: cancel}
	if p.credential != nil {
		c.credential = p.credential.ID
	}
	return c
}

// allowed reports whether c may stay open: its account is still the one it
// was opened as and usable, its sign-in (browser login or credential)
// holds, and its account may still use its environment as it needs.
func (s *Server) allowed(c *conn) bool {
	now := time.Now()
	a, ok := s.Accounts.Usable(c.username, c.accountID, now)
	switch {
	case !ok, c.epoch != noSession && c.epoch != a.SessionEpoch, !c.expires.IsZero() && !now.Before(c.expires),
		c.credential != "" && !s.credentials.has(c.credential):
		return false
	case c.environment == "":
		return true
	case c.level != auth.LevelNone:
		return a.Level(c.environment) == c.level
	default:
		return auth.Can(a, c.need, c.environment)
	}
}

// open registers c and reports whether it is allowed now that it is
// registered, so that a revocation at any point after the check closes
// it; when it isn't, it is unregistered again. done unregisters it.
func (s *Server) open(c *conn) (done func(), ok bool) {
	s.conns.add(c)
	if !s.allowed(c) {
		s.conns.remove(c)
		return func() {}, false
	}
	return func() { s.conns.remove(c) }, true
}

// revalidate closes the live connections allowed no longer allows, of the
// named accounts, or of all without names.
func (s *Server) revalidate(usernames ...string) {
	for _, c := range s.conns.list(func(c *conn) bool { return len(usernames) == 0 || slices.Contains(usernames, c.username) }) {
		if !s.allowed(c) {
			c.cancel()
		}
	}
}

// track registers a long-lived stream that has no environment, as an
// event stream, so that its sign-in's end (its credential deleted, its
// browser login ended) closes it; the stream checks its account's access
// itself. The returned request carries the stream's context, and done
// unregisters it.
func (s *Server) track(r *http.Request) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	c := s.connOf(r, "", cancel)
	done, ok := s.open(c)
	if !ok {
		cancel()
	}
	return r.WithContext(ctx), func() {
		done()
		cancel()
	}
}

// connections are the live connections, with each desktop's viewers.
type connections struct {
	mu  sync.Mutex
	all map[*conn]bool
	// joins counts the viewers each desktop ever had.
	joins map[string]uint64
}

func newConnections() *connections {
	return &connections{all: map[*conn]bool{}, joins: map[string]uint64{}}
}

func (cs *connections) add(c *conn) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.all[c] = true
	if c.viewer {
		cs.joins[c.environment]++
	}
}

func (cs *connections) remove(c *conn) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.all, c)
}

// list returns the connections fn accepts.
func (cs *connections) list(fn func(c *conn) bool) []*conn {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	var out []*conn
	for c := range cs.all {
		if fn(c) {
			out = append(out, c)
		}
	}
	return out
}

// closeCredential closes the connections made with the client credential.
func (cs *connections) closeCredential(id string) {
	for _, c := range cs.list(func(c *conn) bool { return c.credential == id }) {
		c.cancel()
	}
}

// connected reports whether the account with this ID has a live connection
// made with a client credential.
func (cs *connections) connected(accountID string) bool {
	return len(cs.list(func(c *conn) bool { return c.accountID == accountID && c.credential != "" })) > 0
}

// viewersOf lists, each once and sorted, the accounts of a desktop's viewers
// that fn accepts.
func (cs *connections) viewersOf(name string, fn func(c *conn) bool) []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := []string{}
	for c := range cs.all {
		if c.viewer && c.environment == name && fn(c) && !slices.Contains(out, c.username) {
			out = append(out, c.username)
		}
	}
	sort.Strings(out)
	return out
}

// names lists the accounts viewing a desktop.
func (cs *connections) names(name string) []string {
	return cs.viewersOf(name, func(*conn) bool { return true })
}

// observers lists the accounts watching a desktop at observe level.
func (cs *connections) observers(name string) []string {
	return cs.viewersOf(name, func(c *conn) bool { return c.level == auth.LevelObserve })
}

// mics lists the accounts with a mic on in a desktop.
func (cs *connections) mics(name string) []string {
	return cs.viewersOf(name, func(c *conn) bool { return c.mic })
}

// setMic turns the mic badge of an account's stream from one tab on or off.
// seq orders a page's reports: one numbered no higher than the last applied
// is ignored (0 is unnumbered and always applies). It reports false when that
// account has no stream from that tab.
func (cs *connections) setMic(name, username, tab string, on bool, seq int64) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	found := false
	for c := range cs.all {
		if !c.viewer || c.environment != name || c.username != username || c.tab != tab {
			continue
		}
		found = true
		if seq > 0 && seq <= c.micSeq {
			continue
		}
		c.mic = on
		if seq > 0 {
			c.micSeq = seq
		}
	}
	return found
}

// closeAll closes every viewer of a desktop (stopped, removed).
func (cs *connections) closeAll(name string) {
	for _, c := range cs.list(func(c *conn) bool { return c.viewer && c.environment == name }) {
		c.cancel()
	}
}

// snapshot is how many viewers a desktop has, and how many it ever had,
// which counts joins even when a viewer leaves between scheduler ticks.
func (cs *connections) snapshot(name string) (int, uint64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	n := 0
	for c := range cs.all {
		if c.viewer && c.environment == name {
			n++
		}
	}
	return n, cs.joins[name]
}
