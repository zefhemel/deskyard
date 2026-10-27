package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// observeAlice makes bob an observer of alice and anna a full user of it.
func observeAlice(t *testing.T, s *Server) {
	t.Helper()
	createBob(t, s)
	if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve, "anna": auth.LevelFull}); err != nil {
		t.Fatal(err)
	}
}

func TestObserverCanSeeButNotUse(t *testing.T) {
	s, ts := newTestServer(t)
	observeAlice(t, s)
	bob := login(t, ts, "bob")
	base := ts.URL + "/api/environments/alice"
	for _, path := range []string{base, base + "/stats", ts.URL + "/alice/"} {
		if got := status(t, bob, "GET", path, ""); got != 200 {
			t.Errorf("observer GET %s: %d", path, got)
		}
	}
	// alice is stopped: past the permission check, the screenshot needs a
	// running desktop.
	if got := status(t, bob, "GET", base+"/screen.png", ""); got != http.StatusConflict {
		t.Errorf("observer screen.png: %d", got)
	}
	for _, path := range []string{"/processes", "/folder", "/files", "/download", "/activity", "/terminal/sessions"} {
		if got := status(t, bob, "GET", base+path, ""); got != 403 {
			t.Errorf("observer GET %s: %d", path, got)
		}
	}
	for _, path := range []string{"/start", "/stop", "/mic", "/upload", "/terminal/sessions"} {
		if got := status(t, bob, "POST", base+path, ts.URL); got != 403 {
			t.Errorf("observer POST %s: %d", path, got)
		}
	}
	// The refusal names observe-only access.
	resp, body := get(t, bob, base+"/processes")
	if resp.StatusCode != 403 || !strings.Contains(body, "observe-only access can't do that") {
		t.Errorf("processes refusal: %d %s", resp.StatusCode, body)
	}
	// Admin-only routes keep their admin refusal.
	if got := status(t, bob, "DELETE", base, ts.URL); got != 403 {
		t.Errorf("observer DELETE: %d", got)
	}

	var env map[string]any
	_, body = get(t, bob, base)
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatal(err)
	}
	settings := env["settings"].(map[string]any)
	if env["level"] != "observe" || env["folder"] != "" || len(settings["mounts"].([]any)) != 0 ||
		len(settings["volumes"].([]any)) != 0 || settings["vpnConnectionId"] != nil || env["memoryReclaim"] != false {
		t.Errorf("observer's environment: %s", body)
	}
	if _, ok := env["observers"].([]any); !ok {
		t.Errorf("no observers list: %s", body)
	}

	var list struct{ Environments []map[string]any }
	_, body = get(t, bob, ts.URL+"/api/environments")
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list.Environments) != 1 || list.Environments[0]["level"] != "observe" {
		t.Errorf("observer's list: %s", body)
	}

	anna := login(t, ts, "anna")
	_, body = get(t, anna, base)
	env = map[string]any{}
	_ = json.Unmarshal([]byte(body), &env)
	if env["level"] != "full" || env["folder"] == "" {
		t.Errorf("full user's environment: %s", body)
	}
}

func TestObserverEvents(t *testing.T) {
	observer := accounts.Account{Username: "bob", Grants: []accounts.Grant{{Environment: "alice", Level: auth.LevelObserve}}}
	full := accounts.Account{Username: "anna", Grants: []accounts.Grant{{Environment: "alice", Level: auth.LevelFull}}}
	viewers := event{kind: "viewers", environment: "alice", data: map[string]any{"name": "alice", "viewers": []string{"anna"},
		"forwards": []forwardJSON{{Token: "laptop", Direction: "in", Port: 3000}}, "agents": []string{"x"}}}
	activity := event{kind: "activity", environment: "alice", data: map[string]any{}}
	if _, ok := eventFor(observer, activity); ok {
		t.Error("an observer gets activity events")
	}
	if _, ok := eventFor(full, activity); !ok {
		t.Error("a full user misses activity events")
	}
	e, ok := eventFor(observer, viewers)
	data := e.data.(map[string]any)
	agents, _ := json.Marshal(data["agents"])
	if !ok || len(data["forwards"].([]forwardJSON)) != 0 || string(agents) != "[]" || data["name"] != "alice" {
		t.Errorf("observer's viewers event: %v %+v", ok, e.data)
	}
	if e, _ := eventFor(full, viewers); len(e.data.(map[string]any)["forwards"].([]forwardJSON)) != 1 {
		t.Error("a full user's viewers event lost its forwards")
	}
	if len(viewers.data.(map[string]any)["forwards"].([]forwardJSON)) != 1 {
		t.Error("redacting changed the shared event")
	}
	if _, ok := eventFor(accounts.Account{Username: "carol"}, viewers); ok {
		t.Error("an account without a grant gets the event")
	}
}
