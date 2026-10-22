package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

// activityInterval is the least time between two activity events of a desktop.
const activityInterval = time.Second

// newActivity sets up the tracker: presence (a test knob) and the callbacks
// that turn its changes into UI events.
func (s *Server) newActivity(presence time.Duration) *activity.Tracker {
	t := activity.NewTracker()
	if presence > 0 {
		t.Presence = presence
		t.ActiveFor = min(t.ActiveFor, presence)
	}
	// A new agent shows up at once: viewers events go out unthrottled for
	// presence changes, which are few (an agent arrives, leaves, or turns
	// active or idle).
	t.OnPresence = func(d activity.Ref) {
		if d.ID != "" {
			s.sendViewers(d.Name, d.ID)
		}
	}
	t.OnSummary = func(d activity.Ref) { s.publishActivity(d.Name, d.ID) }
	return t
}

// agentOf is the activity agent of an agent token.
func (s *Server) agentOf(t token) activity.Agent {
	return activity.Agent{ID: t.ID, Name: t.Name, Owner: t.CreatedBy}
}

func refOf(c desktops.Desktop) activity.Ref { return activity.Ref{Name: c.Name, ID: c.ID} }

// activityJSON is what agents are doing on a desktop.
type activityJSON struct {
	Name   string             `json:"name"`
	Agents []activity.Summary `json:"agents"`
}

// activityJSON describes the agents on a desktop. A token's open forward
// mappings are its forward item, as they are now ("3000 in · 5173 out").
func (s *Server) activityJSON(name string) activityJSON {
	sums := s.activity.Summary(name)
	for i := range sums {
		sum := &sums[i]
		if sum.Inside {
			continue
		}
		fw := s.forwards.listFor(name, sum.ID)
		if len(fw) == 0 {
			continue
		}
		since := sum.LastActive
		now := []activity.NowItem{}
		for _, it := range sum.Now {
			if it.Kind == "forward" {
				since = it.Since
				continue
			}
			now = append(now, it)
		}
		sum.Now = append(now, activity.NowItem{Kind: "forward", Text: forwardText(fw), Since: since})
	}
	return activityJSON{Name: name, Agents: sums}
}

// forwardText reads "3000 in · 5173 out".
func forwardText(fw []forwardJSON) string {
	parts := make([]string, len(fw))
	for i, f := range fw {
		parts[i] = strconv.Itoa(f.Port) + " " + f.Direction
	}
	return strings.Join(parts, " · ")
}

// publishActivity sends a desktop's activity to the UI, at most once per
// activityInterval (the last state always follows).
func (s *Server) publishActivity(name, desktopID string) {
	if desktopID == "" { // an event without a desktop id would go to everyone
		return
	}
	s.activityPub.do(name, func() {
		s.events.publish(event{kind: "activity", desktopID: desktopID, data: s.activityJSON(name)})
	})
}

// activityLoop lets time pass for the tracker: presence expires, history ages.
func (s *Server) activityLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.activity.Tick()
		}
	}
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.activityJSON(c.Name))
}

// handleScreenEvents streams the screen actions agents make on a desktop,
// for drawing them over the viewer's screen.
func (s *Server) handleScreenEvents(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	allowed := func() bool {
		acct, ok := s.Accounts.Get(a.Username)
		return ok && auth.Can(acct, auth.View, c.ID)
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// Registered so that a revoke closes the stream even while it is idle.
	cn := &conn{username: a.Username, desktopID: c.ID, cancel: cancel}
	s.screenWatchers.add(c.Name, cn)
	defer s.screenWatchers.remove(c.Name, cn)
	if !allowed() { // revoked between desktopFor and add
		writeError(w, http.StatusNotFound, "no such desktop")
		return
	}
	events, stop := s.activity.WatchScreen(c.Name)
	defer stop()
	sse := startSSE(w)
	if sse == nil {
		return
	}
	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case ev, open := <-events:
			if !open || !allowed() {
				return
			}
			b, _ := json.Marshal(ev)
			sse.send("screen", string(b))
		case <-ctx.Done():
			return
		case <-keepAlive.C:
			sse.comment()
		}
	}
}
