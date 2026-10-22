package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/zefhemel/container-computer/supervisor/internal/accounts"
	"github.com/zefhemel/container-computer/supervisor/internal/computers"
)

// hub fans out UI events (/api/events). Every event concerns one computer;
// each subscriber only receives events of computers its account can see.
type event struct {
	kind       string // computer, removed, stats, viewers
	computerID string // "" for events everyone may see
	data       any
}

type subscriber struct {
	username string
	c        chan event
	refresh  chan struct{}
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
		default: // a slow client misses an update; the next one corrects it
		}
	}
}

// refresh asks every subscriber to resend its full computer list (access changed).
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
	sub := &subscriber{username: a.Username, c: make(chan event, 64), refresh: make(chan struct{}, 1)}
	s.events.mu.Lock()
	s.events.subs[sub] = true
	s.events.mu.Unlock()
	s.stats.wake()
	defer func() {
		s.events.mu.Lock()
		delete(s.events.subs, sub)
		s.events.mu.Unlock()
	}()
	send := func(kind string, v any) {
		b, _ := json.Marshal(v)
		sse.send(kind, string(b))
	}
	visible := map[string]bool{} // computer IDs this stream has shown
	sendAll := func() {
		acct, ok := s.Accounts.Get(a.Username)
		if !ok {
			return
		}
		list, err := s.Computers.List(r.Context())
		if err != nil {
			return
		}
		now := map[string]bool{}
		for _, c := range list {
			if acct.HasAccess(c.ID) || (acct.Admin && c.ID == "") {
				now[c.Name] = true
				send("computer", s.computerJSON(r.Context(), c, acct))
			}
		}
		for name := range visible {
			if !now[name] {
				send("removed", map[string]string{"name": name})
			}
		}
		visible = now
	}
	sendAll()
	for {
		select {
		case e := <-sub.c:
			acct, ok := s.Accounts.Get(a.Username)
			if !ok {
				return
			}
			if e.computerID != "" && !acct.HasAccess(e.computerID) {
				continue
			}
			if c, ok := e.data.(computers.Computer); ok {
				visible[c.Name] = true
				send(e.kind, s.computerJSON(r.Context(), c, acct))
				continue
			}
			send(e.kind, e.data)
		case <-sub.refresh:
			sendAll()
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Second):
			sse.comment()
		}
	}
}

// publishComputer sends the current state of one computer to the UI.
func (s *Server) publishComputer(ctx context.Context, name string) {
	c, err := s.Computers.Get(ctx, name)
	if err != nil {
		s.events.publish(event{kind: "removed", data: map[string]string{"name": name}})
		return
	}
	s.events.publish(event{kind: "computer", computerID: c.ID, data: c})
}

// watchEngine turns engine events about computers into UI events.
func (s *Server) watchEngine(ctx context.Context) {
	for ctx.Err() == nil {
		evs, err := s.Eng.Events(ctx)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		for e := range evs {
			name := e.Labels[computers.LabelName]
			if name == "" || e.Labels[computers.LabelManaged] != "true" {
				continue
			}
			switch e.Action {
			case "start", "die", "stop", "create", "destroy", "restart", "pause", "unpause":
				s.publishComputer(ctx, name)
				s.stats.wake()
			}
		}
		time.Sleep(time.Second)
	}
}

var _ = accounts.Account{}
