package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestUIEventsWebSocketSnapshotAndUpdate(t *testing.T) {
	s, ts := newTestServer(t)
	client := login(t, ts, "admin")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	var msg struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	_, b, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &msg); err != nil || msg.Type != "desktop" || !strings.Contains(string(msg.Data), `"name":"alice"`) {
		t.Fatalf("snapshot: %s %v", b, err)
	}
	s.events.publish(event{kind: "stats", desktopID: "id-alice", data: map[string]string{"name": "alice"}})
	_, b, err = ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &msg); err != nil || msg.Type != "stats" {
		t.Fatalf("update: %s %v", b, err)
	}
}

func TestUIEventsWebSocketRejectsForeignOrigin(t *testing.T) {
	_, ts := newTestServer(t)
	client := login(t, ts, "admin")
	for _, path := range []string{"/api/events", "/api/desktops/alice/screen-events"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+path, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {"https://attacker.invalid"}}})
		cancel()
		if ws != nil {
			ws.CloseNow()
		}
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("%s: %v %v", path, resp, err)
		}
	}
}

func TestScreenEventsWebSocketClosesOnRevoke(t *testing.T) {
	s, ts := newTestServer(t)
	_ = s.Accounts.SetAccess("id-alice", []string{"anna"})
	client := login(t, ts, "anna")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/desktops/alice/screen-events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	s.activity.Screen(aliceRef, bot, click)
	_, b, err := ws.Read(ctx)
	if err != nil || !strings.Contains(string(b), `"type":"screen"`) || !strings.Contains(string(b), `"pos":[10,20]`) {
		t.Fatalf("screen: %s %v", b, err)
	}
	_ = s.Accounts.SetAccess("id-alice", nil)
	_, _, err = ws.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("idle stream did not close on revoke: %v", err)
	}
}

func TestUIEventsWebSocketRequiresSessionAndDesktopAccess(t *testing.T) {
	_, ts := newTestServer(t)
	anna := login(t, ts, "anna")
	for _, tc := range []struct {
		path   string
		client *http.Client
		status int
	}{
		{"/api/events", http.DefaultClient, http.StatusUnauthorized},
		{"/api/desktops/alice/screen-events", http.DefaultClient, http.StatusUnauthorized},
		{"/api/desktops/alice/screen-events", anna, http.StatusNotFound},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+tc.path, &websocket.DialOptions{HTTPClient: tc.client, HTTPHeader: http.Header{"Origin": {ts.URL}}})
		cancel()
		if ws != nil {
			ws.CloseNow()
		}
		if err == nil || resp == nil || resp.StatusCode != tc.status {
			t.Fatalf("%s: want %d, got %v %v", tc.path, tc.status, resp, err)
		}
	}
}

func TestUIEventsWebSocketRejectsInputAndUnsubscribes(t *testing.T) {
	s, ts := newTestServer(t)
	client := login(t, ts, "admin")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/events", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte("unexpected input")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("want policy violation, got %v", err)
	}
	for s.events.count() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("closed feed retained its subscriber")
		case <-time.After(time.Millisecond):
		}
	}
}
