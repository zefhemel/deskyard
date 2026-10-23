package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestVPNAdminRoutesAndRedaction(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	user := login(t, ts, "anna")
	for _, route := range []struct{ method, path string }{{"GET", "/api/vpn/connections"}, {"POST", "/api/vpn/connections"}, {"PUT", "/api/vpn/connections/missing"}, {"DELETE", "/api/vpn/connections/missing"}, {"POST", "/api/vpn/test"}} {
		if got := status(t, user, route.method, ts.URL+route.path, ts.URL); got != 403 {
			t.Errorf("%s %s: got %d", route.method, route.path, got)
		}
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/vpn/connections", strings.NewReader(`{"name":"Europe","provider":"nordvpn","country":"Netherlands","username":"private-user","password":"private-password"}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || strings.Contains(string(body), "private-") {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var conn struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &conn)
	resp, err = admin.Get(ts.URL + "/api/vpn/connections")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "countries") || strings.Contains(string(body), "private-") {
		t.Fatalf("list: %s", body)
	}
	if got := status(t, admin, "DELETE", ts.URL+"/api/vpn/connections/"+conn.ID, ts.URL); got != 204 {
		t.Fatalf("delete: %d", got)
	}
}
func TestVPNMissingSelectionIsValidationError(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	for _, route := range []struct{ method, path, body string }{{"POST", "/api/desktops", `{"name":"test-vpn","vpnConnectionId":"missing"}`}, {"PATCH", "/api/desktops/alice", `{"vpnConnectionId":"missing"}`}} {
		req, _ := http.NewRequest(route.method, ts.URL+route.path, strings.NewReader(route.body))
		req.Header.Set("Origin", ts.URL)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("%s: %d %s", route.method, resp.StatusCode, body)
		}
	}
}
func TestVPNPatchDistinguishesNullAndOmission(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
		id   string
	}{{`{}`, false, ""}, {`{"vpnConnectionId":null}`, true, ""}, {`{"vpnConnectionId":"abc"}`, true, "abc"}} {
		var req struct {
			VPN optionalVPN `json:"vpnConnectionId"`
		}
		if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
			t.Fatal(err)
		}
		if req.VPN.Set != tc.want || req.VPN.ID != tc.id {
			t.Fatalf("%s: %+v", tc.raw, req)
		}
	}
}

type apiVPNTester struct{ fail bool }

func (f apiVPNTester) Test(context.Context, vpn.Input) (vpn.Status, error) {
	if f.fail {
		return vpn.Status{}, errors.New("provider-password-secret")
	}
	return vpn.Status{State: "connected", CurrentExitIP: "8.8.8.8"}, nil
}
func (f apiVPNTester) TestSaved(ctx context.Context, _ string, in vpn.Input) (vpn.Status, error) {
	return f.Test(ctx, in)
}
func TestVPNJobsAreSanitizedAndExposeResults(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	for _, fail := range []bool{true, false} {
		s.vpnTests = apiVPNTester{fail: fail}
		req, _ := http.NewRequest("POST", ts.URL+"/api/vpn/test", strings.NewReader(`{"name":"test","provider":"nordvpn","country":"Netherlands","username":"private-user","password":"private-password"}`))
		req.Header.Set("Origin", ts.URL)
		resp, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var accepted struct {
			Job string `json:"job"`
		}
		json.NewDecoder(resp.Body).Decode(&accepted)
		resp.Body.Close()
		if resp.StatusCode != 202 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		j := s.jobs.get(accepted.Job)
		select {
		case <-j.done:
		case <-time.After(time.Second):
			t.Fatal("job stalled")
		}
		st := j.get()
		encoded, _ := json.Marshal(st)
		if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "provider-password-secret") {
			t.Fatalf("secret in job %s", encoded)
		}
		if fail && st.Status != "failed" {
			t.Fatal(st)
		}
		if !fail && (st.Status != "done" || !strings.Contains(string(encoded), "8.8.8.8")) {
			t.Fatalf("missing result %s", encoded)
		}
	}
}
func TestVPNRegularUserCanReadSelectedStatus(t *testing.T) {
	c := alice
	c.Labels = map[string]string{desktops.LabelManaged: "true", desktops.LabelName: "bob", desktops.LabelID: "id-bob", vpn.LabelConnection: "selected-vpn"}
	c.ID = "bob"
	c.Name = "deskyard-bob"
	c.Env = map[string]string{desktops.TokenEnv: "proxy-secret"}
	s, ts := newTestServerWith(t, fakeEngine{others: []engine.Container{c}})
	s.Accounts.SetAccess("id-bob", []string{"anna"})
	user := login(t, ts, "anna")
	resp, err := user.Get(ts.URL + "/api/desktops/bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "selected-vpn") || !strings.Contains(string(body), "blocked") || strings.Contains(string(body), "proxy-secret") {
		t.Fatalf("public status %d %s", resp.StatusCode, body)
	}
}
