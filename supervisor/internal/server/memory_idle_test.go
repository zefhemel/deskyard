package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

func TestIdleMemoryReclaim(t *testing.T) {
	for _, blocker := range []string{"none", "viewer", "operation", "agent", "inside agent", "unknown activity", "opt out", "unsupported", "failure"} {
		t.Run(blocker, func(t *testing.T) {
			e := runningReclaimEngine()
			s, _ := newTestServerWith(t, e)
			s.inside = newInsideReader(s)
			now := time.Now()
			s.activity.Now = func() time.Time { return now }
			states := map[string]*idleMemory{}
			tick := func() {
				s.inside.mu.Lock()
				s.inside.healthy["bob"] = now
				s.inside.mu.Unlock()
				if blocker == "unknown activity" {
					s.inside.mu.Lock()
					delete(s.inside.healthy, "bob")
					s.inside.mu.Unlock()
				}
				s.reclaimIdle(context.Background(), now, states)
			}
			tick()
			now = now.Add(14 * time.Minute)
			tick()
			if e.calls.Load() != 0 {
				t.Fatal("reclaimed early")
			}
			v := &conn{username: "admin"}
			switch blocker {
			case "viewer":
				s.viewers.add("bob", v)
				s.viewers.remove("bob", v)
			case "operation":
				if err := s.Environments.Begin("bob", ""); err != nil {
					t.Fatal(err)
				}
			case "agent":
				s.activity.CmdStart(activity.Ref{Name: "bob", ID: "bob"}, activity.Agent{ID: "a", Name: "agent"}, "cmd", "sleep")
			case "inside agent":
				s.activity.Inside(activity.Ref{Name: "bob", ID: "bob"}, activity.Event{Agent: "terminal", Kind: "cmd-start", ID: "cmd", Cmd: "sleep", T: now})
			case "opt out":
				if err := s.Environments.SetMemoryReclaim("bob", false); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				s.memoryReclaim.Enabled = false
			case "failure":
				e.err = errors.New("failed")
			}
			now = now.Add(20 * time.Minute)
			tick()
			blocked := blocker != "none" && blocker != "failure"
			if got := e.calls.Load(); (got == 0) != blocked {
				t.Fatalf("calls %d, blocked %v", got, blocked)
			}
			if blocker == "operation" {
				s.Environments.Done("bob")
				tick()
				if e.calls.Load() != 1 {
					t.Fatal("busy consumed attempt")
				}
			}
			if blocker == "viewer" || blocker == "opt out" {
				if blocker == "opt out" {
					if err := s.Environments.SetMemoryReclaim("bob", true); err != nil {
						t.Fatal(err)
					}
					tick()
				}
				now = now.Add(15 * time.Minute)
				tick()
				if e.calls.Load() != 1 {
					t.Fatal("did not reset timer")
				}
			}
			now = now.Add(time.Hour)
			tick()
			if !blocked || blocker == "operation" || blocker == "viewer" || blocker == "opt out" {
				if e.calls.Load() != 1 {
					t.Fatal("repeated reclaim", e.calls.Load())
				}
			}
		})
	}
}

func TestIdleMemoryViewerAndRestartReset(t *testing.T) {
	e := runningReclaimEngine()
	s, _ := newTestServerWith(t, e)
	s.inside = newInsideReader(s)
	now := time.Now()
	states := map[string]*idleMemory{}
	tick := func() {
		s.inside.mu.Lock()
		s.inside.healthy["bob"] = now
		s.inside.mu.Unlock()
		s.reclaimIdle(context.Background(), now, states)
	}
	tick()
	now = now.Add(15 * time.Minute)
	tick()
	if e.calls.Load() != 1 {
		t.Fatal("first attempt missing")
	}
	viewer := &conn{}
	s.viewers.add("bob", viewer)
	s.viewers.remove("bob", viewer)
	tick()
	now = now.Add(14 * time.Minute)
	tick()
	if e.calls.Load() != 1 {
		t.Fatal("viewer reset too short")
	}
	now = now.Add(time.Minute)
	tick()
	if e.calls.Load() != 2 {
		t.Fatal("viewer did not rearm")
	}
	// Restarting the same container changes StartedAt, not its ID.
	e.others[0].StartedAt = now
	tick()
	now = now.Add(14 * time.Minute)
	tick()
	if e.calls.Load() != 2 {
		t.Fatal("restart reset too short")
	}
	now = now.Add(time.Minute)
	tick()
	if e.calls.Load() != 3 {
		t.Fatal("restart did not rearm")
	}
}

func TestIdleMemoryBusyAgentFinishes(t *testing.T) {
	e := runningReclaimEngine()
	s, _ := newTestServerWith(t, e)
	s.inside = newInsideReader(s)
	now := time.Now()
	s.activity.Now = func() time.Time { return now }
	states := map[string]*idleMemory{}
	tick := func() {
		s.inside.mu.Lock()
		s.inside.healthy["bob"] = now
		s.inside.mu.Unlock()
		s.reclaimIdle(context.Background(), now, states)
	}
	ref := activity.Ref{Name: "bob", ID: "bob"}
	agent := activity.Agent{ID: "a", Name: "bot"}
	s.activity.CmdStart(ref, agent, "work", "sleep")
	tick()
	now = now.Add(time.Hour)
	tick()
	if e.calls.Load() != 0 {
		t.Fatal("reclaimed busy agent")
	}
	s.activity.CmdEnd(ref, agent, "work", nil)
	now = now.Add(time.Minute)
	tick()
	if e.calls.Load() != 1 {
		t.Fatal("busy skip consumed attempt")
	}
}
