package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

// hub fans out UI events (/api/events). Every event concerns one desktop;
// each subscriber only receives events of desktops its account can see.
type event struct {
	kind      string // desktop, removed, stats, viewers
	desktopID string // "" for events everyone may see
	data      any
}

type subscriber struct {
	username string
	c        chan event
	refresh  chan struct{}
	overflow chan struct{}
}

type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]bool
}

func newHub() *hub { return &hub{subs: map[*subscriber]bool{}} }

func (h *hub) publish(e event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.c <- e:
		default:
			// Losing an authoritative event requires reconciliation.
			select {
			case s.overflow <- struct{}{}:
			default:
			}
		}
	}
}

// refresh asks every subscriber to resend its full desktop list (access changed).
func (h *hub) refresh() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.refresh <- struct{}{}:
		default:
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	sse := startSSE(w)
	if sse == nil {
		return
	}
	sub := &subscriber{username: a.Username, c: make(chan event, 64), refresh: make(chan struct{}, 1), overflow: make(chan struct{}, 1)}
	s.events.mu.Lock()
	s.events.subs[sub] = true
	s.events.mu.Unlock()
	s.stats.wake()
	s.inside.wake()
	defer func() {
		s.events.mu.Lock()
		delete(s.events.subs, sub)
		s.events.mu.Unlock()
	}()
	send := func(kind string, v any) {
		b, _ := json.Marshal(v)
		sse.send(kind, string(b))
	}
	visible := map[string]bool{} // desktop IDs this stream has shown
	sendAll := func() bool {
		acct, ok := s.Accounts.Get(a.Username)
		if !ok {
			return false
		}
		list, err := s.Desktops.List(r.Context())
		if err != nil {
			return false
		}
		now := map[string]bool{}
		for _, c := range list {
			if acct.HasAccess(c.ID) || (acct.Admin && c.ID == "") {
				now[c.Name] = true
				send("desktop", s.desktopJSON(r.Context(), c, acct))
			}
		}
		for name := range visible {
			if !now[name] {
				send("removed", map[string]string{"name": name})
			}
		}
		visible = now
		return true
	}
	if !sendAll() {
		return
	}
	for {
		select {
		case e := <-sub.c:
			acct, ok := s.Accounts.Get(a.Username)
			if !ok {
				return
			}
			if e.desktopID != "" && !acct.HasAccess(e.desktopID) {
				continue
			}
			// Lifecycle payloads can have queued before the snapshot. Resolve
			// current state rather than allowing an old payload to undo it.
			name := ""
			if c, ok := e.data.(desktops.Desktop); ok {
				name = c.Name
			}
			if e.kind == "removed" {
				if removed, ok := e.data.(map[string]string); ok {
					name = removed["name"]
				}
			}
			if name != "" {
				c, err := s.Desktops.Get(r.Context(), name)
				if err != nil && !errors.Is(err, desktops.ErrNotFound) {
					return
				}
				if err == nil && (acct.HasAccess(c.ID) || (acct.Admin && c.ID == "")) {
					visible[name] = true
					send("desktop", s.desktopJSON(r.Context(), c, acct))
				} else {
					delete(visible, name)
					send("removed", map[string]string{"name": name})
				}
				continue
			}
			send(e.kind, e.data)
		case <-sub.refresh:
			for len(sub.c) > 0 {
				<-sub.c
			}
			if !sendAll() {
				return
			}
		case <-sub.overflow:
			// EventSource reconnects and requests a new authoritative snapshot.
			return
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Second):
			sse.comment()
		}
	}
}

// publishDesktop sends the current state of one desktop to the UI.
func (s *Server) publishDesktop(ctx context.Context, name string) {
	c, err := s.Desktops.Get(ctx, name)
	if err != nil {
		s.events.publish(event{kind: "removed", data: map[string]string{"name": name}})
		return
	}
	s.events.publish(event{kind: "desktop", desktopID: c.ID, data: c})
}

// watchEngine turns engine events about desktops into UI events.
func (s *Server) watchEngine(ctx context.Context) {
	for ctx.Err() == nil {
		evs, err := s.Eng.Events(ctx)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		for e := range evs {
			if s.Desktops.Networking != nil {
				s.Desktops.Networking.NotifyEngineEvent(e)
			}
			name := e.Labels[desktops.LabelName]
			if name == "" || !desktops.IsDesktop(e.Labels) {
				continue
			}
			switch e.Action {
			case "die", "stop", "destroy", "restart":
				s.viewers.closeAll(name)
			}
			switch e.Action {
			case "start", "die", "stop", "create", "destroy", "restart", "pause", "unpause":
				s.publishDesktop(ctx, name)
				s.stats.wake()
				s.inside.wake()
			}
		}
		time.Sleep(time.Second)
	}
}

var _ = accounts.Account{}
