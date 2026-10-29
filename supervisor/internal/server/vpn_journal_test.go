package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

type journalEngine struct{ rolloutEngine }

func (*journalEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{ID: "image", Labels: map[string]string{computers.LabelContract: computers.Contract}}, nil
}

func TestVPNJournalErrorsAreSafeInPowerHTTPAndDesktopJobs(t *testing.T) {
	e := &journalEngine{}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	st := s.Computers.Networking.Connections
	u, p := "user", "password"
	conn, err := st.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	e.consumer = &engine.Container{ID: "consumer", Name: "deskyard-alice", State: "exited", Labels: map[string]string{computers.LabelManaged: "true", computers.LabelName: "alice", computers.LabelRevision: "1", vpn.LabelConnection: conn.ID}}
	defineComputers(t, s)
	dir := filepath.Join(t.TempDir(), "private-state-sentinel")
	path := filepath.Join(dir, "network", "transitions.json")
	record := map[string]any{"name": "alice", "connectionRevision": 1, "appliedConnectionRevision": 1, "appliedRevision": 1, "runtimeId": "gateway"}
	if err = store.WriteJSON(path, map[string]any{"version": 1, "transitions": []any{record}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Computers.ConfigureNetworking(st, &rolloutRuntime{}, dir); err != nil {
		t.Fatal(err)
	}
	// A real filesystem failure, after loading a valid durable record. Keep
	// connections in their own healthy store so validation reaches the journal.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body string
		job                      bool
	}{
		{"power", "POST", "/api/environments/alice/start", "", false},
		{"create", "POST", "/api/environments", `{"name":"new-desktop","image":"img","vpnConnectionId":"` + conn.ID + `"}`, true},
		{"recreate", "PATCH", "/api/environments/alice", `{"vpnConnectionId":null}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Origin", ts.URL)
			resp, err := admin.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if tc.job {
				if resp.StatusCode != 202 {
					t.Fatalf("job not accepted: %d %s", resp.StatusCode, body)
				}
				var accepted struct{ Job string }
				if err = json.Unmarshal(body, &accepted); err != nil {
					t.Fatal(err)
				}
				select {
				case <-s.jobs.get(accepted.Job).done:
				case <-time.After(3 * time.Second):
					t.Fatal("job stalled")
				}
				resp, err = admin.Get(ts.URL + "/api/jobs/" + accepted.Job)
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
				if !strings.Contains(string(body), `"status":"failed"`) {
					t.Fatalf("write failure reported success: %s", body)
				}
			} else if resp.StatusCode < 400 {
				t.Fatalf("write failure reported %d: %s", resp.StatusCode, body)
			}
			for _, private := range []string{dir, "private-state-sentinel", "transitions.json", "not a directory", "private-token"} {
				if strings.Contains(string(body), private) {
					t.Errorf("private cause in public response: %s", body)
				}
			}
			got, err := s.Computers.Get(context.Background(), "alice")
			if err != nil || got.Settings.VPNConnectionID != conn.ID {
				t.Fatalf("failed persistence changed selected policy: %+v %v", got.VPN, err)
			}
		})
	}
}
