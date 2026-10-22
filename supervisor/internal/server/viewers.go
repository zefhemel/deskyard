package server

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// viewers tracks the open desktop connections per desktop, with the account
// each belongs to and a way to close it (when access is revoked).
type conn struct {
	username  string
	desktopID string
	cancel    context.CancelFunc
}

type viewers struct {
	mu     sync.Mutex
	byName map[string]map[*conn]bool
}

func newViewers() *viewers { return &viewers{byName: map[string]map[*conn]bool{}} }

func (v *viewers) add(name string, c *conn) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.byName[name] == nil {
		v.byName[name] = map[*conn]bool{}
	}
	v.byName[name][c] = true
}

func (v *viewers) remove(name string, c *conn) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.byName[name], c)
	if len(v.byName[name]) == 0 {
		delete(v.byName, name)
	}
}

// names lists the accounts viewing a desktop, each once, sorted.
func (v *viewers) names(name string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := []string{}
	for c := range v.byName[name] {
		if !slices.Contains(out, c.username) {
			out = append(out, c.username)
		}
	}
	sort.Strings(out)
	return out
}

// revoke closes the connections of an account to desktops it may no longer use.
func (v *viewers) revoke(username string, stillAllowed func(desktopID string) bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, conns := range v.byName {
		for c := range conns {
			if c.username == username && !stillAllowed(c.desktopID) {
				c.cancel()
			}
		}
	}
}

// closeAll closes every connection to a desktop (stopped, removed).
func (v *viewers) closeAll(name string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for c := range v.byName[name] {
		c.cancel()
	}
}

// publishViewers tells the UI who is on a desktop and what is mapped. Calls
// are coalesced per desktop (see throttle), so a client churning mappings
// can't flood the event stream.
func (s *Server) publishViewers(name, desktopID string) {
	s.viewersPub.do(name, func() { s.sendViewers(name, desktopID) })
	s.publishActivity(name, desktopID) // a token's forward item follows its mappings
}

func (s *Server) sendViewers(name, desktopID string) {
	s.events.publish(event{kind: "viewers", desktopID: desktopID,
		data: map[string]any{"name": name, "viewers": s.viewers.names(name), "forwards": s.forwards.list(name),
			"agents": s.activity.Present(name)}})
}

// viewersInterval is the least time between two viewers events of a desktop.
const viewersInterval = 250 * time.Millisecond

// throttle runs a keyed action at most once per interval. A call inside the
// interval schedules one trailing run (with that call's function), so the
// last state is always delivered.
type throttle struct {
	interval time.Duration
	mu       sync.Mutex
	keys     map[string]*throttleKey
}

type throttleKey struct {
	last    time.Time
	pending func() // set while a trailing run is scheduled
	timer   *time.Timer
}

func newThrottle(interval time.Duration) *throttle {
	return &throttle{interval: interval, keys: map[string]*throttleKey{}}
}

func (t *throttle) do(key string, fn func()) {
	t.mu.Lock()
	k := t.keys[key]
	if k == nil {
		k = &throttleKey{}
		t.keys[key] = k
	}
	if k.pending != nil { // a trailing run is already scheduled: it takes the latest
		k.pending = fn
		t.mu.Unlock()
		return
	}
	wait := t.interval - time.Since(k.last)
	if wait <= 0 {
		k.last = time.Now()
		t.mu.Unlock()
		fn()
		return
	}
	k.pending = fn
	k.timer = time.AfterFunc(wait, func() {
		t.mu.Lock()
		run := k.pending
		k.pending = nil
		k.last = time.Now()
		t.mu.Unlock()
		run()
	})
	t.mu.Unlock()
}
