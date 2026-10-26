package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// A job is a long operation (create, upgrade, image pull) whose
// progress the UI follows over server-sent events at /api/jobs/{id}.
type jobState struct {
	Result      any      `json:"result,omitempty"`
	Status      string   `json:"status"` // running, done, failed
	Message     string   `json:"message"`
	Progress    *float64 `json:"progress"`
	Error       string   `json:"error,omitempty"`
	Environment string   `json:"environment,omitempty"`
}

// maxJobHistory is how many updates a job keeps for its followers. A verbose job
// sends every log line as an update; a follower that connects late or falls
// behind sees the last maxJobHistory of them.
const maxJobHistory = 2000

type job struct {
	owner       string
	environment string
	cancellable bool
	cancel      context.CancelFunc
	done        chan struct{}

	mu        sync.Mutex
	state     jobState
	history   []jobState // the last maxJobHistory states, oldest first
	first     int        // sequence number of history[0]
	changed   chan struct{}
	cancelled bool
}

type jobs struct {
	mu   sync.Mutex
	byID map[string]*job
}

func newJobs() *jobs { return &jobs{byID: map[string]*job{}} }

// jobFunc is a job's work. progress reports a message with a fraction, or
// -1 for none. ctx ends when a cancellable job is cancelled.
type jobFunc func(ctx context.Context, progress func(string, float64)) error

// start runs fn in the background as a job owned by an account.
func (js *jobs) start(owner, environment string, fn jobFunc) string {
	return js.run(owner, environment, false, fn)
}

// startCancellable is start for jobs that may be stopped half-way:
// POST /api/jobs/{id}/cancel cancels fn's ctx.
func (js *jobs) startCancellable(owner string, fn jobFunc) string {
	return js.run(owner, "", true, fn)
}

func (js *jobs) startResult(owner string, cancellable bool, fn func(context.Context, func(string, float64)) (any, error)) string {
	return js.runResult(owner, "", cancellable, fn)
}
func (js *jobs) run(owner, environment string, cancellable bool, fn jobFunc) string {
	return js.runResult(owner, environment, cancellable, func(ctx context.Context, p func(string, float64)) (any, error) { return nil, fn(ctx, p) })
}
func (js *jobs) runResult(owner, environment string, cancellable bool, fn func(context.Context, func(string, float64)) (any, error)) string {
	return js.runCommittedResult(owner, environment, cancellable, func(ctx context.Context, p func(string, float64), _ func(func() error) error) (any, error) {
		return fn(ctx, p)
	})
}

// startCommittedResult is cancellable through admission, then commits under the
// same mutex used by stop. Once save succeeds, cancellation truthfully conflicts.
func (js *jobs) startCommittedResult(owner string, fn func(context.Context, func(string, float64), func(func() error) error) (any, error)) string {
	return js.runCommittedResult(owner, "", true, fn)
}
func (js *jobs) runCommittedResult(owner, environment string, cancellable bool, fn func(context.Context, func(string, float64), func(func() error) error) (any, error)) string {
	id := randomID(8)
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{owner: owner, environment: environment, cancellable: cancellable, cancel: cancel,
		done: make(chan struct{}), changed: make(chan struct{})}
	j.set(jobState{Status: "running", Message: "Starting", Environment: environment})
	js.mu.Lock()
	js.byID[id] = j
	js.mu.Unlock()
	go func() {
		result, err := fn(ctx, func(msg string, frac float64) {
			st := j.get()
			st.Message = msg
			st.Progress = nil
			if frac >= 0 {
				st.Progress = &frac
			}
			j.set(st)
		}, func(save func() error) error { return j.commit(ctx, save) })
		cancel()
		st := j.get()
		st.Progress = nil
		st.Result = result
		switch {
		case err != nil && j.wasCancelled():
			st.Status, st.Error, st.Message = "failed", "cancelled", "Cancelled"
		case err != nil:
			st.Status, st.Error, st.Message = "failed", err.Error(), "Failed"
		default:
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

// forEnvironment returns the running job for an environment, if there is one.
func (js *jobs) forEnvironment(name string) (string, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	for id, j := range js.byID {
		if j.environment == name && j.get().Status == "running" {
			return id, true
		}
	}
	return "", false
}

func (js *jobs) get(id string) *job {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.byID[id]
}

func (j *job) get() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

func (j *job) set(st jobState) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state = st
	j.history = append(j.history, st)
	if n := len(j.history) - maxJobHistory; n > 0 {
		j.history = append(j.history[:0:0], j.history[n:]...)
		j.first += n
	}
	close(j.changed)
	j.changed = make(chan struct{})
}

// since returns the states from sequence number seq on (or from the oldest
// kept), the next sequence number, and a channel closed on the next update.
func (j *job) since(seq int) ([]jobState, int, <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < j.first {
		seq = j.first
	}
	out := slices.Clone(j.history[seq-j.first:])
	return out, j.first + len(j.history), j.changed
}

func (j *job) wasCancelled() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cancelled
}

func (j *job) commit(ctx context.Context, save func() error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancelled {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := save(); err != nil {
		return err
	}
	j.cancellable = false
	return nil
}

// stop cancels a running cancellable job; it reports false for one that
// can't be cancelled or has finished.
func (j *job) stop() bool {
	j.mu.Lock()
	ok := j.cancellable && j.state.Status == "running"
	if ok {
		j.cancelled = true
	}
	j.mu.Unlock()
	if ok {
		j.cancel()
	}
	return ok
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	j := s.jobs.get(r.PathValue("id"))
	if j == nil || (j.owner != a.Username && !a.Admin && !s.canSeeEnvironment(r, a, j.environment)) {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	sse := startSSE(w)
	if sse == nil {
		return
	}
	// Every update in order, from the oldest kept: a build's log lines each
	// come as one.
	seq := 0
	for {
		states, next, changed := j.since(seq)
		seq = next
		for _, st := range states {
			b, _ := json.Marshal(st)
			sse.send("", string(b))
			if st.Status != "running" {
				return
			}
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Second):
			sse.comment()
		}
	}
}

// handleCancelJob stops a cancellable job for its owner or an admin.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	j := s.jobs.get(r.PathValue("id"))
	if j == nil || (j.owner != a.Username && !a.Admin) {
		writeError(w, http.StatusNotFound, "no such job")
		return
	}
	if !j.stop() {
		writeError(w, http.StatusConflict, "this job can't be cancelled, or has finished")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// canSeeEnvironment: whoever can see an environment can follow its jobs.
func (s *Server) canSeeEnvironment(r *http.Request, a accounts.Account, name string) bool {
	if name == "" {
		return false
	}
	c, err := s.Environments.Get(r.Context(), name)
	return err == nil && auth.Can(a, auth.View, c.ID)
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
