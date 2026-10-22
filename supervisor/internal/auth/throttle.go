package auth

import (
	"sync"
	"time"
)

// Throttle slows down password guessing: after 5 failed logins for one key
// (a username or an IP), one attempt per 10 seconds for that key.
type Throttle struct {
	mu   sync.Mutex
	keys map[string]*attempts
	Now  func() time.Time
}

type attempts struct {
	failures int
	last     time.Time
}

const (
	freeFailures = 5
	slowInterval = 10 * time.Second
)

func (t *Throttle) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Allow reports whether a login attempt for all keys may proceed now.
func (t *Throttle) Allow(keys ...string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		a := t.keys[k]
		if a != nil && a.failures >= freeFailures && t.now().Sub(a.last) < slowInterval {
			return false
		}
	}
	return true
}

func (t *Throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.keys == nil {
		t.keys = map[string]*attempts{}
	}
	for _, k := range keys {
		a := t.keys[k]
		if a == nil {
			a = &attempts{}
			t.keys[k] = a
		}
		a.failures++
		a.last = t.now()
	}
}

func (t *Throttle) Succeed(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.keys, k)
	}
}
