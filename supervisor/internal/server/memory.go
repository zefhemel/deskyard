package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// detectMemoryReclaim runs once, before the server starts accepting requests.
// Failure disables only this optional feature, not the supervisor.
func (s *Server) detectMemoryReclaim(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := s.Eng.ProbeMemoryReclaim(ctx); err != nil {
		s.memoryReclaim = engine.MemoryReclaimSupport{Reason: err.Error()}
		s.Log.Warn("memory reclamation disabled", "enabled", false, "reason", err.Error())
		return
	}
	s.memoryReclaim = engine.MemoryReclaimSupport{Enabled: true}
	s.Log.Info("memory reclamation enabled", "enabled", true, "verified", []string{
		"rootful Linux Docker", "cgroup v2 memory and swap controls", "active host swap",
		"memory.reclaim with swappiness", "helper image, host mounts and cgroup namespace", "helper cleanup",
	})
}

func (s *Server) handleReclaimMemory(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Manage)
	if !ok {
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, environments.ErrStopped.Error())
		return
	}
	if !s.memoryReclaim.Enabled {
		writeError(w, http.StatusNotImplemented, s.memoryReclaim.Reason)
		return
	}
	// Keep the desktop running, while excluding concurrent lifecycle/reclaim requests.
	release, err := s.Environments.Reserve(c.Name, "")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	ctx, cancel := timeout(r, 45*time.Second)
	defer cancel()
	result, err := s.Eng.ReclaimMemory(ctx, c.Container.ID)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, engine.ErrReclaimUnsupported) {
			code = http.StatusNotImplemented
		}
		writeError(w, code, err.Error())
		return
	}
	s.Log.Info("reclaimed memory", "environment", c.Name, "by", a.Username,
		"before", result.BeforeBytes, "after", result.AfterBytes, "partial", result.Partial)
	writeJSON(w, http.StatusOK, result)
}

// handleMemoryPolicy saves an environment's memory reclaim preference in its
// definition, without touching its container.
func (s *Server) handleMemoryPolicy(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.environmentFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		MemoryReclaim *bool `json:"memoryReclaim"`
	}
	if err := readJSON(r, &req); err != nil || req.MemoryReclaim == nil {
		writeError(w, http.StatusBadRequest, "memoryReclaim must be a boolean")
		return
	}
	if err := s.Environments.SetMemoryReclaim(c.Name, *req.MemoryReclaim); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.inside.wake()
	s.publishEnvironment(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}
