package server

import (
	"sync"
	"time"
)

// publishViewers tells the UI who is on a desktop and what is mapped. Calls
// are coalesced per desktop (see throttle), so a client churning mappings
// can't flood the event stream.
func (s *Server) publishViewers(name, environment string) {
	s.viewersPub.do(name, func() { s.sendViewers(name, environment) })
	s.publishActivity(name, environment) // a token's forward item follows its mappings
}

func (s *Server) sendViewers(name, environment string) {
	s.events.publish(event{kind: "viewers", environment: environment,
		data: map[string]any{"name": name, "viewers": s.conns.names(name), "observers": s.conns.observers(name), "mics": s.conns.mics(name), "forwards": s.forwards.list(name),
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
