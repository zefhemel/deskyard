package activity

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tracker keeps, in memory only, who is on each desktop and what they have
// done in the last Window. Recording never blocks: the one mutex is held for
// short in-memory work, callbacks run after it is released, and screen
// subscribers are fed without waiting.
type Tracker struct {
	Now           func() time.Time
	Presence      time.Duration // how long an agent stays after its last action
	ActiveFor     time.Duration // how long after an action it counts as active
	Window        time.Duration // how much history is kept
	BrowserLinger time.Duration // how long browser use lasts after the last touch
	InsideRate    int           // inside events accepted per second per desktop, sustained
	InsideBurst   int           // inside events accepted at once (a backfill of the whole ring)
	MaxEvents     int           // events kept per desktop
	OnPresence    func(Ref)     // the Present list of a desktop may have changed
	OnSummary     func(Ref)     // the Summary of a desktop may have changed

	mu    sync.Mutex
	desks map[string]*desk // by desktop name
}

// NewTracker is a Tracker with the standard limits.
func NewTracker() *Tracker {
	return &Tracker{
		Now:           time.Now,
		Presence:      5 * time.Minute,
		ActiveFor:     10 * time.Second,
		Window:        15 * time.Minute,
		BrowserLinger: 30 * time.Second,
		InsideRate:    50,
		InsideBurst:   5000, // the inside ring's size (cli/app activityMax)
		MaxEvents:     5000,
	}
}

type agentState struct {
	agent        Agent
	lastActive   time.Time
	browserOpen  int
	browserStart time.Time
	browserLast  time.Time
	hasSession   bool
	forwards     map[int]string // open forwards by sequence number, with their text
	forwardSeq   int
	forwardSince time.Time
}

type running struct {
	agent  string
	text   string
	since  time.Time
	inside bool
}

type desk struct {
	ref      Ref
	agents   map[string]*agentState
	running  map[string]running // by agent ID + "/" + command id
	events   []event            // outside agents' history
	inEvents []event            // inside programs' history, kept apart so a flood can't evict the above
	bucket   tokenBucket        // inside events
	watchers map[*watcher]struct{}
	pub      []Presence // last Present list a callback was told about
}

type watcher struct{ ch chan ScreenEvent }

// runKey identifies a running command. NUL can't occur in either part (inside
// ids are cleaned), so two agents' keys never collide.
func runKey(agent, id string) string { return agent + "\x00" + id }

func (t *Tracker) now() time.Time { return t.Now() }

func (t *Tracker) desk(d Ref) *desk {
	if t.desks == nil {
		t.desks = map[string]*desk{}
	}
	ds := t.desks[d.Name]
	if ds == nil {
		ds = &desk{agents: map[string]*agentState{}, running: map[string]running{}, watchers: map[*watcher]struct{}{}}
		t.desks[d.Name] = ds
	}
	ds.ref = d
	return ds
}

func (ds *desk) agentState(a Agent, at time.Time) *agentState {
	s := ds.agents[a.ID]
	if s == nil {
		s = &agentState{}
		ds.agents[a.ID] = s
	}
	s.agent = a
	if at.After(s.lastActive) {
		s.lastActive = at
	}
	return s
}

// record keeps an item; inside and outside agents have separate budgets of
// max events each, so inside input can never evict outside history.
func (ds *desk) record(a Agent, item RecentItem, max int) {
	list := &ds.events
	if a.Inside {
		list = &ds.inEvents
	}
	*list = append(*list, event{a.ID, item})
	if n := len(*list) - max; n > 0 {
		*list = slices.Delete(*list, 0, n)
	}
}

// eventsOf is the history list an agent's items live in.
func (ds *desk) eventsOf(s *agentState) []event {
	if s.agent.Inside {
		return ds.inEvents
	}
	return ds.events
}

// mutate runs f under the lock, then publishes any change to presence and
// summaries after unlocking.
func (t *Tracker) mutate(d Ref, f func(ds *desk, now time.Time)) {
	t.mu.Lock()
	ds := t.desk(d)
	now := t.now()
	f(ds, now)
	t.settle(ds, now)
	fire := t.diff(ds, now, true)
	t.mu.Unlock()
	fire()
}

// diff compares the desktop's presence with what callbacks were last told and
// returns the callbacks to run once the lock is released.
func (t *Tracker) diff(ds *desk, now time.Time, summaryChanged bool) func() {
	cur := t.present(ds, now)
	presChanged := !samePresence(cur, ds.pub)
	ds.pub = cur
	ref, onP, onS := ds.ref, t.OnPresence, t.OnSummary
	return func() {
		if presChanged && onP != nil {
			onP(ref)
		}
		if (presChanged || summaryChanged) && onS != nil {
			onS(ref)
		}
	}
}

// samePresence ignores LastActive: it moves on every action.
func samePresence(a, b []Presence) bool {
	return slices.EqualFunc(a, b, func(x, y Presence) bool {
		x.LastActive, y.LastActive = time.Time{}, time.Time{}
		return x == y
	})
}

// browserUsing: a connection is open, or the last use was a moment ago.
func (t *Tracker) browserUsing(s *agentState, now time.Time) bool {
	return s.browserOpen > 0 || (s.hasSession && now.Sub(s.browserLast) < t.BrowserLinger)
}

// settle ends browser sessions that have lapsed, drops inside commands that
// never ended, and forgets agents that have left. It reports whether
// anything changed.
func (t *Tracker) settle(ds *desk, now time.Time) bool {
	changed := false
	for _, s := range ds.agents {
		if s.hasSession && !t.browserUsing(s, now) {
			s.hasSession = false
			ds.record(s.agent, RecentItem{Kind: "browser", T: s.browserLast, DurationMs: s.browserLast.Sub(s.browserStart).Milliseconds()}, t.MaxEvents)
			changed = true
		}
	}
	for k, r := range ds.running {
		if r.inside && now.Sub(r.since) > t.Window {
			delete(ds.running, k)
			changed = true
		}
	}
	cutoff := now.Add(-t.Window)
	if i := slices.IndexFunc(ds.events, func(e event) bool { return e.item.T.Before(cutoff) }); i >= 0 {
		n := len(ds.events)
		ds.events = slices.DeleteFunc(ds.events, func(e event) bool { return e.item.T.Before(cutoff) })
		changed = changed || len(ds.events) != n
	}
	if slices.ContainsFunc(ds.inEvents, func(e event) bool { return e.item.T.Before(cutoff) }) {
		ds.inEvents = slices.DeleteFunc(ds.inEvents, func(e event) bool { return e.item.T.Before(cutoff) })
		changed = true
	}
	busy := map[string]bool{} // agents with a command running
	for _, r := range ds.running {
		busy[r.agent] = true
	}
	for id, s := range ds.agents {
		if !t.present1(s, busy[id], now) {
			delete(ds.agents, id)
			if s.agent.Inside {
				ds.inEvents = slices.DeleteFunc(ds.inEvents, func(e event) bool { return e.agent == id })
			} else {
				ds.events = slices.DeleteFunc(ds.events, func(e event) bool { return e.agent == id })
			}
			changed = true
		}
	}
	return changed
}

func (t *Tracker) present1(s *agentState, busy bool, now time.Time) bool {
	return busy || len(s.forwards) > 0 || t.browserUsing(s, now) || now.Sub(s.lastActive) <= t.Presence
}

func (t *Tracker) present(ds *desk, now time.Time) []Presence {
	out := []Presence{}
	for _, s := range ds.agents {
		out = append(out, Presence{
			ID: s.agent.ID, Name: s.agent.Name, Owner: s.agent.Owner, Inside: s.agent.Inside,
			Color: Color(s.agent.ID), Active: now.Sub(s.lastActive) < t.ActiveFor, LastActive: s.lastActive,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// CmdStart records that a command began running.
func (t *Tracker) CmdStart(d Ref, a Agent, id, display string) {
	t.mutate(d, func(ds *desk, now time.Time) {
		ds.agentState(a, now)
		ds.running[runKey(a.ID, id)] = running{agent: a.ID, text: display, since: now}
	})
}

// CmdEnd records that a command ended; a nil exit means the connection was
// lost. An unknown id is ignored.
func (t *Tracker) CmdEnd(d Ref, a Agent, id string, exit *int) {
	t.mutate(d, func(ds *desk, now time.Time) { t.cmdEnd(ds, a, id, exit, now) })
}

func (t *Tracker) cmdEnd(ds *desk, a Agent, id string, exit *int, at time.Time) {
	key := runKey(a.ID, id)
	r, ok := ds.running[key]
	if !ok {
		return
	}
	delete(ds.running, key)
	ds.agentState(a, at)
	item := RecentItem{Kind: "command", T: at, Text: r.text, DurationMs: max(at.Sub(r.since), 0).Milliseconds()}
	if exit != nil {
		x := *exit
		item.Exit = &x
	} else {
		item.Disconnected = true
	}
	ds.record(a, item, t.MaxEvents)
}

// Screen records a screen action from the outside.
func (t *Tracker) Screen(d Ref, a Agent, s ScreenAction) {
	t.mutate(d, func(ds *desk, now time.Time) { t.screen(ds, a, s, now) })
}

// liveFor is how recent an action must be to be drawn on the overlay.
const liveFor = 5 * time.Second

// overlayActions are the screen actions drawn on the viewer's page.
var overlayActions = []string{"click", "double-click", "triple-click", "drag", "scroll", "key", "hold-key", "type"}

func (t *Tracker) screen(ds *desk, a Agent, s ScreenAction, at time.Time) {
	ds.agentState(a, at)
	ds.record(a, RecentItem{Kind: "screen", T: at, Action: s.Action, Count: 1}, t.MaxEvents)
	// An old (backdated) inside event must not be drawn as if it were live.
	if len(ds.watchers) == 0 || !slices.Contains(overlayActions, s.Action) || t.now().Sub(at) > liveFor {
		return
	}
	ev := ScreenEvent{
		Agent:        ScreenAgent{ID: a.ID, Name: a.Name, Color: Color(a.ID), Inside: a.Inside},
		T:            at,
		ScreenAction: s,
	}
	for w := range ds.watchers {
		select {
		case w.ch <- ev:
		default: // a slow subscriber loses events; it never slows the agent
		}
	}
}

// Copy records a file copied into (direction "in") or out of the desktop.
// The path is shown like a command: without control characters, and cut.
func (t *Tracker) Copy(d Ref, a Agent, direction, path string, size int64) {
	path = DisplayPath(path)
	t.mutate(d, func(ds *desk, now time.Time) {
		ds.agentState(a, now)
		ds.record(a, RecentItem{Kind: "copy", T: now, Direction: direction, Path: path, Size: size}, t.MaxEvents)
	})
}

func (s *agentState) browserUse(now time.Time) {
	if !s.hasSession {
		s.hasSession, s.browserStart = true, now
	}
	s.browserLast = now
}

// BrowserOpen records an open browser (CDP) connection; call done when it
// closes.
func (t *Tracker) BrowserOpen(d Ref, a Agent) (done func()) {
	var s *agentState
	t.mutate(d, func(ds *desk, now time.Time) {
		s = ds.agentState(a, now)
		s.browserOpen++
		s.browserUse(now)
	})
	return t.once(d, a.ID, s, func(s *agentState, now time.Time) {
		s.browserOpen--
		s.browserUse(now)
	})
}

// BrowserTouch records a short use of the browser, such as one HTTP call.
func (t *Tracker) BrowserTouch(d Ref, a Agent) {
	t.mutate(d, func(ds *desk, now time.Time) { ds.agentState(a, now).browserUse(now) })
}

// ForwardOpen records an open port forward, described by text (for example
// "3000 in · 5173 out"); call done when it closes.
func (t *Tracker) ForwardOpen(d Ref, a Agent, text string) (done func()) {
	var s *agentState
	var seq int
	t.mutate(d, func(ds *desk, now time.Time) {
		s = ds.agentState(a, now)
		if len(s.forwards) == 0 {
			s.forwardSince = now
		}
		if s.forwards == nil {
			s.forwards = map[int]string{}
		}
		s.forwardSeq++
		seq = s.forwardSeq
		s.forwards[seq] = text
	})
	return t.once(d, a.ID, s, func(s *agentState, now time.Time) { delete(s.forwards, seq) })
}

// once makes a done func that undoes an open exactly once, and not at all if
// the agent has been forgotten in the meantime.
func (t *Tracker) once(d Ref, id string, s *agentState, f func(*agentState, time.Time)) func() {
	var mu sync.Mutex
	called := false
	return func() {
		mu.Lock()
		first := !called
		called = true
		mu.Unlock()
		if !first {
			return
		}
		t.mutate(d, func(ds *desk, now time.Time) {
			if ds.agents[id] != s {
				return
			}
			f(s, now)
			if now.After(s.lastActive) {
				s.lastActive = now
			}
		})
	}
}

// Inside records an event from a program running inside the desktop. The
// event is untrusted: it is sanitised, its time is clamped, and it is rate
// limited: a burst of InsideBurst (a backfill of the whole ring), then
// InsideRate a second. It only ever touches that program's own agent. It
// returns false when the event was dropped.
func (t *Tracker) Inside(d Ref, e Event) bool {
	// The rate comes first, so a dropped event costs next to nothing.
	if !t.takeInside(d) {
		return false
	}
	e, ok := Sanitize(e)
	if !ok {
		return false
	}
	a := InsideAgent(e.Agent)
	accepted := false
	t.mutate(d, func(ds *desk, now time.Time) {
		if e.T.After(now) {
			e.T = now
		}
		if e.T.Before(now.Add(-t.Window)) {
			return
		}
		if _, known := ds.agents[a.ID]; !known && ds.insideAgents() >= maxInsideAgents {
			return
		}
		accepted = true
		switch e.Kind {
		case "screen":
			t.screen(ds, a, *e.Screen, e.T)
		case "cmd-start":
			ds.agentState(a, e.T)
			ds.capInsideRunning()
			ds.running[runKey(a.ID, e.ID)] = running{agent: a.ID, text: e.Cmd, since: e.T, inside: true}
		case "cmd-end":
			t.cmdEnd(ds, a, e.ID, e.Exit, e.T)
		}
	})
	return accepted
}

// takeInside takes one inside event from the desktop's bucket. The bucket
// runs on our own clock: an event's time is the sender's claim, and
// backdating must not buy extra events.
func (t *Tracker) takeInside(d Ref) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.desk(d).bucket.take(t.now(), float64(t.InsideRate), float64(t.InsideBurst))
}

// tokenBucket holds up to burst tokens and gains rate a second; it starts
// full.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (b *tokenBucket) fill(now time.Time, rate, burst float64) {
	if b.last.IsZero() {
		b.tokens, b.last = burst, now
	} else if now.After(b.last) {
		b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
		b.last = now
	}
}

func (b *tokenBucket) take(now time.Time, rate, burst float64) bool {
	b.fill(now, rate, burst)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// full reports whether forgetting the bucket would give nothing away.
func (b *tokenBucket) full(now time.Time, rate, burst float64) bool {
	b.fill(now, rate, burst)
	return b.tokens >= burst
}

// Bounds on what programs inside a desktop can make the tracker hold.
const (
	maxInsideAgents  = 32
	maxInsideRunning = 256
)

func (ds *desk) insideAgents() int {
	n := 0
	for _, s := range ds.agents {
		if s.agent.Inside {
			n++
		}
	}
	return n
}

// capInsideRunning makes room for one more inside command by dropping the
// oldest ones.
func (ds *desk) capInsideRunning() {
	var keys []string
	for k, r := range ds.running {
		if r.inside {
			keys = append(keys, k)
		}
	}
	if len(keys) < maxInsideRunning {
		return
	}
	slices.SortFunc(keys, func(x, y string) int { return ds.running[x].since.Compare(ds.running[y].since) })
	for _, k := range keys[:len(keys)-maxInsideRunning+1] {
		delete(ds.running, k)
	}
}

// ResetInside drops the commands running inside the desktop, for when the
// stream restarts and their ends will never arrive.
func (t *Tracker) ResetInside(d Ref) {
	t.mutate(d, func(ds *desk, now time.Time) {
		for k, r := range ds.running {
			if r.inside {
				delete(ds.running, k)
			}
		}
	})
}

// Forget removes an agent from every desktop.
func (t *Tracker) Forget(agentID string) {
	t.mu.Lock()
	var fires []func()
	for _, ds := range t.desks {
		if _, ok := ds.agents[agentID]; !ok {
			continue
		}
		delete(ds.agents, agentID)
		for k, r := range ds.running {
			if r.agent == agentID {
				delete(ds.running, k)
			}
		}
		ds.events = slices.DeleteFunc(ds.events, func(e event) bool { return e.agent == agentID })
		ds.inEvents = slices.DeleteFunc(ds.inEvents, func(e event) bool { return e.agent == agentID })
		fires = append(fires, t.diff(ds, t.now(), true))
	}
	t.mu.Unlock()
	for _, f := range fires {
		f()
	}
}

// ForgetDesktop drops everything known about a desktop and closes its screen
// subscriptions.
func (t *Tracker) ForgetDesktop(name string) {
	t.mu.Lock()
	ds := t.desks[name]
	delete(t.desks, name)
	var fire func()
	if ds != nil {
		for w := range ds.watchers {
			close(w.ch)
		}
		had := len(ds.pub) > 0
		ds.agents, ds.running, ds.events, ds.inEvents, ds.pub = nil, nil, nil, nil, nil
		ref, onP, onS := ds.ref, t.OnPresence, t.OnSummary
		fire = func() {
			if had && onP != nil {
				onP(ref)
			}
			if had && onS != nil {
				onS(ref)
			}
		}
	}
	t.mu.Unlock()
	if fire != nil {
		fire()
	}
}

// Tick lets time pass: presence expires, browser sessions end, history ages
// out. Call it about once a second.
func (t *Tracker) Tick() {
	t.mu.Lock()
	now := t.now()
	var fires []func()
	for name, ds := range t.desks {
		changed := t.settle(ds, now)
		fires = append(fires, t.diff(ds, now, changed))
		if len(ds.agents) == 0 && len(ds.running) == 0 && len(ds.events) == 0 && len(ds.inEvents) == 0 && len(ds.watchers) == 0 &&
			ds.bucket.full(now, float64(t.InsideRate), float64(t.InsideBurst)) {
			delete(t.desks, name)
		}
	}
	t.mu.Unlock()
	for _, f := range fires {
		f()
	}
}

// Present lists the agents on a desktop, sorted by name then id. Never nil.
func (t *Tracker) Present(name string) []Presence {
	t.mu.Lock()
	defer t.mu.Unlock()
	ds := t.desks[name]
	if ds == nil {
		return []Presence{}
	}
	return t.present(ds, t.now())
}

// Summary describes each agent on a desktop, in the same order as Present.
func (t *Tracker) Summary(name string) []Summary {
	t.mu.Lock()
	defer t.mu.Unlock()
	ds := t.desks[name]
	if ds == nil {
		return []Summary{}
	}
	now := t.now()
	cutoff := now.Add(-t.Window)
	out := []Summary{}
	for _, p := range t.present(ds, now) {
		s := ds.agents[p.ID]
		sum := Summary{Presence: p, Now: []NowItem{}, Recent: recentFor(ds.eventsOf(s), p.ID, cutoff)}
		for _, r := range ds.running {
			if r.agent == p.ID {
				sum.Now = append(sum.Now, NowItem{Kind: "command", Text: r.text, Since: r.since})
			}
		}
		sort.Slice(sum.Now, func(i, j int) bool {
			if !sum.Now[i].Since.Equal(sum.Now[j].Since) {
				return sum.Now[i].Since.Before(sum.Now[j].Since)
			}
			return sum.Now[i].Text < sum.Now[j].Text
		})
		if t.browserUsing(s, now) {
			sum.Now = append(sum.Now, NowItem{Kind: "browser", Since: s.browserStart})
		}
		if len(s.forwards) > 0 {
			seqs := make([]int, 0, len(s.forwards))
			for q := range s.forwards {
				seqs = append(seqs, q)
			}
			sort.Ints(seqs)
			texts := make([]string, len(seqs))
			for i, q := range seqs {
				texts[i] = s.forwards[q]
			}
			sum.Now = append(sum.Now, NowItem{Kind: "forward", Text: strings.Join(texts, ", "), Since: s.forwardSince})
		}
		out = append(out, sum)
	}
	return out
}

// ScreenEvent is a screen action as shown on the viewer's page.
type ScreenEvent struct {
	Agent ScreenAgent `json:"agent"`
	T     time.Time   `json:"t"`
	ScreenAction
}

// ScreenAgent is who made a screen action.
type ScreenAgent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Inside bool   `json:"inside"`
}

// WatchScreen subscribes to the drawable screen actions on a desktop. Call
// the returned func to stop; it closes the channel. A subscriber that doesn't
// read loses events rather than blocking anyone.
func (t *Tracker) WatchScreen(name string) (<-chan ScreenEvent, func()) {
	w := &watcher{ch: make(chan ScreenEvent, 64)}
	t.mu.Lock()
	if t.desks == nil {
		t.desks = map[string]*desk{}
	}
	ds := t.desks[name]
	if ds == nil {
		ds = &desk{ref: Ref{Name: name}, agents: map[string]*agentState{}, running: map[string]running{}, watchers: map[*watcher]struct{}{}}
		t.desks[name] = ds
	}
	ds.watchers[w] = struct{}{}
	t.mu.Unlock()
	return w.ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if ds2 := t.desks[name]; ds2 == ds {
			if _, ok := ds.watchers[w]; ok {
				delete(ds.watchers, w)
				close(w.ch)
			}
		} else if _, ok := ds.watchers[w]; ok { // desktop was forgotten; ForgetDesktop closed it
			delete(ds.watchers, w)
		}
	}
}
