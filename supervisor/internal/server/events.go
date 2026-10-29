package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
)

// hub fans out UI events (/api/events). Every event concerns one computer;
// each subscriber only receives events of computers its account can see.
type event struct {
	kind     string // computer, removed, stats, viewers, activity
	computer string // "" for events everyone may see
	data     any
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

// refresh asks every subscriber to resend its full computer list (access
// or conflicts changed).
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

// eventFor is the event as the account may receive it, false when it may
// not: an observer gets no activity, and viewers events without the
// mappings and clients full users have open.
func eventFor(a accounts.Account, e event) (event, bool) {
	if e.computer == "" {
		return e, true
	}
	switch level := a.Level(e.computer); {
	case level == auth.LevelNone:
		return e, false
	case level == auth.LevelFull:
		return e, true
	case e.kind == "activity":
		return e, false
	case e.kind == "viewers":
		if data, ok := e.data.(map[string]any); ok {
			observed := make(map[string]any, len(data))
			for k, v := range data {
				observed[k] = v
			}
			observed["forwards"], observed["agents"] = []forwardJSON{}, []activity.Presence{}
			e.data = observed
		}
	}
	return e, true
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	who, _ := s.principal(r)
	a := who.account
	r, untrack := s.trackStream(r)
	defer untrack()
	ctx, sse, closeStream := s.startUIEvents(w, r)
	if sse == nil {
		return
	}
	defer closeStream()
	r = r.WithContext(ctx)
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
	visible := map[string]bool{}  // computer names this stream has shown
	conflicts := []conflictJSON{} // the conflicts this stream has shown
	// The stream ends once the account is gone, expired or replaced, also
	// when deleting it failed, once its browser login ended (a bumped
	// session epoch; the page reconnects with its new cookie), and once its
	// credential is deleted.
	sendAll := func() bool {
		acct, ok := s.accountNow(a.Username, a.ID, time.Now())
		if !ok || !who.signedIn(acct) {
			return false
		}
		list, err := s.Computers.List(r.Context())
		if err != nil {
			return false
		}
		now := map[string]bool{}
		for _, c := range list {
			if auth.Can(acct, auth.Observe, c.ID) {
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
		// Conflicts are sent when they change; a stream starts without any.
		if now := s.conflictsJSON(acct); !reflect.DeepEqual(now, conflicts) {
			conflicts = now
			send("conflicts", conflicts)
		}
		return true
	}
	if !sendAll() {
		return
	}
	for {
		select {
		case e := <-sub.c:
			acct, ok := s.accountNow(a.Username, a.ID, time.Now())
			if !ok {
				return
			}
			e, ok = eventFor(acct, e)
			if !ok {
				continue
			}
			// Lifecycle payloads can have queued before the snapshot. Resolve
			// current state rather than allowing an old payload to undo it.
			name := ""
			if c, ok := e.data.(computers.Computer); ok {
				name = c.Name
			}
			if e.kind == "removed" {
				if removed, ok := e.data.(map[string]string); ok {
					name = removed["name"]
				}
			}
			if name != "" {
				c, err := s.Computers.Get(r.Context(), name)
				if err != nil && !errors.Is(err, computers.ErrNotFound) {
					return
				}
				if err == nil && auth.Can(acct, auth.Observe, c.ID) {
					visible[name] = true
					send("computer", s.computerJSON(r.Context(), c, acct))
				} else if visible[name] {
					// Only of one it could see: others' names aren't its to know.
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
			// The client reconnects and requests a new authoritative snapshot.
			return
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
	s.events.publish(event{kind: "computer", computer: c.Name, data: c})
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
			if s.Computers.Networking != nil {
				s.Computers.Networking.NotifyEngineEvent(e)
			}
			name := e.Labels[computers.LabelName]
			if name == "" || !computers.IsComputer(e.Labels) {
				continue
			}
			switch e.Action {
			case "die", "stop", "destroy", "restart":
				s.viewers.closeAll(name)
			}
			switch e.Action {
			case "die", "destroy", "create":
				// A crash or a lost container is reconciled now; a created
				// one may duplicate a computer.
				select {
				case s.reconcile <- struct{}{}:
				default:
				}
			}
			switch e.Action {
			case "start", "die", "stop", "create", "destroy", "restart", "pause", "unpause":
				s.publishComputer(ctx, name)
				s.stats.wake()
				s.inside.wake()
			}
		}
		time.Sleep(time.Second)
	}
}

var _ = accounts.Account{}
