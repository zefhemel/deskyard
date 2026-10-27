package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/forward"
)

var (
	aliceRef = activity.Ref{Name: "alice", ID: "alice"}
	bot      = activity.Agent{ID: "tok1", Name: "deploy-bot", Owner: "admin"}
	click    = activity.ScreenAction{Action: "click", Pos: []int{10, 20}}
)

func TestAgentsInDesktopJSON(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	c, err := s.Environments.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := s.Accounts.Get("admin")

	j := s.environmentJSON(ctx, c, admin)
	if j.Agents == nil || len(j.Agents) != 0 {
		t.Fatalf("no activity: agents = %#v", j.Agents)
	}
	b, _ := json.Marshal(j)
	if !strings.Contains(string(b), `"agents":[]`) {
		t.Errorf("agents should marshal as []: %s", b)
	}

	s.activity.Screen(aliceRef, bot, click)
	j = s.environmentJSON(ctx, c, admin)
	if len(j.Agents) != 1 || j.Agents[0].ID != "tok1" || j.Agents[0].Name != "deploy-bot" || !j.Agents[0].Active {
		t.Fatalf("agents = %#v", j.Agents)
	}
}

func TestActivityJSONAddsForwards(t *testing.T) {
	s, _ := newTestServer(t)
	lk := newForwardLink("deploy-bot", []int{5173})
	lk.tokenID = "tok1"
	s.forwards.tryAdd("alice", lk)
	done := s.activity.ForwardOpen(aliceRef, bot, "")
	defer done()

	j := s.activityJSON("alice")
	if j.Name != "alice" || len(j.Agents) != 1 {
		t.Fatalf("activity = %#v", j)
	}
	var fw []activity.NowItem
	for _, it := range j.Agents[0].Now {
		if it.Kind == "forward" {
			fw = append(fw, it)
		}
	}
	if len(fw) != 1 || fw[0].Text != "5173 out" || fw[0].Since.IsZero() {
		t.Fatalf("forward now items = %#v", fw)
	}

	// An in port the desktop listens on is listed before the out ports.
	lk.listens[1] = 3000
	lk.fromHelper(forward.TypeListening, 1)
	j = s.activityJSON("alice")
	for _, it := range j.Agents[0].Now {
		if it.Kind == "forward" && it.Text != "3000 in · 5173 out" {
			t.Errorf("text = %q", it.Text)
		}
	}

	// Another token's links are not this agent's.
	if got := s.forwards.listFor("alice", "tok2"); len(got) != 0 {
		t.Errorf("tok2 = %#v", got)
	}
	// Links without an owner belong to no one.
	s.forwards.tryAdd("alice", newForwardLink("old", []int{80}))
	if got := s.forwards.listFor("alice", ""); len(got) != 0 {
		t.Errorf("no owner = %#v", got)
	}
}

// subscribe adds a raw event subscriber to the hub.
func subscribe(t *testing.T, s *Server) chan event {
	sub := &subscriber{username: "admin", c: make(chan event, 256), refresh: make(chan struct{}, 1)}
	s.events.mu.Lock()
	s.events.subs[sub] = true
	s.events.mu.Unlock()
	t.Cleanup(func() {
		s.events.mu.Lock()
		delete(s.events.subs, sub)
		s.events.mu.Unlock()
	})
	return sub.c
}

func TestPresenceChangePublishesViewers(t *testing.T) {
	s, _ := newTestServer(t)
	evs := subscribe(t, s)
	// A viewers event just went out: the presence change must not wait
	// for the viewers throttle.
	s.publishViewers("alice", "alice")
	for len(evs) < 2 { // viewers and activity
		time.Sleep(time.Millisecond)
	}
	<-evs
	<-evs
	time.Sleep(10 * time.Millisecond) // the activity throttle's next run is a trailing one

	s.activity.Screen(aliceRef, bot, click)
	select {
	case e := <-evs:
		if e.kind != "viewers" || e.environment != "alice" {
			t.Fatalf("first event = %s %s", e.kind, e.environment)
		}
		b, _ := json.Marshal(e.data)
		var d struct {
			Name   string              `json:"name"`
			Agents []activity.Presence `json:"agents"`
		}
		_ = json.Unmarshal(b, &d)
		if d.Name != "alice" || len(d.Agents) != 1 || d.Agents[0].ID != "tok1" {
			t.Fatalf("viewers data = %s", b)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no viewers event")
	}

	for range 20 {
		s.activity.Screen(aliceRef, bot, click)
		time.Sleep(5 * time.Millisecond)
	}
	activities := 0
	deadline := time.After(1500 * time.Millisecond)
	for done := false; !done; {
		select {
		case e := <-evs:
			if e.kind == "activity" {
				activities++
				if e.environment != "alice" {
					t.Errorf("activity environment = %q", e.environment)
				}
				if a, ok := e.data.(activityJSON); !ok || a.Name != "alice" || len(a.Agents) != 1 {
					t.Errorf("activity data = %#v", e.data)
				}
			}
		case <-deadline:
			done = true
		}
	}
	if activities < 1 || activities > 2 {
		t.Errorf("%d activity events, want 1 or 2", activities)
	}
}

func TestPresenceKnob(t *testing.T) {
	for _, c := range []struct{ presence, active time.Duration }{
		{2 * time.Second, 2 * time.Second},
		{12 * time.Second, 10 * time.Second},
		{0, 10 * time.Second},
	} {
		dir := t.TempDir()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		accts, _ := accounts.Load(dir)
		e := fakeEngine{}
		s, err := New(Options{Log: log, Eng: e, Accounts: accts, Layout: config.NewLayout(dir), DefaultImage: "img",
			Environments:     environments.New(e, environments.Config{DataDir: dir, Subnet: "10.0.0.0/16"}, log),
			ActivityPresence: c.presence})
		if err != nil {
			t.Fatal(err)
		}
		want := c.presence
		if want == 0 {
			want = 5 * time.Minute
		}
		if s.activity.Presence != want || s.activity.ActiveFor != c.active {
			t.Errorf("%v: presence %v, active %v", c.presence, s.activity.Presence, s.activity.ActiveFor)
		}
	}
}

func TestActivityRoutes(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	for _, p := range []string{"/api/environments/alice/activity", "/api/environments/alice/screen-events"} {
		if got := status(t, anna, "GET", ts.URL+p, ""); got != 404 {
			t.Errorf("anna %s: %d", p, got)
		}
	}
	s.activity.Screen(aliceRef, bot, click)
	resp, err := admin.Get(ts.URL + "/api/environments/alice/activity")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var j struct {
		Name   string             `json:"name"`
		Agents []activity.Summary `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&j); err != nil || j.Name != "alice" || len(j.Agents) != 1 {
		t.Fatalf("activity: %v %#v", err, j)
	}
}

func TestScreenEventsStream(t *testing.T) {
	s, ts := newTestServer(t)
	_ = s.Accounts.SetAccess("alice", fullAccess("anna"))
	anna := login(t, ts, "anna")
	resp, err := anna.Get(ts.URL + "/api/environments/alice/screen-events")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("screen-events: %v %v", err, resp)
	}
	defer resp.Body.Close()
	lines := make(chan string, 16)
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(b)
			if n > 0 {
				lines <- string(b[:n])
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	// Click until the stream, once subscribed, passes one on.
	deadline := time.After(2 * time.Second)
	for got := ""; !strings.Contains(got, "event: screen\n"); {
		s.activity.Screen(aliceRef, bot, click)
		select {
		case got = <-lines:
			if !strings.Contains(got, "event: screen\n") {
				continue // a comment line, before the subscription
			}
			if !strings.Contains(got, `"action":"click"`) || !strings.Contains(got, `"pos":[10,20]`) {
				t.Errorf("stream = %q", got)
			}
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("no screen event")
		}
	}

	// Taking away access closes the stream, even with nothing to send.
	_ = s.Accounts.SetAccess("alice", nil)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-lines:
			if !open {
				return
			}
		case <-timeout:
			t.Fatal("stream still open after revoke")
		}
	}
}

// within fails the test if f doesn't return: a Tracker callback that took a
// lock the caller held would deadlock.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s deadlocked", what)
	}
}

func TestTokenGoneForgetsAgent(t *testing.T) {
	s, _ := newTestServer(t)
	mk := func(name string, exp *time.Time) token {
		tk, _, err := s.tokens.create(name, "alice", "admin", exp)
		if err != nil {
			t.Fatal(err)
		}
		s.activity.Screen(aliceRef, s.agentOf(tk), click)
		return tk
	}
	var gone []string
	orig := s.tokens.onGone
	s.tokens.onGone = func(tk token) { gone = append(gone, tk.ID); orig(tk) }

	// A live forward link of the token: Forget publishes viewers, which reads
	// the forwards; it must run with no lock held.
	del := mk("one", nil)
	lk := newForwardLink("one", []int{5173})
	lk.tokenID = del.ID
	s.forwards.tryAdd("alice", lk)
	within(t, "remove", func() { s.tokens.remove(func(x token) bool { return x.ID == del.ID }) })
	if len(gone) != 1 || gone[0] != del.ID {
		t.Fatalf("onGone after delete: %v", gone)
	}

	past := time.Now().Add(-time.Hour)
	exp := mk("two", &past)
	within(t, "expireNow", func() { s.tokens.expireNow(time.Now()); s.tokens.expireNow(time.Now()) })
	if len(gone) != 2 || gone[1] != exp.ID {
		t.Fatalf("onGone after expiry (once): %v", gone)
	}
	if p := s.activity.Present("alice"); len(p) != 0 {
		t.Fatalf("still present: %#v", p)
	}
}

func TestForwardOpenDoesNotHoldLocks(t *testing.T) {
	s, _ := newTestServer(t)
	lk := newForwardLink("deploy-bot", []int{5173})
	lk.tokenID = bot.ID
	s.forwards.tryAdd("alice", lk)
	within(t, "ForwardOpen", func() {
		done := s.activity.ForwardOpen(aliceRef, bot, "forward")
		s.forwards.remove("alice", lk)
		done()
	})
}

func TestExecEnvWins(t *testing.T) {
	env := execEnvFor(map[string]string{"DESKYARD_ACTIVITY": "on", "FOO": "1"})
	has := func(kv string) bool { return slices.Contains(env, kv) }
	if !has("DESKYARD_ACTIVITY=off") || !has("FOO=1") || has("DESKYARD_ACTIVITY=on") || !has("WORKSPACE_PROXY_TOKEN=") {
		t.Errorf("env = %v", env)
	}
}
