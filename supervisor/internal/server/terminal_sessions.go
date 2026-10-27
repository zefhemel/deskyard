package server

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

// terminalSessionsInterval is how often the shared terminal sessions of
// running environments are listed regardless of events. The desktop's tmux
// hooks report opens, ends and attaches on its activity stream (see changed),
// so this is only a safety net, for instance for a program started in a
// session or an event lost while the stream restarted.
const terminalSessionsInterval = 60 * time.Second

// terminalChangeGap is the least time between two refreshes asked for by
// change events, so that a desktop sending many costs a few execs a second.
const terminalChangeGap = 250 * time.Millisecond

// terminalSessions caches each running environment's public shared terminal
// sessions for the environment JSON. Entries belong to one running lifetime of
// the container; a stopped, restarted or removed environment has none.
type terminalSessions struct {
	s       *Server
	mu      sync.Mutex
	seq     uint64
	entries map[string]terminalSessionsEntry
	// changing has an entry while a change refresh runs for that environment;
	// true when another change arrived meanwhile.
	changing map[string]bool
}

type terminalSessionsEntry struct {
	generation string
	seq        uint64 // of the refresh that stored it; older results are dropped
	sessions   []terminal.Session
}

func newTerminalSessions(s *Server) *terminalSessions {
	return &terminalSessions{s: s, entries: map[string]terminalSessionsEntry{}, changing: map[string]bool{}}
}

func terminalCapable(c environments.Environment) bool {
	return c.ImageLabels["deskyard.terminal"] == "tmux-v1"
}

// get returns the cached sessions of the named environment, never nil.
func (t *terminalSessions) get(name string) []terminal.Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]terminal.Session{}, t.entries[name].sessions...)
}

// forEnvironment is get for this running lifetime of c: a restarted
// environment shows no sessions of its previous lifetime.
func (t *terminalSessions) forEnvironment(c environments.Environment) []terminal.Session {
	if c.State != "running" || !terminalCapable(c) {
		return []terminal.Session{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[c.Name]
	if e.generation != terminalGeneration(c) {
		return []terminal.Session{}
	}
	return append([]terminal.Session{}, e.sessions...)
}

// errTerminalHelper is a failure to run the image's helper, as opposed to
// invalid output from it.
var errTerminalHelper = errors.New("terminal helper failed")

// list runs the image's fixed list helper.
func (t *terminalSessions) list(ctx context.Context, c environments.Environment) ([]terminal.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := engine.Run(ctx, t.s.Eng, c.Container.ID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-list"}, User: "user", WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: terminal.MaxListOutput, StderrLimit: 4096})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errTerminalHelper, err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("%w: exit status %d", errTerminalHelper, result.Code)
	}
	return terminal.ParseSessions(result.Stdout)
}

// refresh lists c's sessions now, stores them and publishes the environment
// when its list changed. An environment that isn't running, or whose image
// has no shared terminal, has its entry cleared instead.
func (t *terminalSessions) refresh(ctx context.Context, c environments.Environment) ([]terminal.Session, error) {
	if c.State != "running" || !terminalCapable(c) {
		t.clear(ctx, c.Name)
		return []terminal.Session{}, nil
	}
	t.mu.Lock()
	t.seq++
	seq := t.seq
	t.mu.Unlock()
	sessions, err := t.list(ctx, c)
	if err != nil {
		return nil, err
	}
	t.store(ctx, c.Name, terminalSessionsEntry{generation: terminalGeneration(c), seq: seq, sessions: sessions})
	return sessions, nil
}

// refreshLater refreshes in the background after a change made through this
// supervisor, outliving the request that made it.
func (t *terminalSessions) refreshLater(ctx context.Context, c environments.Environment) {
	go func() { _, _ = t.refresh(context.WithoutCancel(ctx), c) }()
}

// changed refreshes the named environment's sessions after its desktop
// reported a change. Changes reported while a refresh runs fold into one more
// refresh after it.
func (t *terminalSessions) changed(ctx context.Context, name string) {
	if t == nil {
		return // a server that isn't running its background work
	}
	t.mu.Lock()
	if _, busy := t.changing[name]; busy {
		t.changing[name] = true
		t.mu.Unlock()
		return
	}
	t.changing[name] = false
	t.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	go func() {
		for {
			if c, err := t.s.Environments.Get(ctx, name); err == nil {
				if _, err := t.refresh(ctx, c); err != nil {
					t.s.Log.Debug("listing shared terminals", "environment", name, "err", err)
				}
			}
			time.Sleep(terminalChangeGap)
			t.mu.Lock()
			if !t.changing[name] {
				delete(t.changing, name)
				t.mu.Unlock()
				return
			}
			t.changing[name] = false
			t.mu.Unlock()
		}
	}()
}

func (t *terminalSessions) store(ctx context.Context, name string, e terminalSessionsEntry) {
	t.mu.Lock()
	old, had := t.entries[name]
	if had && old.seq > e.seq {
		t.mu.Unlock()
		return
	}
	t.entries[name] = e
	t.mu.Unlock()
	// Publish when what the environment JSON shows changes. It shows no
	// sessions of an earlier lifetime, so after a restart an equal list is
	// still news.
	var shown []terminal.Session
	if had && old.generation == e.generation {
		shown = old.sessions
	}
	if !slices.Equal(shown, e.sessions) {
		t.s.publishEnvironment(ctx, name)
	}
}

func (t *terminalSessions) clear(ctx context.Context, name string) {
	t.mu.Lock()
	old, had := t.entries[name]
	delete(t.entries, name)
	t.mu.Unlock()
	if had && len(old.sessions) > 0 {
		t.s.publishEnvironment(ctx, name)
	}
}

// terminalSessionsParallel bounds the list execs one poll runs at a time.
const terminalSessionsParallel = 4

// run polls every running environment with a shared terminal until ctx ends,
// with up to a second of jitter so it doesn't run in step with other loops.
func (t *terminalSessions) run(ctx context.Context) {
	for {
		t.poll(ctx)
		wait := time.NewTimer(terminalSessionsInterval + rand.N(time.Second))
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
	}
}

// poll refreshes running terminal-capable environments, a few at a time so
// that one slow container doesn't delay the others yet many environments
// don't make as many simultaneous execs, and clears every other entry.
func (t *terminalSessions) poll(ctx context.Context) {
	list, err := t.s.Environments.List(ctx)
	if err != nil {
		return
	}
	live := map[string]bool{}
	slots := make(chan struct{}, terminalSessionsParallel)
	var wg sync.WaitGroup
	for _, c := range list {
		if c.State != "running" || !terminalCapable(c) {
			continue
		}
		live[c.Name] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if _, err := t.refresh(ctx, c); err != nil && ctx.Err() == nil {
				t.s.Log.Debug("listing shared terminals", "environment", c.Name, "err", err)
			}
		}()
	}
	wg.Wait()
	t.mu.Lock()
	var gone []string
	for name := range t.entries {
		if !live[name] {
			gone = append(gone, name)
		}
	}
	t.mu.Unlock()
	for _, name := range gone {
		t.clear(ctx, name)
	}
}

// terminalSessionsEnvironment is the shared precondition of the session
// routes: View permission, like attaching, and a running capable desktop.
func (s *Server) terminalSessionsEnvironment(w http.ResponseWriter, r *http.Request, verb string, session *string) (environments.Environment, bool) {
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return c, false
	}
	if session != nil && !terminal.ValidSession(*session) {
		writeError(w, 400, "invalid terminal session name")
		return c, false
	}
	if c.State != "running" {
		writeError(w, 409, "start the environment before "+verb+" terminals")
		return c, false
	}
	if !terminalCapable(c) {
		writeError(w, 409, "upgrade this desktop image to use shared terminals (requires deskyard.terminal=tmux-v1)")
		return c, false
	}
	return c, true
}

// handleCreateTerminalSession starts the next free terminal-N, detached; the
// caller then attaches to it by name.
func (s *Server) handleCreateTerminalSession(w http.ResponseWriter, r *http.Request) {
	c, ok := s.terminalSessionsEnvironment(w, r, "opening", nil)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := engine.Run(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-create"}, User: "user", WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: 128, StderrLimit: 4096})
	name, found := strings.CutSuffix(string(result.Stdout), "\n")
	if err != nil || result.Code != 0 || !found || !terminal.ValidSession(name) {
		writeError(w, 502, "could not open a shared terminal; upgrade the desktop image if its helper is unavailable")
		return
	}
	s.terminalSessions.refreshLater(r.Context(), c)
	writeJSON(w, 201, map[string]string{"name": name})
}

// handleEndTerminalSession ends a shared session for everyone: its shell, its
// programs and every client, including this supervisor's browser attachments,
// which are told it ended so that they don't reconnect.
func (s *Server) handleEndTerminalSession(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	c, ok := s.terminalSessionsEnvironment(w, r, "ending", &session)
	if !ok {
		return
	}
	// Attachments are marked first: their tmux clients exit as the session
	// ends, and they must report that as ended rather than as a shell exit.
	// A client leaving mid-request doesn't turn an ended session into a failure.
	ending := s.terminals.beginEnding(c.ID, session)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	result, err := engine.Run(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-kill", session}, User: "user", WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: 4096, StderrLimit: 4096})
	switch {
	case err == nil && (result.Code == 0 || result.Code == 3):
		// A session that doesn't exist has no live attachment either.
		ending.finish(true)
		s.terminalSessions.refreshLater(r.Context(), c)
		if result.Code == 3 {
			writeError(w, 404, "no such terminal session")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		ending.finish(false)
		if err == nil {
			err = errors.New(strings.TrimSpace(string(result.Stderr)))
		}
		s.Log.Warn("ending shared terminal", "environment", c.Name, "session", session, "err", err)
		writeError(w, 502, "could not end the shared terminal; upgrade the desktop image if its helper is unavailable")
	}
}
