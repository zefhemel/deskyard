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

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
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
	return engine.Image{ID: "img", Labels: map[string]string{environments.LabelContract: environments.Contract}}, nil
}
func (e *hostSettingsEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.Labels[environments.LabelManaged] == "true" {
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

func TestHostMountSettingsAPI(t *testing.T) {
	e := &hostSettingsEngine{current: alice}
	e.current.Labels = maps.Clone(alice.Labels)
	e.current.Image = "img"
	e.current.Env = map[string]string{environments.TokenEnv: "token"}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
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
	invalid := `{"mounts":[{"source":"/host/data","target":"/workspace","readOnly":true}]}`
	if code, _ := send(admin, "PATCH", "/api/environments/alice", invalid); code != 400 {
		t.Fatalf("unsafe target: %d", code)
	}
	if code, _ := send(admin, "POST", "/api/environments", `{"name":"new-desktop","mounts":[{"source":"relative","target":"/mnt/data"}]}`); code != 400 {
		t.Fatalf("invalid create mounts: %d", code)
	}
	if code, _ := send(anna, "PATCH", "/api/environments/alice", `{"mounts":[]}`); code != 403 {
		t.Fatalf("non-admin: %d", code)
	}
	mounts := []environments.HostMount{{Source: t.TempDir(), Target: "/mnt/photos", ReadOnly: true}}
	patch := func(body any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		code, b := send(admin, "PATCH", "/api/environments/alice", string(raw))
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
	patch(map[string]any{"mounts": mounts})
	code, b := send(admin, "GET", "/api/environments/alice", "")
	if code != 200 {
		t.Fatal(code)
	}
	var got struct {
		Settings struct {
			Mounts []environments.HostMount `json:"mounts"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Settings.Mounts, mounts) {
		t.Fatalf("not exposed by API: %s", b)
	}
	patch(map[string]any{"nanoCpus": int64(1e9)})
	c, _ := s.Environments.Get(context.Background(), "alice")
	if !reflect.DeepEqual(c.Settings.Mounts, mounts) {
		t.Fatal("omitting mounts lost them")
	}
	patch(map[string]any{"mounts": []environments.HostMount{}})
	c, _ = s.Environments.Get(context.Background(), "alice")
	if len(c.Settings.Mounts) != 0 {
		t.Fatal("empty list did not detach")
	}
}
