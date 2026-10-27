package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
)

// bearerRequest sends a request with the bearer credential and no Origin.
func bearerRequest(t *testing.T, method, url, bearer string) (int, string, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp
}

func createBob(t *testing.T, s *Server) accounts.Account {
	t.Helper()
	a, err := s.Accounts.Create(accounts.NewAccount{Username: "bob", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func issueCredential(t *testing.T, s *Server, a accounts.Account) (credential, string) {
	t.Helper()
	c, bearer, err := s.credentials.create(a, "cli", "deskyard CLI on test")
	if err != nil {
		t.Fatal(err)
	}
	return c, bearer
}

func TestBearerCredentialReachesAPI(t *testing.T) {
	s, ts := newTestServer(t)
	c, bearer := issueCredential(t, s, createBob(t, s))
	if !strings.HasPrefix(bearer, "dyc_"+c.ID+"_") || len(c.ID) != 8 {
		t.Fatalf("bearer %q for id %q", bearer, c.ID)
	}
	code, body, resp := bearerRequest(t, "GET", ts.URL+"/api/me", bearer)
	var me meJSON
	_ = json.Unmarshal([]byte(body), &me)
	if code != 200 || me.Username != "bob" {
		t.Fatalf("GET /api/me: %d %s", code, body)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("bearer request set cookies: %v", resp.Cookies())
	}
	// No Origin: bearer requests skip the same-origin check, and routes that
	// re-issue a browser session don't set a cookie.
	code, body, resp = bearerRequest(t, "POST", ts.URL+"/api/me/sign-out-everywhere", bearer)
	if code != 204 || len(resp.Cookies()) != 0 {
		t.Fatalf("POST without Origin: %d %s %v", code, body, resp.Cookies())
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != 200 {
		t.Fatalf("sign out everywhere ended the credential: %d", code)
	}
	got, ok := s.credentials.check(bearer)
	if !ok || got.LastUsed == nil {
		t.Fatalf("last use not recorded: %+v", got)
	}
}

func TestBadBearerRejected(t *testing.T) {
	s, ts := newTestServer(t)
	c, bearer := issueCredential(t, s, createBob(t, s))
	anna := login(t, ts, "anna")
	for _, b := range []string{"", "nonsense", "dyc_" + c.ID + "_wrong", "dyc_" + c.ID, "dyl_" + strings.TrimPrefix(bearer, "dyc_")} {
		code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/me", b)
		if code != 401 || !strings.Contains(body, "the credential is unknown, revoked or expired") {
			t.Errorf("%q: %d %s", b, code, body)
		}
		// A cookie sent along doesn't rescue a bad bearer.
		req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+b)
		resp, err := anna.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("%q with a cookie: %d", b, resp.StatusCode)
		}
	}
}

func TestRevokedCredentialRejected(t *testing.T) {
	s, ts := newTestServer(t)
	c, bearer := issueCredential(t, s, createBob(t, s))
	if err := s.credentials.remove(func(o credential) bool { return o.ID == c.ID }); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != 401 {
		t.Fatalf("revoked credential: %d %s", code, body)
	}
}

func TestCredentialOfRecreatedAccountRejected(t *testing.T) {
	s, ts := newTestServer(t)
	_, bearer := issueCredential(t, s, createBob(t, s))
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != 401 {
		t.Fatalf("credential of deleted bob: %d", code)
	}
	createBob(t, s)
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != 401 {
		t.Fatalf("credential of the earlier bob works for the recreated one: %d", code)
	}
}

func TestCredentialOfExpiredAccountRejected(t *testing.T) {
	s, ts := newTestServer(t)
	_, bearer := issueCredential(t, s, createBob(t, s))
	past := time.Now().Add(-time.Hour)
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) { a.Expires = &past }); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != 401 {
		t.Fatalf("credential of expired bob: %d", code)
	}
}

func TestCredentialsPersistAcrossRestart(t *testing.T) {
	s, _ := newTestServer(t)
	c, bearer := issueCredential(t, s, createBob(t, s))
	st, err := os.Stat(s.credentials.path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json: %v %v", st, err)
	}
	if b, _ := os.ReadFile(s.credentials.path); strings.Contains(string(b), strings.TrimPrefix(bearer, "dyc_"+c.ID+"_")) {
		t.Fatal("credentials.json holds the secret")
	}
	restarted := httptest.NewServer(restart(t, s))
	t.Cleanup(restarted.Close)
	code, body, _ := bearerRequest(t, "GET", restarted.URL+"/api/me", bearer)
	if code != 200 || !strings.Contains(body, `"bob"`) {
		t.Fatalf("credential after restart: %d %s", code, body)
	}
	list := restart(t, s).credentials.list("")
	if len(list) != 1 || list[0].ID != c.ID || list[0].Name != "deskyard CLI on test" || list[0].Kind != "cli" || list[0].Username != "bob" {
		t.Fatalf("persisted credentials: %+v", list)
	}
}

func TestCredentialListByAccount(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	anna, _ := s.Accounts.Get("anna")
	first, _ := issueCredential(t, s, bob)
	issueCredential(t, s, anna)
	second, _ := issueCredential(t, s, bob)
	got := s.credentials.list(bob.ID)
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("bob's credentials, newest first: %+v", got)
	}
	if all := s.credentials.list(""); len(all) != 3 {
		t.Fatalf("all credentials: %d", len(all))
	}
}

func TestCredentialRemoveClosesTrackedConnections(t *testing.T) {
	s, _ := newTestServer(t)
	c, _ := issueCredential(t, s, createBob(t, s))
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	other, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	d, _ := issueCredential(t, s, createAccount(t, s, "carol"))
	defer s.credentials.track(d.ID, "alice", otherCancel)()
	if err := s.credentials.remove(func(o credential) bool { return o.ID == c.ID }); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("removing the credential left its connection open")
	}
	if other.Err() != nil {
		t.Fatal("removing one credential closed another's connection")
	}

	// A failed write still closes live access, and reports the error.
	ctx, cancel = context.WithCancel(context.Background())
	defer s.credentials.track(d.ID, "alice", cancel)()
	s.credentials.path = blockedStatePath(t)
	if err := s.credentials.remove(func(o credential) bool { return o.ID == d.ID }); err == nil {
		t.Fatal("remove reported success without persisting")
	}
	if ctx.Err() == nil {
		t.Fatal("a failed write left the connection open")
	}
}

func TestCredentialCloseWhere(t *testing.T) {
	s, _ := newTestServer(t)
	c, _ := issueCredential(t, s, createBob(t, s))
	onAlice, cancelAlice := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancelAlice)()
	onBeta, cancelBeta := context.WithCancel(context.Background())
	defer cancelBeta()
	defer s.credentials.track(c.ID, "beta", cancelBeta)()
	s.credentials.closeWhere(c.ID, func(env string) bool { return env == "alice" })
	if onAlice.Err() == nil || onBeta.Err() != nil {
		t.Fatalf("closeWhere alice: alice %v, beta %v", onAlice.Err(), onBeta.Err())
	}
}

func TestCredentialRetain(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	keep, _ := issueCredential(t, s, bob)
	drop, _ := issueCredential(t, s, createAccount(t, s, "carol"))
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(drop.ID, "alice", cancel)()
	if err := s.credentials.retain(func(c credential) bool { return c.AccountID == bob.ID }); err != nil {
		t.Fatal(err)
	}
	if got := restart(t, s).credentials.list(""); len(got) != 1 || got[0].ID != keep.ID {
		t.Fatalf("retained: %+v", got)
	}
	if ctx.Err() == nil {
		t.Fatal("retain left a dropped credential's connection open")
	}
}

func createAccount(t *testing.T, s *Server, username string) accounts.Account {
	t.Helper()
	a, err := s.Accounts.Create(accounts.NewAccount{Username: username, Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
