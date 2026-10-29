package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
)

func TestDockerAccessSettingsAPI(t *testing.T) {
	e := &hostSettingsEngine{current: alice}
	e.current.Labels = maps.Clone(alice.Labels)
	e.current.Image = "img"
	e.current.Env = map[string]string{computers.TokenEnv: "token"}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	send := func(client *http.Client, method, path string, body map[string]any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(raw))
		req.Header.Set("Origin", ts.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	for _, method := range []string{"POST", "PATCH"} {
		path := "/api/environments"
		if method == "PATCH" {
			path += "/alice"
		}
		if code, _ := send(anna, method, path, map[string]any{"dockerAccess": true}); code != 403 {
			t.Fatalf("%s non-admin: %d", method, code)
		}
	}
	exposed := func(want bool) {
		t.Helper()
		code, b := send(admin, "GET", "/api/environments/alice", nil)
		var got struct {
			Settings struct {
				DockerAccess bool `json:"dockerAccess"`
			} `json:"settings"`
		}
		if code != 200 || json.Unmarshal(b, &got) != nil || got.Settings.DockerAccess != want {
			t.Fatalf("GET settings want %t: %d %s", want, code, b)
		}
	}
	patch := func(body map[string]any) {
		t.Helper()
		code, b := send(admin, "PATCH", "/api/environments/alice", body)
		if code != 202 {
			t.Fatalf("PATCH %d %s", code, b)
		}
		var res struct{ Job string }
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		j := s.jobs.get(res.Job)
		select {
		case <-j.done:
		case <-time.After(5 * time.Second):
			t.Fatal("job timeout")
		}
		if st := j.get(); st.Status != "done" {
			t.Fatalf("job %+v", st)
		}
	}
	exposed(false)
	patch(map[string]any{"dockerAccess": true})
	exposed(true)
	patch(map[string]any{"nanoCpus": int64(1e9)})
	exposed(true)
	d, _ := s.Computers.Get(context.Background(), "alice")
	if !d.Settings.DockerAccess || d.Container.Env["DESKYARD_DOCKER_ACCESS"] != "1" {
		t.Fatal("permission lost during unrelated update")
	}
	patch(map[string]any{"dockerAccess": false})
	exposed(false)
	d, _ = s.Computers.Get(context.Background(), "alice")
	if d.Container.Env["DESKYARD_DOCKER_ACCESS"] != "" {
		t.Fatal("revocation retained startup permission")
	}
}
