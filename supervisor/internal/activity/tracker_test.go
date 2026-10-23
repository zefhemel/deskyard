package activity

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

var (
	dk    = Ref{"alice", "id-1"}
	bot   = Agent{ID: "tok1", Name: "deploy-bot", Owner: "zef"}
	click = ScreenAction{Action: "click", Pos: []int{10, 20}}
)

type rig struct {
	*Tracker
	now      time.Time
	pres, sm int
}

func newRig() *rig {
	r := &rig{Tracker: NewTracker(), now: t0}
	r.Now = func() time.Time { return r.now }
	r.OnPresence = func(Ref) { r.pres++ }
	r.OnSummary = func(Ref) { r.sm++ }
	return r
}

func (r *rig) adv(d time.Duration) { r.now = r.now.Add(d) }

// watched reports whether anyone is subscribed to alice's screen events.
func (r *rig) watched() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.desks["alice"]
	return ds != nil && len(ds.watchers) > 0
}

func (r *rig) recent() []RecentItem { return r.Summary("alice")[0].Recent }
func (r *rig) nowItems() []NowItem  { return r.Summary("alice")[0].Now }

func TestPresenceFollowsActivity(t *testing.T) {
	r := newRig()
	if p := r.Present("alice"); p == nil || len(p) != 0 {
		t.Fatalf("empty should be non-nil empty: %#v", p)
	}
	r.Screen(dk, bot, click)
	p := r.Present("alice")
	if len(p) != 1 || p[0].ID != "tok1" || p[0].Owner != "zef" || p[0].Color != Color("tok1") || !p[0].Active || p[0].Inside {
		t.Fatalf("%+v", p)
	}
	if r.pres != 1 {
		t.Fatalf("first action must notify presence at once, got %d", r.pres)
	}
	r.adv(11 * time.Second)
	r.Tick()
	p = r.Present("alice")
	if len(p) != 1 || p[0].Active || r.pres != 2 {
		t.Fatalf("%+v pres=%d", p, r.pres)
	}
	r.adv(5 * time.Minute)
	r.Tick()
	if len(r.Present("alice")) != 0 || r.pres != 3 {
		t.Fatalf("pres=%d", r.pres)
	}
	if s := r.Summary("alice"); s == nil || len(s) != 0 {
		t.Fatal("summary must be non-nil")
	}
}

func TestRunningCommandKeepsPresent(t *testing.T) {
	r := newRig()
	r.CmdStart(dk, bot, "c1", "npm test")
	r.adv(20 * time.Minute)
	r.Tick()
	if len(r.Present("alice")) != 1 {
		t.Fatal("gone")
	}
	want := []NowItem{{Kind: "command", Text: "npm test", Since: t0}}
	if got := r.nowItems(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestCommandEnds(t *testing.T) {
	r := newRig()
	r.CmdStart(dk, bot, "c1", "npm test")
	r.adv(2 * time.Second)
	three := 3
	r.CmdEnd(dk, bot, "nope", &three)
	if len(r.recent()) != 0 {
		t.Fatal("unknown id recorded")
	}
	r.CmdEnd(dk, bot, "c1", &three)
	got := r.recent()
	if len(got) != 1 || got[0].Kind != "command" || got[0].Text != "npm test" || got[0].Exit == nil || *got[0].Exit != 3 || got[0].DurationMs != 2000 || got[0].Disconnected {
		t.Fatalf("%+v", got)
	}
	if len(r.nowItems()) != 0 {
		t.Fatal("still running")
	}
	r.CmdStart(dk, bot, "c2", "sleep 9")
	r.CmdEnd(dk, bot, "c2", nil)
	got = r.recent()
	if len(got) != 2 || !got[0].Disconnected || got[0].Exit != nil {
		t.Fatalf("%+v", got)
	}
}

func TestScreenMerge(t *testing.T) {
	r := newRig()
	for range 14 {
		r.Screen(dk, bot, click)
		r.adv(time.Second)
	}
	got := r.recent()
	if len(got) != 1 || got[0].Kind != "screen" || got[0].Action != "click" || got[0].Count != 14 {
		t.Fatalf("%+v", got)
	}
	if want := t0.Add(13 * time.Second); !got[0].T.Equal(want) {
		t.Fatalf("T=%v want newest %v", got[0].T, want)
	}
	r.Screen(dk, bot, ScreenAction{Action: "type", Chars: 4})
	r.Screen(dk, bot, click)
	got = r.recent()
	if len(got) != 3 || got[0].Action != "click" || got[1].Action != "type" || got[2].Count != 14 {
		t.Fatalf("%+v", got)
	}
	r2 := newRig()
	for range 3 {
		r2.Screen(dk, bot, ScreenAction{Action: "screenshot"})
	}
	if got := r2.recent(); len(got) != 1 || got[0].Action != "screenshot" || got[0].Count != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestRecentLimitAndWindow(t *testing.T) {
	r := newRig()
	r.CmdStart(dk, bot, "old", "old one")
	r.CmdEnd(dk, bot, "old", new(int))
	r.adv(14 * time.Minute)
	for i := range 25 {
		id := fmt.Sprint("c", i)
		r.CmdStart(dk, bot, id, "cmd "+id)
		r.CmdEnd(dk, bot, id, new(int))
		r.adv(time.Second)
	}
	got := r.recent()
	if len(got) != 20 || got[0].Text != "cmd c24" {
		t.Fatalf("%d %+v", len(got), got[0])
	}
	r.adv(time.Minute)
	r.Tick()
	for _, it := range r.recent() {
		if it.Text == "old one" {
			t.Fatal("old item kept")
		}
	}
	if len(r.events(t)) > 25 {
		t.Fatal("not pruned")
	}
}

func (r *rig) events(t *testing.T) []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.desks["alice"]
	return append(append([]event{}, ds.events...), ds.inEvents...)
}

func TestCopyPathIsCleaned(t *testing.T) {
	r := newRig()
	long := "/workspace/" + strings.Repeat("d/", 100) + "report.pdf"
	r.Copy(dk, bot, "out", "/x\x1b[2J\u202e/y\nz", 1)
	r.Copy(dk, bot, "out", long, 1)
	got := r.recent()
	if got[1].Path != "/x [2J/y z" {
		t.Errorf("control chars: %q", got[1].Path)
	}
	if p := got[0].Path; utf8.RuneCountInString(p) != 121 || !strings.HasPrefix(p, "…") || !strings.HasSuffix(p, "/report.pdf") {
		t.Errorf("long path: %q", p)
	}
}

func TestCopy(t *testing.T) {
	r := newRig()
	r.Copy(dk, bot, "in", "/workspace/a.pdf", 1234)
	got := r.recent()
	want := RecentItem{Kind: "copy", T: t0, Direction: "in", Path: "/workspace/a.pdf", Size: 1234}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%+v", got)
	}
}

func hasKind(n []NowItem, k string) bool {
	for _, i := range n {
		if i.Kind == k {
			return true
		}
	}
	return false
}

func TestBrowserSession(t *testing.T) {
	r := newRig()
	done := r.BrowserOpen(dk, bot)
	if n := r.nowItems(); len(n) != 1 || n[0].Kind != "browser" || !n[0].Since.Equal(t0) {
		t.Fatalf("%+v", n)
	}
	r.adv(4 * time.Minute)
	done()
	done() // twice is harmless
	r.adv(29 * time.Second)
	r.Tick()
	if !hasKind(r.nowItems(), "browser") {
		t.Fatal("lingering use gone too early")
	}
	r.adv(2 * time.Second)
	r.Tick()
	if hasKind(r.nowItems(), "browser") {
		t.Fatal("still using")
	}
	got := r.recent()
	if len(got) != 1 || got[0].Kind != "browser" || got[0].DurationMs != 240000 {
		t.Fatalf("%+v", got)
	}
	// 4m31s so far; present until 9m after open (5m after last use at 4m).
	r.adv(4*time.Minute + 28*time.Second) // 8m59s
	r.Tick()
	if len(r.Present("alice")) != 1 {
		t.Fatal("left early")
	}
	r.adv(2 * time.Second) // 9m01s
	r.Tick()
	if len(r.Present("alice")) != 0 {
		t.Fatal("still present")
	}
}

func TestBrowserTouch(t *testing.T) {
	r := newRig()
	r.BrowserTouch(dk, bot)
	if !hasKind(r.nowItems(), "browser") {
		t.Fatal("not using")
	}
	r.adv(20 * time.Second)
	r.BrowserTouch(dk, bot)
	r.adv(31 * time.Second)
	r.Tick()
	got := r.recent()
	if hasKind(r.nowItems(), "browser") || len(got) != 1 || got[0].Kind != "browser" || got[0].DurationMs != 20000 {
		t.Fatalf("%+v", got)
	}
}

func TestForwardKeepsPresent(t *testing.T) {
	r := newRig()
	done := r.ForwardOpen(dk, bot, "3000 in · 5173 out")
	r.adv(20 * time.Minute)
	r.Tick()
	if len(r.Present("alice")) != 1 || !hasKind(r.nowItems(), "forward") {
		t.Fatal("gone")
	}
	if n := r.nowItems(); n[0].Text != "3000 in · 5173 out" || !n[0].Since.Equal(t0) {
		t.Fatalf("%+v", n)
	}
	done()
	r.adv(5*time.Minute + time.Second)
	r.Tick()
	if len(r.Present("alice")) != 0 {
		t.Fatal("still present")
	}
}

func TestInsideAgents(t *testing.T) {
	r := newRig()
	ck := click
	if !r.Inside(dk, Event{Agent: "a terminal", Kind: "screen", T: t0, Screen: &ck}) {
		t.Fatal("dropped")
	}
	p := r.Present("alice")
	if len(p) != 1 || p[0].ID != "inside:a terminal" || p[0].Name != "a terminal" || !p[0].Inside || p[0].Owner != "" {
		t.Fatalf("%+v", p)
	}
	r.CmdStart(dk, bot, "c1", "outside")
	r.Inside(dk, Event{Agent: "a terminal", Kind: "cmd-end", ID: "c1", T: t0})
	if n := r.Summary("alice"); len(n) != 2 || !hasKind(n[1].Now, "command") && !hasKind(n[0].Now, "command") {
		t.Fatalf("%+v", n)
	}
	r.Inside(dk, Event{Agent: "a terminal", Kind: "cmd-start", ID: "x", Cmd: "make", T: t0})
	r.adv(16 * time.Minute)
	r.Tick()
	for _, s := range r.Summary("alice") {
		if s.Inside && len(s.Now) != 0 {
			t.Fatalf("stale inside command kept: %+v", s.Now)
		}
	}
	if len(r.Summary("alice")) != 1 || r.Summary("alice")[0].ID != "tok1" || len(r.Summary("alice")[0].Now) != 1 {
		t.Fatalf("outside command should be untouched: %+v", r.Summary("alice"))
	}
	// time clamping
	r.Inside(dk, Event{Agent: "p", Kind: "screen", T: r.now.Add(time.Hour), Screen: &ck})
	if !r.recentOf("inside:p")[0].T.Equal(r.now) {
		t.Fatalf("future not clamped")
	}
	if r.Inside(dk, Event{Agent: "q", Kind: "screen", T: r.now.Add(-20 * time.Minute), Screen: &ck}) {
		t.Fatal("old accepted")
	}
	if r.Inside(dk, Event{Agent: "q", Kind: "screen", Screen: &ScreenAction{Action: "bogus"}}) {
		t.Fatal("garbage accepted")
	}
}

func (r *rig) recentOf(id string) []RecentItem {
	for _, s := range r.Summary("alice") {
		if s.ID == id {
			return s.Recent
		}
	}
	return nil
}

func TestInsideFloodIsCapped(t *testing.T) {
	r := newRig()
	r.Screen(dk, bot, click)
	before := r.Summary("alice")[0]
	ck := click
	n := 0
	for range 6000 { // all at one instant: the burst, no more
		if r.Inside(dk, Event{Agent: "a terminal", Kind: "screen", T: t0, Screen: &ck}) {
			n++
		}
	}
	if n != 5000 {
		t.Fatalf("burst accepted %d", n)
	}
	var after Summary
	for _, s := range r.Summary("alice") {
		if s.ID == "tok1" {
			after = s
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("outside agent changed:\n%+v\n%+v", before, after)
	}
	n = 0
	for range 100 { // then a sustained flood: 200 a second for 10 seconds
		r.adv(100 * time.Millisecond)
		for range 20 {
			if r.Inside(dk, Event{Agent: "a terminal", Kind: "screen", T: r.now, Screen: &ck}) {
				n++
			}
		}
	}
	if n < 495 || n > 505 {
		t.Fatalf("sustained flood accepted %d in 10 s", n)
	}
	r2 := newRig()
	r2.Screen(dk, bot, click)
	r2.ForwardOpen(dk, bot, "keeps it present for the 10 minutes") // presence alone lasts 5
	for range 6000 {                                               // 10 a second over 10 minutes, one wall second each
		r2.adv(100 * time.Millisecond)
		r2.Inside(dk, Event{Agent: "a terminal", Kind: "screen", T: r2.now, Screen: &ck})
	}
	if got := len(r2.events(t)); got > 5001 {
		t.Fatalf("%d kept", got)
	}
	var clicked bool
	for _, s := range r2.Summary("alice") {
		if s.ID == "tok1" && len(s.Recent) == 1 && s.Recent[0].Action == "click" {
			clicked = true
		}
	}
	if !clicked {
		t.Fatal("inside flood evicted the outside agent's click")
	}
}

func TestResetInsideDropsRunning(t *testing.T) {
	r := newRig()
	r.Inside(dk, Event{Agent: "claude", Kind: "cmd-start", ID: "a", Cmd: "ls", T: t0})
	if len(r.Summary("alice")[0].Now) != 1 {
		t.Fatal("not running")
	}
	r.ResetInside(dk)
	for _, s := range r.Summary("alice") {
		if len(s.Now) != 0 || len(s.Recent) != 0 {
			t.Fatalf("%+v", s)
		}
	}
}

func TestForget(t *testing.T) {
	r := newRig()
	r.Screen(dk, bot, click)
	r.pres = 0
	r.Forget("tok1")
	if len(r.Present("alice")) != 0 || r.pres != 1 {
		t.Fatalf("pres=%d", r.pres)
	}
	r.Screen(dk, bot, click)
	r.ForgetDesktop("alice")
	if len(r.Present("alice")) != 0 {
		t.Fatal("desktop kept")
	}
}

func TestWatchScreen(t *testing.T) {
	r := newRig()
	if r.watched() {
		t.Fatal("watched")
	}
	ch, cancel := r.WatchScreen("alice")
	if !r.watched() {
		t.Fatal("not watched")
	}
	for _, a := range []string{"click", "move", "drag", "screenshot", "scroll", "zoom", "key", "mouse-down", "mouse-up", "hold-key", "type"} {
		r.Screen(dk, bot, ScreenAction{Action: a, Chars: 5})
	}
	var got []string
	for len(ch) > 0 {
		e := <-ch
		got = append(got, e.Action)
		if e.Action == "type" && e.Chars != 5 {
			t.Fatal("chars lost")
		}
		if e.Agent.ID != "tok1" || e.Agent.Color != Color("tok1") || !e.T.Equal(t0) {
			t.Fatalf("%+v", e)
		}
	}
	if want := []string{"click", "drag", "scroll", "key", "hold-key", "type"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	start := time.Now()
	for range 1000 {
		r.Screen(dk, bot, click)
	}
	// A blocking send would hang for good; the bound only has to catch
	// that, with room for -race on a slow CI runner.
	if time.Since(start) > 5*time.Second {
		t.Fatal("blocked by a stuck watcher")
	}
	cancel()
	cancel()
	for range ch {
	}
	if r.watched() {
		t.Fatal("still watched")
	}
	ch2, cancel2 := r.WatchScreen("alice")
	r.ForgetDesktop("alice")
	for range ch2 {
	}
	cancel2()
}

func TestCallbacksOutsideLock(t *testing.T) {
	r := newRig()
	r.OnPresence = func(Ref) { r.Present("alice"); r.Summary("alice") }
	r.OnSummary = func(Ref) { r.Present("alice"); r.Summary("alice") }
	done := make(chan struct{})
	go func() {
		r.Screen(dk, bot, click)
		r.Tick()
		r.Forget("tok1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock")
	}
}

func TestInsideBackdatingIsRateLimited(t *testing.T) {
	r := newRig()
	ck := click
	n := 0
	for i := range 6000 {
		at := t0.Add(-time.Duration(i%800) * time.Second)
		if r.Inside(dk, Event{Agent: "a terminal", Kind: "screen", T: at, Screen: &ck}) {
			n++
		}
	}
	if n != 5000 {
		t.Fatalf("accepted %d at one instant", n)
	}
}

// A backfill replays the ring at once: all of it counts, newest included.
func TestInsideBackfill(t *testing.T) {
	r := newRig()
	r.adv(time.Hour)
	start := r.now.Add(-10 * time.Minute)
	for i := range 60 { // 120 events over 10 minutes, the last a few seconds ago
		at := start.Add(time.Duration(i) * 10 * time.Second)
		id := fmt.Sprint(i)
		if !r.Inside(dk, Event{Agent: "claude", Kind: "cmd-start", ID: id, Cmd: "cmd " + id, T: at}) ||
			!r.Inside(dk, Event{Agent: "claude", Kind: "cmd-end", ID: id, Exit: new(int), T: at.Add(5 * time.Second)}) {
			t.Fatalf("event %d dropped", i)
		}
	}
	sums := r.Summary("alice")
	if len(sums) != 1 || sums[0].ID != "inside:claude" {
		t.Fatalf("present: %+v", sums)
	}
	if len(sums[0].Now) != 0 {
		t.Fatalf("still running: %+v", sums[0].Now)
	}
	if rec := sums[0].Recent; len(rec) == 0 || rec[0].Text != "cmd 59" {
		t.Fatalf("recent: %+v", rec)
	}

	// A start in the replay, its end after it: nothing is left running.
	r.Inside(dk, Event{Agent: "claude", Kind: "cmd-start", ID: "late", Cmd: "make", T: r.now})
	if len(r.nowItems()) != 1 {
		t.Fatal("start not taken")
	}
	r.adv(2 * time.Second)
	r.Inside(dk, Event{Agent: "claude", Kind: "cmd-end", ID: "late", Exit: new(int), T: r.now})
	if now := r.nowItems(); len(now) != 0 {
		t.Fatalf("stuck running: %+v", now)
	}
}

// A dropped event never reaches the tracker's state, so it's cheap; a
// bucket that isn't full keeps its desktop.
func TestInsideDroppedEarly(t *testing.T) {
	r := newRig()
	r.InsideBurst, r.InsideRate = 1, 1
	r.Inside(dk, Event{Agent: "a", Kind: "screen", T: t0, Screen: &ScreenAction{Action: "bogus"}}) // invalid, but takes the token
	if r.Inside(dk, Event{Agent: "a", Kind: "screen", T: t0, Screen: &ScreenAction{Action: "key"}}) {
		t.Fatal("over the limit, accepted")
	}
	r.Tick()
	r.mu.Lock()
	_, kept := r.desks["alice"]
	r.mu.Unlock()
	if !kept {
		t.Fatal("desk forgotten with an empty bucket: the limit would reset")
	}
	r.adv(time.Second)
	if !r.Inside(dk, Event{Agent: "a", Kind: "screen", T: r.now, Screen: &ScreenAction{Action: "key"}}) {
		t.Fatal("not refilled")
	}
}

func TestInsideCaps(t *testing.T) {
	r := newRig()
	r.Screen(dk, bot, click)
	for i := range 400 {
		if i%40 == 0 {
			r.adv(time.Second) // stay under the rate limit
		}
		r.Inside(dk, Event{Agent: "p", Kind: "cmd-start", ID: fmt.Sprint(i), Cmd: "x", T: r.now})
	}
	r.mu.Lock()
	n := len(r.desks["alice"].running)
	r.mu.Unlock()
	if n != maxInsideRunning {
		t.Fatalf("running %d", n)
	}
	for i := range 100 {
		if i%40 == 0 {
			r.adv(time.Second)
		}
		r.Inside(dk, Event{Agent: fmt.Sprint("prog", i), Kind: "screen", T: r.now, Screen: &ScreenAction{Action: "key"}})
	}
	inside := 0
	for _, p := range r.Present("alice") {
		if p.Inside {
			inside++
		}
	}
	if inside != maxInsideAgents || len(r.Present("alice")) != maxInsideAgents+1 {
		t.Fatalf("inside agents %d", inside)
	}
}
