package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// This fixture enters the public service operation lock through Stop. No
// production lock/test hooks are needed to exercise the real HTTP job boundary.
type rolloutEngine struct {
	fakeEngine
	mu                       sync.Mutex
	consumer                 *engine.Container
	stopEntered, stopRelease chan struct{}
	validated                atomic.Bool
	listed                   chan struct{}
	creates                  int
}

func (e *rolloutEngine) List(ctx context.Context, _ map[string]string) ([]engine.Container, error) {
	if e.validated.Load() {
		select {
		case e.listed <- struct{}{}:
		default:
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.consumer == nil {
		return nil, nil
	}
	return []engine.Container{*e.consumer}, nil
}
func (e *rolloutEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.consumer != nil && (id == e.consumer.ID || id == e.consumer.Name) {
		return *e.consumer, nil
	}
	return engine.Container{}, engine.ErrNotFound
}
func (e *rolloutEngine) Stop(ctx context.Context, _ string, _ time.Duration) error {
	if e.stopEntered != nil {
		close(e.stopEntered)
		select {
		case <-e.stopRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (e *rolloutEngine) UpdateRestartPolicy(context.Context, string, string) error { return nil }
func (e *rolloutEngine) Remove(context.Context, string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consumer = nil
	return nil
}
func (e *rolloutEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return nil, nil
}
func (e *rolloutEngine) CreateNetwork(context.Context, string, string, map[string]string) error {
	return nil
}
func (e *rolloutEngine) Create(_ context.Context, s engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.creates++
	e.consumer = &engine.Container{ID: "replacement", Name: s.Name, Labels: s.Labels, NetworkContainer: s.NetworkContainer, State: "exited", Restart: s.Restart}
	return "replacement", nil
}

func (*rolloutEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	return engine.Image{ID: ref}, nil
}

type rolloutRuntime struct {
	entered, release chan struct{}
	fail             bool
}

func (*rolloutRuntime) SetReservedNetworks([]netip.Prefix) error { return nil }
func (r *rolloutRuntime) Ensure(ctx context.Context, _, _, _ string, _ []vpn.Exception) (engine.Container, error) {
	if r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return engine.Container{}, ctx.Err()
		}
	}
	if r.fail {
		return engine.Container{}, errors.New("secret provider details")
	}
	return engine.Container{ID: "gateway", State: "running"}, nil
}
func (*rolloutRuntime) Remove(context.Context, string) error { return nil }
func (*rolloutRuntime) Status(context.Context, string) (vpn.Status, error) {
	return vpn.Status{State: "connecting"}, nil
}

type rolloutTester struct {
	e *rolloutEngine
	apiVPNTester
}

func (r rolloutTester) TestSaved(context.Context, string, vpn.Input) (vpn.Status, error) {
	r.e.validated.Store(true)
	return vpn.Status{State: "connected"}, nil
}
func awaitRollout(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("rollout boundary not reached")
	}
}

func TestVPNUpdateCancellationBoundary(t *testing.T) {
	for _, postcommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting-for-operation", true: "committed-recovery"}[postcommit], func(t *testing.T) {
			e := &rolloutEngine{listed: make(chan struct{}, 1)}
			s, ts := newTestServerWith(t, e)
			admin := login(t, ts, "admin")
			st := s.Computers.Networking.Connections
			user, password := "user", "password"
			conn, err := st.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &user, Password: &password})
			if err != nil {
				t.Fatal(err)
			}
			e.consumer = &engine.Container{ID: "original", Name: "deskyard-alice", State: "exited", Restart: "no", NetworkContainer: "gateway", Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: "alice", computers.LabelRevision: "1", vpn.LabelConnection: conn.ID}}
			defineComputers(t, s)
			dir := t.TempDir()
			record := map[string]any{"name": "alice", "connectionRevision": 1, "appliedConnectionRevision": 1, "appliedRevision": 1, "runtimeId": "gateway"}
			if err = store.WriteJSON(filepath.Join(dir, "network", "transitions.json"), map[string]any{"version": 1, "transitions": []any{record}}); err != nil {
				t.Fatal(err)
			}
			runtime := &rolloutRuntime{}
			if err = s.Computers.ConfigureNetworking(st, runtime, dir); err != nil {
				t.Fatal(err)
			}
			s.vpnTests = rolloutTester{e: e}
			stopDone := make(chan error, 1)
			if !postcommit {
				e.stopEntered = make(chan struct{})
				e.stopRelease = make(chan struct{})
				go func() { stopDone <- s.Computers.Stop(context.Background(), "alice") }()
				awaitRollout(t, e.stopEntered)
			} else {
				runtime.entered = make(chan struct{})
				runtime.release = make(chan struct{})
				runtime.fail = true
			}
			body := `{"name":"Changed","provider":"nordvpn","country":"Germany","expectedRevision":1,"affectedComputers":["alice"],"confirmRestart":true}`
			req, _ := http.NewRequest("PUT", ts.URL+"/api/vpn/connections/"+conn.ID, strings.NewReader(body))
			req.Header.Set("Origin", ts.URL)
			resp, err := admin.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var accepted struct{ Job string }
			json.NewDecoder(resp.Body).Decode(&accepted)
			resp.Body.Close()
			if resp.StatusCode != 202 {
				t.Fatalf("update status %d", resp.StatusCode)
			}
			j := s.jobs.get(accepted.Job)
			if postcommit {
				awaitRollout(t, runtime.entered)
			} else {
				awaitRollout(t, e.listed)
			}
			code := status(t, admin, "POST", ts.URL+"/api/jobs/"+accepted.Job+"/cancel", ts.URL)
			if postcommit {
				close(runtime.release)
			} else {
				close(e.stopRelease)
				if err = <-stopDone; err != nil {
					t.Fatal(err)
				}
			}
			awaitRollout(t, j.done)
			saved, _ := st.Get(conn.ID)
			if !postcommit {
				if code != 204 || saved.Revision != 1 || j.get().Error != "cancelled" || e.creates != 0 {
					t.Fatalf("cancel before commit: status=%d revision=%d creates=%d job=%+v", code, saved.Revision, e.creates, j.get())
				}
				got, _ := s.Computers.Get(context.Background(), "alice")
				if got.Container.ID != "original" || got.Settings.VPNConnectionID != conn.ID {
					t.Fatalf("desktop changed: %+v", got)
				}
			} else {
				if code != 409 || saved.Revision != 2 || j.get().Status != "done" {
					t.Fatalf("cancel after commit: status=%d revision=%d job=%+v", code, saved.Revision, j.get())
				}
				got, _ := s.Computers.Get(context.Background(), "alice")
				if got.VPN.State != "blocked" || got.VPN.DesiredRevision != 2 || got.Settings.VPNConnectionID != conn.ID {
					t.Fatalf("lost pending policy: %+v", got)
				}
				encoded, _ := json.Marshal(j.get())
				if !strings.Contains(string(encoded), "selected policy retained") {
					t.Fatalf("missing rollout failure: %s", encoded)
				}
				runtime.entered = nil
				runtime.fail = false
				if err = s.Computers.ConfigureNetworking(st, runtime, dir); err != nil {
					t.Fatal(err)
				}
				// Startup leaves the failed rollout; another edit retries it.
				if err = s.Computers.Networking.Reconcile(context.Background()); err != nil {
					t.Fatal(err)
				}
				if got, _ = s.Computers.Get(context.Background(), "alice"); got.VPN.AppliedRevision != 1 {
					t.Fatalf("startup retried a failed rollout: %+v", got.VPN)
				}
				var ids []string
				for _, ref := range s.Computers.Networking.References(conn.ID) {
					ids = append(ids, ref.ID)
				}
				if _, _, err = s.Computers.Networking.UpdateConnection(context.Background(), conn.ID, 2, ids, vpn.Input{Name: "Again", Provider: "nordvpn", Country: "Germany"}, func(string, float64) {}, nil); err != nil {
					t.Fatal(err)
				}
				got, _ = s.Computers.Get(context.Background(), "alice")
				if got.VPN.AppliedRevision != 3 || got.Container.NetworkContainer != "gateway" || got.Settings.VPNConnectionID != conn.ID {
					t.Fatalf("recovery lost selection: %+v", got)
				}
			}
		})
	}
}

// A container using the connection whose definition doesn't select it needs
// repair before the connection can change.
func TestVPNUpdateUnselectedReferenceRequiresRepair(t *testing.T) {
	e := &rolloutEngine{listed: make(chan struct{}, 1)}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	user, password := "user", "password"
	conn, err := s.Computers.Networking.Connections.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &user, Password: &password})
	if err != nil {
		t.Fatal(err)
	}
	e.consumer = &engine.Container{ID: "original", Name: "deskyard-alice", State: "exited", Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: "alice", computers.LabelRevision: "1", vpn.LabelConnection: conn.ID}}
	defineComputers(t, s)
	if _, err = s.Computers.Defs.Update("alice", func(d *computers.Definition) error {
		d.Network = computers.Network{Kind: computers.NetworkIsolated}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("PUT", ts.URL+"/api/vpn/connections/"+conn.ID, strings.NewReader(`{"name":"Changed","provider":"nordvpn","country":"Germany","expectedRevision":1,"affectedComputers":["alice"],"confirmRestart":true}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	saved, _ := s.Computers.Networking.Connections.Get(conn.ID)
	if resp.StatusCode != 409 || body["job"] != nil || !strings.Contains(body["error"].(string), "repair") || saved.Revision != 1 || e.creates != 0 {
		t.Fatalf("missing repair conflict: status=%d body=%v revision=%d", resp.StatusCode, body, saved.Revision)
	}
}
