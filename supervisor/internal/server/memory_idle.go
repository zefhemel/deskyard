package server

import (
	"context"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const idleMemoryDelay = 15 * time.Minute

type idleMemory struct {
	container   string
	started     time.Time
	viewerEpoch uint64
	policyEpoch uint64
	since       time.Time
	attempted   bool
}

func (s *Server) idleMemoryLoop(ctx context.Context) {
	if !s.memoryReclaim.Enabled {
		return
	}
	states := map[string]*idleMemory{}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		s.reclaimIdle(ctx, time.Now(), states)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Only this loop owns states. Busy/unknown activity skips keep their original
// timer; an actual attempt (including failure) consumes this absence's one shot.
func (s *Server) reclaimIdle(ctx context.Context, now time.Time, states map[string]*idleMemory) {
	if !s.memoryReclaim.Enabled {
		return
	}
	list, err := s.Desktops.List(ctx)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, c := range list {
		if ctx.Err() != nil {
			return
		}
		live[c.ID] = true
		count, epoch := s.viewers.snapshot(c.Name)
		enabled, policyEpoch := s.memoryPolicy.snapshot(c.ID)
		if c.Container.State != "running" || !enabled || count > 0 {
			delete(states, c.ID)
			continue
		}
		st := states[c.ID]
		if st == nil || st.container != c.Container.ID || !st.started.Equal(c.StartedAt) || st.viewerEpoch != epoch || st.policyEpoch != policyEpoch {
			states[c.ID] = &idleMemory{container: c.Container.ID, started: c.StartedAt, viewerEpoch: epoch, policyEpoch: policyEpoch, since: now}
			continue
		}
		if st.attempted || now.Sub(st.since) < idleMemoryDelay {
			continue
		}
		release, err := s.Desktops.Reserve(c.Name, "")
		if err != nil {
			continue
		}
		s.reclaimIdleDesktop(ctx, now, c, st)
		release()
	}
	for id := range states {
		if !live[id] {
			delete(states, id)
		}
	}
}

func (s *Server) reclaimIdleDesktop(ctx context.Context, now time.Time, c desktops.Desktop, st *idleMemory) {
	// Re-read after acquiring the lifecycle reservation, so a completed restart
	// or newly arrived viewer cannot be hidden by the earlier list snapshot.
	fresh, err := s.Desktops.Get(ctx, c.Name)
	if err != nil || fresh.ID != c.ID || fresh.Container.ID != st.container || fresh.Container.State != "running" || !fresh.StartedAt.Equal(st.started) {
		return
	}
	count, epoch := s.viewers.snapshot(c.Name)
	enabled, policyEpoch := s.memoryPolicy.snapshot(c.ID)
	if count > 0 || epoch != st.viewerEpoch || (!enabled || policyEpoch != st.policyEpoch) || !s.inside.ready(c.Name, now) {
		return
	}
	for _, a := range s.activity.Summary(c.Name) {
		if a.Active || len(a.Now) > 0 {
			return
		}
	}
	reclaimer, ok := s.Eng.(engine.MemoryReclaimer)
	if !ok {
		return
	}
	st.attempted = true
	request, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, err := reclaimer.ReclaimMemory(request, c.Container.ID)
	if err != nil {
		s.Log.Warn("automatic memory reclamation failed", "desktop", c.Name, "err", err)
		return
	}
	s.Log.Info("reclaimed memory", "desktop", c.Name, "trigger", "automatic", "before", result.BeforeBytes, "after", result.AfterBytes, "partial", result.Partial)
}
