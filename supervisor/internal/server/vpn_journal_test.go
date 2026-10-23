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

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

type journalEngine struct{ rolloutEngine }

func (*journalEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{ID: "image", Labels: map[string]string{desktops.LabelContract: desktops.Contract}}, nil
}

func TestVPNJournalErrorsAreSafeInPowerHTTPAndDesktopJobs(t *testing.T) {
	e := &journalEngine{}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	st := s.Desktops.Networking.Connections
	u, p := "user", "password"
	conn, err := st.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	const desktopID = "012345678901234567890123456789ab"
	e.consumer = &engine.Container{ID: "consumer", Name: "deskyard-alice", State: "exited", Labels: map[string]string{desktops.LabelManaged: "true", desktops.LabelName: "alice", desktops.LabelID: desktopID}}
	dir := filepath.Join(t.TempDir(), "private-state-sentinel")
	path := filepath.Join(dir, "vpn", "desktops.json")
	record := map[string]any{"id": desktopID, "name": "alice", "token": "private-token", "created": time.Now(), "settings": desktops.Settings{Image: "img", VPNConnectionID: conn.ID}, "running": true, "revision": 1, "appliedRevision": 1, "runtimeId": "gateway"}
	if err = store.WriteJSON(path, map[string]any{desktopID: record}); err != nil {
		t.Fatal(err)
	}
	if err = s.Desktops.ConfigureNetworking(st, &rolloutRuntime{}, dir); err != nil {
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
		{"power", "POST", "/api/desktops/alice/stop", "", false},
		{"create", "POST", "/api/desktops", `{"name":"new-desktop","image":"img","vpnConnectionId":"` + conn.ID + `"}`, true},
		{"recreate", "PATCH", "/api/desktops/alice", `{"vpnConnectionId":null}`, true},
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
			for _, private := range []string{dir, "private-state-sentinel", "desktops.json", "not a directory", "private-token"} {
				if strings.Contains(string(body), private) {
					t.Errorf("private cause in public response: %s", body)
				}
			}
			got, err := s.Desktops.Get(context.Background(), "alice")
			if err != nil || got.Settings.VPNConnectionID != conn.ID {
				t.Fatalf("failed persistence changed selected policy: %+v %v", got.VPN, err)
			}
		})
	}
}
