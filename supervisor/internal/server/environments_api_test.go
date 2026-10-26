package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

func getJSON(t *testing.T, c *http.Client, url string, v any) int {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if v != nil && res.StatusCode == 200 {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s: %v: %s", url, err, b)
		}
	}
	return res.StatusCode
}

func TestOldDesktopRoutesGone(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/desktops"}, {"POST", "/api/desktops"}, {"GET", "/api/desktops/alice"},
		{"POST", "/api/desktops/alice/start"}, {"GET", "/api/desktops/alice/files"},
	} {
		if got := status(t, admin, r.method, ts.URL+r.path, ts.URL); got != 404 {
			t.Errorf("%s %s: %d", r.method, r.path, got)
		}
	}
	if got := status(t, admin, "GET", ts.URL+"/api/environments/alice", ""); got != 200 {
		t.Fatalf("new route: %d", got)
	}
}

func TestMissingEnvironmentIs404(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	res, err := admin.Get(ts.URL + "/api/environments/nobody")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 || !strings.Contains(string(b), "no such environment") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

// environmentList is the list response, decoded loosely so that renamed or
// leftover fields show.
type environmentList struct {
	Environments []map[string]any `json:"environments"`
	Conflicts    []map[string]any `json:"conflicts"`
}

func TestEnvironmentListShowsDefinitionState(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	if _, err := s.Environments.Defs.Update("alice", func(d *environments.Definition) error { d.Revision = 3; return nil }); err != nil {
		t.Fatal(err)
	}
	var list environmentList
	if got := getJSON(t, admin, ts.URL+"/api/environments", &list); got != 200 {
		t.Fatal(got)
	}
	if len(list.Environments) != 1 || list.Conflicts == nil {
		t.Fatalf("%+v", list)
	}
	e := list.Environments[0]
	if e["name"] != "alice" || e["revision"] != 3.0 || e["appliedRevision"] != 1.0 || e["power"] != "stopped" ||
		e["error"] != "" || e["conflict"] != "" || e["memoryReclaim"] != true {
		t.Fatalf("%+v", e)
	}
	for _, gone := range []string{"id", "autoReclaim"} {
		if _, ok := e[gone]; ok {
			t.Errorf("%s still in %+v", gone, e)
		}
	}
}

func TestEnvironmentListReportsConflictsToAdmins(t *testing.T) {
	ghost := engine.Container{ID: "c9", Name: "deskyard-ghost", State: "running",
		Labels: map[string]string{environments.LabelManaged: "true", environments.LabelName: "ghost", environments.LabelRevision: "1"}}
	s, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{ghost}})
	// The fake server defines every container it finds; this one isn't.
	if err := s.Environments.Defs.Delete("ghost"); err != nil {
		t.Fatal(err)
	}
	s.Environments.ReconcileOnce(context.Background())
	admin, anna := login(t, ts, "admin"), login(t, ts, "anna")
	var list environmentList
	getJSON(t, admin, ts.URL+"/api/environments", &list)
	if len(list.Conflicts) != 1 {
		t.Fatalf("%+v", list.Conflicts)
	}
	c := list.Conflicts[0]
	if c["name"] != "ghost" || c["kind"] != "unknown" || c["message"] == "" {
		t.Fatalf("%+v", c)
	}
	if cs, _ := c["containers"].([]any); len(cs) != 1 || cs[0] != "deskyard-ghost" {
		t.Fatalf("%+v", c)
	}
	list = environmentList{}
	getJSON(t, anna, ts.URL+"/api/environments", &list)
	if list.Conflicts == nil || len(list.Conflicts) != 0 {
		t.Fatalf("anna sees %+v", list.Conflicts)
	}
}

func TestJobAndAccountJSONUseEnvironmentNames(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	var accts []map[string]any
	getJSON(t, admin, ts.URL+"/api/accounts", &accts)
	for _, a := range accts {
		if a["username"] == "anna" {
			if envs, _ := a["environments"].([]any); len(envs) != 1 || envs[0] != "alice" {
				t.Fatalf("%+v", a)
			}
			if _, ok := a["desktops"]; ok {
				t.Fatalf("%+v", a)
			}
		}
	}
	req, _ := http.NewRequest("PATCH", ts.URL+"/api/accounts/anna", strings.NewReader(`{"environments":[]}`))
	req.Header.Set("Origin", ts.URL)
	res, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if a, _ := s.Accounts.Get("anna"); res.StatusCode != 200 || len(a.Environments) != 0 {
		t.Fatalf("%d %+v", res.StatusCode, a.Environments)
	}
	id := s.jobs.start("admin", "alice", func(context.Context, func(string, float64)) error { return nil })
	if got := s.jobs.get(id); got == nil || got.get().Environment != "alice" {
		t.Fatalf("job %+v", got)
	}
	b, _ := json.Marshal(jobState{Status: "running", Environment: "alice"})
	if !strings.Contains(string(b), `"environment":"alice"`) {
		t.Fatal(string(b))
	}
}

func TestEventsSendConflictsToAdmins(t *testing.T) {
	ghost := engine.Container{ID: "c9", Name: "deskyard-ghost", State: "running",
		Labels: map[string]string{environments.LabelManaged: "true", environments.LabelName: "ghost", environments.LabelRevision: "1"}}
	s, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{ghost}})
	if err := s.Environments.Defs.Delete("ghost"); err != nil {
		t.Fatal(err)
	}
	s.Environments.ReconcileOnce(context.Background())
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	snapshot := func(user string) map[string]string {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/events",
			&websocket.DialOptions{HTTPClient: login(t, ts, user), HTTPHeader: http.Header{"Origin": {ts.URL}}})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.CloseNow()
		got := map[string]string{}
		// The snapshot is alice, then the conflicts when there are any;
		// a stats or viewers event may follow.
		for range 2 {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			_, b, err := ws.Read(ctx)
			cancel()
			if err != nil {
				break
			}
			var msg struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(b, &msg); err != nil {
				t.Fatal(err)
			}
			got[msg.Type] = string(msg.Data)
		}
		return got
	}
	if got := snapshot("admin"); !strings.Contains(got["environment"], `"name":"alice"`) || !strings.Contains(got["conflicts"], `"kind":"unknown"`) {
		t.Fatalf("admin: %v", got)
	}
	if got := snapshot("anna"); got["environment"] == "" || got["conflicts"] != "" {
		t.Fatalf("anna: %v", got)
	}
}
