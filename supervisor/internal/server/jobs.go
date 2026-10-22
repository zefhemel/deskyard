package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// A job is a long operation (create, upgrade, image pull) whose progress the
// UI follows over server-sent events at /api/jobs/{id}.
type jobState struct {
	Status   string   `json:"status"` // running, done, failed
	Message  string   `json:"message"`
	Progress *float64 `json:"progress"`
	Error    string   `json:"error,omitempty"`
	Desktop  string   `json:"desktop,omitempty"`
}

type job struct {
	mu    sync.Mutex
	owner string
	state jobState
	subs  []chan jobState
	done  chan struct{}
}

type jobs struct {
	mu   sync.Mutex
	byID map[string]*job
}

func newJobs() *jobs { return &jobs{byID: map[string]*job{}} }

// start runs fn in the background as a job owned by an account.
func (js *jobs) start(owner, desktop string, fn func(ctx context.Context, progress func(string, float64)) error) string {
	id := randomID(8)
	j := &job{owner: owner, state: jobState{Status: "running", Message: "Starting", Desktop: desktop}, done: make(chan struct{})}
	js.mu.Lock()
	js.byID[id] = j
	js.mu.Unlock()
	go func() {
		err := fn(context.Background(), func(msg string, frac float64) {
			st := j.get()
			st.Message = msg
			st.Progress = nil
			if frac >= 0 {
				st.Progress = &frac
			}
			j.set(st)
		})
		st := j.get()
		st.Progress = nil
		if err != nil {
			st.Status, st.Error, st.Message = "failed", err.Error(), "Failed"
		} else {
			st.Status, st.Message = "done", "Done"
		}
		j.set(st)
		close(j.done)
		// Keep finished jobs a while for late subscribers.
		time.AfterFunc(10*time.Minute, func() {
			js.mu.Lock()
			delete(js.byID, id)
			js.mu.Unlock()
		})
	}()
	return id
}

func (j *job) get() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

func (j *job) set(st jobState) {
	j.mu.Lock()
	j.state = st
	subs := append([]chan jobState(nil), j.subs...)
	j.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- st:
		default:
		}
	}
}

func (j *job) subscribe() (chan jobState, func()) {
	c := make(chan jobState, 16)
	j.mu.Lock()
	j.subs = append(j.subs, c)
	j.mu.Unlock()
	return c, func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		for i, x := range j.subs {
			if x == c {
				j.subs = append(j.subs[:i], j.subs[i+1:]...)
				break
			}
		}
	}
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	s.jobs.mu.Lock()
	j := s.jobs.byID[r.PathValue("id")]
	s.jobs.mu.Unlock()
	if j == nil || (j.owner != a.Username && !a.Admin) {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	sse := startSSE(w)
	if sse == nil {
		return
	}
	updates, unsub := j.subscribe()
	defer unsub()
	send := func(st jobState) bool {
		b, _ := json.Marshal(st)
		sse.send("", string(b))
		return st.Status != "running"
	}
	if send(j.get()) {
		return
	}
	for {
		select {
		case st := <-updates:
			if send(st) {
				return
			}
		case <-j.done:
			send(j.get())
			return
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Second):
			sse.comment()
		}
	}
}

// --- server-sent events -----------------------------------------------------------

type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func startSSE(w http.ResponseWriter) *sseWriter {
	f, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w, f}
}

func (s *sseWriter) send(event, data string) {
	if event != "" {
		fmt.Fprintf(s.w, "event: %s\n", event)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.f.Flush()
}

func (s *sseWriter) comment() {
	fmt.Fprint(s.w, ": keep-alive\n\n")
	s.f.Flush()
}
