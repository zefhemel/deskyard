package server

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// viewers tracks the open desktop streams per desktop, with the account
// each belongs to, the level it was opened at, the browser tab it comes
// from, whether that tab's mic is on, and a way to close it (when access is
// revoked or changes level, or its browser session ends).
type conn struct {
	username  string
	accountID string
	epoch     int    // the session epoch of the browser login it came from; noSession for a client credential
	tab       string // the page's id for its browser tab ("" if it sent none)
	computer  string
	level     auth.Level // the account's level when it opened; another level closes it
	cancel    context.CancelFunc
	// guarded by viewers.mu: whether the tab's page last said its mic is on,
	// and the sequence number it said it with (a page numbers its reports;
	// older ones lose).
	mic    bool
	micSeq int64
}

type viewers struct {
	mu     sync.Mutex
	byName map[string]map[*conn]bool
	epochs map[string]uint64
}

func newViewers() *viewers {
	return &viewers{byName: map[string]map[*conn]bool{}, epochs: map[string]uint64{}}
}

func (v *viewers) add(name string, c *conn) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.byName[name] == nil {
		v.byName[name] = map[*conn]bool{}
	}
	v.byName[name][c] = true
	v.epochs[name]++
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

// observers lists the accounts watching a desktop at observe level, each
// once, sorted.
func (v *viewers) observers(name string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := []string{}
	for c := range v.byName[name] {
		if c.level == auth.LevelObserve && !slices.Contains(out, c.username) {
			out = append(out, c.username)
		}
	}
	sort.Strings(out)
	return out
}

// setMic turns the mic badge of an account's stream from one tab on or off.
// seq orders a page's reports: one numbered no higher than the last applied
// is ignored (0 is unnumbered and always applies). It reports false when that
// account has no stream from that tab.
func (v *viewers) setMic(name, username, tab string, on bool, seq int64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	found := false
	for c := range v.byName[name] {
		if c.username != username || c.tab != tab {
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

// mics lists the accounts with a mic on in a desktop, each once, sorted.
func (v *viewers) mics(name string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := []string{}
	for c := range v.byName[name] {
		if c.mic && !slices.Contains(out, c.username) {
			out = append(out, c.username)
		}
	}
	sort.Strings(out)
	return out
}

// revoke closes the connections of an account whose level on their desktop
// is no longer the one they were opened at: access removed, or lowered or
// raised (the page reconnects at the new level).
func (v *viewers) revoke(username string, level func(computer string) auth.Level) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, conns := range v.byName {
		for c := range conns {
			if c.username == username && level(c.computer) != c.level {
				c.cancel()
			}
		}
	}
}

// noSession is the epoch of a connection made with a client credential,
// which session epochs don't end.
const noSession = -1

// endSessions closes the connections of an account's browser logins that
// its current session epoch no longer accepts, and any of an earlier
// account of the name.
func (v *viewers) endSessions(username, accountID string, epoch int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, conns := range v.byName {
		for c := range conns {
			if c.username == username && (c.accountID != accountID || c.epoch != noSession && c.epoch != epoch) {
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
func (s *Server) publishViewers(name, computer string) {
	s.viewersPub.do(name, func() { s.sendViewers(name, computer) })
	s.publishActivity(name, computer) // a token's forward item follows its mappings
}

func (s *Server) sendViewers(name, computer string) {
	s.events.publish(event{kind: "viewers", computer: computer,
		data: map[string]any{"name": name, "viewers": s.viewers.names(name), "observers": s.viewers.observers(name), "mics": s.viewers.mics(name), "forwards": s.forwards.list(name),
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

// snapshot retains joins even if a viewer leaves between scheduler ticks.
func (v *viewers) snapshot(name string) (int, uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.byName[name]), v.epochs[name]
}
