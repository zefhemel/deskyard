package server

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type hostSettingsEngine struct {
	fakeEngine
	mu      sync.Mutex
	current engine.Container
}

func (e *hostSettingsEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == e.current.Name || id == e.current.ID {
		return e.current, nil
	}
	return engine.Container{}, engine.ErrNotFound
}
func (e *hostSettingsEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []engine.Container{e.current}, nil
}
func (e *hostSettingsEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{ID: "img", Labels: map[string]string{computers.LabelContract: computers.Contract}}, nil
}
func (e *hostSettingsEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.Labels[computers.LabelManaged] == "true" {
		e.current = engine.Container{ID: s.Name, Name: s.Name, Image: s.Image, ImageID: "img", Labels: s.Labels, Env: s.Env, State: "exited"}
	}
	return s.Name, nil
}
func (e *hostSettingsEngine) Rename(_ context.Context, id, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id != e.current.Name && id != e.current.ID {
		return engine.ErrNotFound
	}
	e.current.Name = name
	return nil
}
func (e *hostSettingsEngine) Remove(context.Context, string) error { return nil }
func (e *hostSettingsEngine) Start(context.Context, string) error  { return nil }
func (e *hostSettingsEngine) Exec(context.Context, string, engine.ExecSpec) (*engine.Process, error) {
	return &engine.Process{Stdout: bytes.NewReader(nil), Stderr: bytes.NewReader(nil), Wait: func() (int, error) { return 0, nil }, Close: func() {}}, nil
}

func TestHostVolumeSettingsAPI(t *testing.T) {
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
	send := func(client *http.Client, method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.Bytes()
	}
	// Host folders were replaced by host volumes: a request with any is refused.
	for _, req := range []struct{ method, path, body string }{
		{"PATCH", "/api/computers/alice", `{"mounts":[{"source":"/host/data","target":"/mnt/data","readOnly":true}]}`},
		{"POST", "/api/computers", `{"name":"new-desktop","mounts":[{"source":"/host/data","target":"/mnt/data"}]}`},
	} {
		if code, b := send(admin, req.method, req.path, req.body); code != 400 || !strings.Contains(string(b), "host volume") {
			t.Fatalf("%s %s with host folders: %d %s", req.method, req.path, code, b)
		}
	}
	source := t.TempDir()
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos","hostPath":"relative"}`); code != 400 {
		t.Fatalf("relative host path: %d %s", code, b)
	}
	code, b := send(admin, "POST", "/api/volumes", `{"name":"photos","hostPath":"`+source+`"}`)
	if code != 201 || !strings.Contains(string(b), `"kind":"host"`) || !strings.Contains(string(b), `"hostPath":"`+source+`"`) || !strings.Contains(string(b), `"folder":"`+source+`"`) {
		t.Fatalf("create host volume: %d %s", code, b)
	}
	if code, b := send(admin, "PATCH", "/api/computers/alice", `{"volumes":[{"name":"photos","path":"/workspace/photos"}]}`); code != 400 {
		t.Fatalf("unsafe grant path: %d %s", code, b)
	}
	if code, _ := send(anna, "PATCH", "/api/computers/alice", `{"volumes":[]}`); code != 403 {
		t.Fatalf("non-admin: %d", code)
	}
	grants := []computers.VolumeGrant{{Name: "photos", Path: "/mnt/photos", ReadOnly: true}}
	patch := func(body any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, b := send(admin, "PATCH", "/api/computers/alice", string(raw))
		if code != 202 {
			t.Fatalf("PATCH: %d %s", code, b)
		}
		var res struct{ Job string }
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		j := s.jobs.get(res.Job)
		select {
		case <-j.done:
		case <-time.After(5 * time.Second):
			t.Fatal("job timed out")
		}
		if st := j.get(); st.Status != "done" {
			t.Fatalf("job: %+v", st)
		}
	}
	patch(map[string]any{"volumes": grants})
	code, b = send(admin, "GET", "/api/computers/alice", "")
	if code != 200 {
		t.Fatal(code)
	}
	var got struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	var gotGrants []computers.VolumeGrant
	if err := json.Unmarshal(got.Settings["volumes"], &gotGrants); err != nil || !reflect.DeepEqual(gotGrants, grants) {
		t.Fatalf("not exposed by API: %s", b)
	}
	if _, ok := got.Settings["mounts"]; ok {
		t.Fatalf("settings still have mounts: %s", b)
	}
	patch(map[string]any{"nanoCpus": int64(1e9)})
	c, _ := s.Computers.Get(context.Background(), "alice")
	if !reflect.DeepEqual(c.Settings.Volumes, grants) {
		t.Fatal("omitting volumes lost them")
	}
}
