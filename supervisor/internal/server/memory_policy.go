package server

import (
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// Opt-outs belong to stable desktop IDs, surviving container recreation.
// A separate store lets this preference change without restarting a desktop.
type memoryPolicy struct {
	mu        sync.Mutex
	path      string
	disabled  map[string]bool
	revisions map[string]uint64
}

func loadMemoryPolicy(path string) (*memoryPolicy, error) {
	p := &memoryPolicy{path: path, disabled: map[string]bool{}, revisions: map[string]uint64{}}
	if err := store.ReadJSON(path, &p.disabled); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if p.disabled == nil {
		p.disabled = map[string]bool{}
	}
	return p, nil
}
func (p *memoryPolicy) enabled(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.disabled[id]
}
func (p *memoryPolicy) snapshot(id string) (bool, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.disabled[id], p.revisions[id]
}
func (p *memoryPolicy) set(id string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := maps.Clone(p.disabled)
	if enabled {
		delete(next, id)
	} else {
		next[id] = true
	}
	if err := store.WriteJSON(p.path, next); err != nil {
		return err
	}
	if p.disabled[id] != next[id] {
		p.revisions[id]++
	}
	p.disabled = next
	return nil
}
func (s *Server) handleMemoryPolicy(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		AutoReclaim *bool `json:"autoReclaim"`
	}
	if err := readJSON(r, &req); err != nil || req.AutoReclaim == nil {
		writeError(w, http.StatusBadRequest, "autoReclaim must be a boolean")
		return
	}
	if err := s.memoryPolicy.set(c.ID, *req.AutoReclaim); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.inside.wake()
	s.publishDesktop(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}
