package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
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
	c, err := s.Computers.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := s.Accounts.Get("admin")

	j := s.computerJSON(ctx, c, admin)
	if j.Agents == nil || len(j.Agents) != 0 {
		t.Fatalf("no activity: agents = %#v", j.Agents)
	}
	b, _ := json.Marshal(j)
	if !strings.Contains(string(b), `"agents":[]`) {
		t.Errorf("agents should marshal as []: %s", b)
	}

	s.activity.Screen(aliceRef, bot, click)
	j = s.computerJSON(ctx, c, admin)
	if len(j.Agents) != 1 || j.Agents[0].ID != "tok1" || j.Agents[0].Name != "deploy-bot" || !j.Agents[0].Active {
		t.Fatalf("agents = %#v", j.Agents)
	}
}

func TestActivityJSONAddsForwards(t *testing.T) {
	s, _ := newTestServer(t)
	lk := newForwardLink("deploy-bot", "tok1", []int{5173})
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
	s.forwards.tryAdd("alice", newForwardLink("old", "old", []int{80}))
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
		if e.kind != "viewers" || e.computer != "alice" {
			t.Fatalf("first event = %s %s", e.kind, e.computer)
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
				if e.computer != "alice" {
					t.Errorf("activity computer = %q", e.computer)
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
			Computers:        computers.New(e, computers.Config{DataDir: dir, Subnet: "10.0.0.0/16"}, log),
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

func TestCredentialGoneForgetsAgent(t *testing.T) {
	s, _ := newTestServer(t)
	mk := func(a accounts.Account) credential {
		c, _ := issueCredential(t, s, a)
		s.activity.Screen(aliceRef, s.agentOf(c), click)
		return c
	}
	var gone []string
	orig := s.credentials.onGone
	s.credentials.onGone = func(c credential) { gone = append(gone, c.ID); orig(c) }

	// A live forward link of the credential: Forget publishes viewers, which
	// reads the forwards; it must run with no lock held.
	del := mk(accountNamed(t, s, "admin"))
	s.forwards.tryAdd("alice", newForwardLink(del.Name, del.ID, []int{5173}))
	within(t, "remove", func() { s.credentials.remove(func(x credential) bool { return x.ID == del.ID }) })
	if len(gone) != 1 || gone[0] != del.ID {
		t.Fatalf("onGone after delete: %v", gone)
	}

	// Deleting the account deletes its credentials (the sweep).
	ofAnna := mk(accountNamed(t, s, "anna"))
	within(t, "account deletion", func() {
		if err := s.Accounts.Delete("anna"); err != nil {
			t.Error(err)
		}
	})
	if len(gone) != 2 || gone[1] != ofAnna.ID {
		t.Fatalf("onGone after the account's deletion: %v", gone)
	}
	if p := s.activity.Present("alice"); len(p) != 0 {
		t.Fatalf("still present: %#v", p)
	}
}

func TestForwardOpenDoesNotHoldLocks(t *testing.T) {
	s, _ := newTestServer(t)
	lk := newForwardLink("deploy-bot", bot.ID, []int{5173})
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

// A browser login that ended between its request's check and the stream's
// registration doesn't get the agents' screen actions.
func TestScreenEventsRecheckSession(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	anna := accountNamed(t, s, "anna")
	claims := &auth.SessionClaims{Username: "anna", AccountID: anna.ID, Epoch: anna.SessionEpoch}
	if _, err := s.Accounts.BumpSessionEpoch("anna"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("name", "alice")
		s.handleScreenEvents(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: anna, claims: claims})))
	}))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("screen events for a browser login that ended: %d", resp.StatusCode)
	}
}
