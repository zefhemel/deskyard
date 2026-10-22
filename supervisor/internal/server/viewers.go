package server

import (
	"context"
	"slices"
	"sort"
	"sync"
)

// viewers tracks the open desktop connections per computer, with the account
// each belongs to and a way to close it (when access is revoked).
type conn struct {
	username   string
	computerID string
	cancel     context.CancelFunc
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

// names lists the accounts viewing a computer, each once, sorted.
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

// revoke closes the connections of an account to computers it may no longer use.
func (v *viewers) revoke(username string, stillAllowed func(computerID string) bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, conns := range v.byName {
		for c := range conns {
			if c.username == username && !stillAllowed(c.computerID) {
				c.cancel()
			}
		}
	}
}

// closeAll closes every connection to a computer (stopped, removed).
func (v *viewers) closeAll(name string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for c := range v.byName[name] {
		c.cancel()
	}
}

func (s *Server) publishViewers(name, computerID string) {
	s.events.publish(event{kind: "viewers", computerID: computerID,
		data: map[string]any{"name": name, "viewers": s.viewers.names(name)}})
}
