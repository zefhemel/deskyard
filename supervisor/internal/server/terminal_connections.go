package server

import (
	"context"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

type terminalConnection struct {
	claims      auth.SessionClaims
	environment string
	cancel      context.CancelFunc
}

// Registration and logout share a lock so logout also rejects in-flight joins.
// Revoked IDs live only until the last possible issued cookie expires.
type terminalConnections struct {
	mu      sync.Mutex
	active  map[*terminalConnection]bool
	revoked map[string]time.Time
	stopped bool
}

func newTerminalConnections() *terminalConnections {
	return &terminalConnections{active: map[*terminalConnection]bool{}, revoked: map[string]time.Time{}}
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
		if live.environment == c.environment {
			desktops++
		}
	}
	if users >= 16 || desktops >= 32 {
		return false
	}
	t.active[c] = true
	return true
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
func (t *terminalConnections) shutdown() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	for c := range t.active {
		c.cancel()
	}
}
func (s *Server) terminalAllowed(c *terminalConnection) bool {
	a, ok := s.Accounts.Get(c.claims.Username)
	return ok && a.SessionEpoch == c.claims.Epoch && auth.Can(a, auth.View, c.environment) && time.Now().Before(c.claims.Expires)
}
