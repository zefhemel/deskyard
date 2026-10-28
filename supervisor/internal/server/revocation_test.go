package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// unblockAccountsFile undoes blockAccountsFile.
func unblockAccountsFile(t *testing.T, s *Server) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(s.Layout.Data, "accounts.json")); err != nil {
		t.Fatal(err)
	}
}

// savedAccount is the named account as accounts.json has it.
func savedAccount(t *testing.T, s *Server, username string) (accounts.Account, bool) {
	t.Helper()
	saved, err := accounts.Load(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	return saved.Get(username)
}

// requireUnsaved fails the test unless the accounts hold a revocation they
// couldn't save.
func requireUnsaved(t *testing.T, s *Server) {
	t.Helper()
	if !s.Accounts.Unsaved() {
		t.Fatal("the test's change was saved")
	}
}

// A delete that can't be saved still ends the account: its cookie, its
// credentials and its password are refused until the sweep saves it.
func TestUnsavedDeleteKeepsTheAccountOut(t *testing.T) {
	s, ts := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	anna := login(t, ts, "anna")
	_, bearer := issueCredential(t, s, accountNamed(t, s, "anna"))
	conn := trackCredential(t, s, accountNamed(t, s, "anna"), "alice")
	admin := login(t, ts, "admin")
	blockAccountsFile(t, s)
	if code := status(t, admin, "DELETE", ts.URL+"/api/accounts/anna", ts.URL); code != http.StatusInternalServerError {
		t.Fatalf("delete: %d", code)
	}
	requireUnsaved(t, s)
	if conn.Err() == nil {
		t.Error("the connection stayed open")
	}
	if code := status(t, anna, "GET", ts.URL+"/api/me", ""); code != http.StatusUnauthorized {
		t.Errorf("cookie after the delete: %d", code)
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/me", bearer); code != http.StatusUnauthorized {
		t.Errorf("credential after the delete: %d", code)
	}
	if code, _ := send(t, &http.Client{}, "POST", ts.URL+"/api/login", ts.URL, `{"username":"anna","password":"pw"}`); code != http.StatusUnauthorized {
		t.Errorf("password after the delete: %d", code)
	}
	unblockAccountsFile(t, s)
	s.sweep()
	if _, ok := savedAccount(t, s, "anna"); ok || s.Accounts.Unsaved() {
		t.Error("the sweep didn't save the delete")
	}
}

// A grant removal or downgrade that can't be saved still closes what the
// new level doesn't allow and holds until it is saved, from either editor.
func TestUnsavedGrantChangesHold(t *testing.T) {
	for _, tc := range []struct{ name, method, path, body string }{
		{"access tab", "PUT", "/api/environments/alice/access", `{"grants":[{"username":"anna","level":"observe"}]}`},
		{"account dialog", "PATCH", "/api/accounts/anna", `{"grants":[{"environment":"alice","level":"observe"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ts := newTestServer(t)
			if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
				t.Fatal(err)
			}
			_, bearer := issueCredential(t, s, accountNamed(t, s, "anna"))
			conn := trackCredential(t, s, accountNamed(t, s, "anna"), "alice")
			admin := login(t, ts, "admin")
			blockAccountsFile(t, s)
			if code, body := send(t, admin, tc.method, ts.URL+tc.path, ts.URL, tc.body); code != http.StatusInternalServerError {
				t.Fatalf("change: %d %s", code, body)
			}
			requireUnsaved(t, s)
			if conn.Err() == nil {
				t.Error("the full connection stayed open")
			}
			if a, ok := s.Accounts.Usable("anna", accountNamed(t, s, "anna").ID, time.Now()); !ok || a.Level("alice") != auth.LevelObserve {
				t.Errorf("anna now: %v %v", ok, a.Grants)
			}
			if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/processes", bearer); code != http.StatusForbidden {
				t.Errorf("full access after the downgrade: %d", code)
			}
			unblockAccountsFile(t, s)
			s.sweep()
			if a, _ := savedAccount(t, s, "anna"); a.Level("alice") != auth.LevelObserve {
				t.Errorf("the sweep didn't save the downgrade: %v", a.Grants)
			}
			// A later grant decides anew.
			if code, body := send(t, admin, "PUT", ts.URL+"/api/environments/alice/access", ts.URL, `{"grants":[{"username":"anna","level":"full"}]}`); code != http.StatusNoContent {
				t.Fatalf("upgrade: %d %s", code, body)
			}
			s.sweep()
			if a, _ := s.Accounts.Usable("anna", accountNamed(t, s, "anna").ID, time.Now()); a.Level("alice") != auth.LevelFull {
				t.Errorf("the upgrade didn't hold: %v", a.Grants)
			}
		})
	}
}

// A change refused for another reason than saving keeps what an earlier
// one that couldn't be saved revoked.
func TestRefusedChangeKeepsUnsavedRevocation(t *testing.T) {
	s, ts := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	blockAccountsFile(t, s)
	if code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/anna", ts.URL, `{"grants":[]}`); code != http.StatusInternalServerError {
		t.Fatalf("removal: %d %s", code, body)
	}
	unblockAccountsFile(t, s)
	if code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/anna", ts.URL,
		`{"grants":[{"environment":"alice","level":"full"},{"environment":"alice","level":"full"}]}`); code != http.StatusBadRequest {
		t.Fatalf("refused change: %d %s", code, body)
	}
	if a, _ := s.Accounts.Usable("anna", accountNamed(t, s, "anna").ID, time.Now()); a.Level("alice") != auth.LevelNone {
		t.Errorf("a refused change restored the revoked access: %v", a.Grants)
	}
}

// A change that can't be saved either keeps what an earlier unsaved one
// revoked, also when it asks for the access back.
func TestUnsavedAccessChangeKeepsEarlierRevocation(t *testing.T) {
	s, ts := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	blockAccountsFile(t, s)
	for _, level := range []string{"observe", "full"} {
		if code, body := send(t, admin, "PUT", ts.URL+"/api/environments/alice/access", ts.URL, `{"grants":[{"username":"anna","level":"`+level+`"}]}`); code != http.StatusInternalServerError {
			t.Fatalf("%s: %d %s", level, code, body)
		}
	}
	if a, _ := s.Accounts.Usable("anna", accountNamed(t, s, "anna").ID, time.Now()); a.Level("alice") != auth.LevelObserve {
		t.Errorf("anna now: %v", a.Grants)
	}
}

// The Accounts page and the Access tab show access as it stands, so saving
// what they show keeps a revocation that couldn't be saved.
func TestEditorsShowUnsavedRevocations(t *testing.T) {
	s, ts := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	blockAccountsFile(t, s)
	if code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/anna", ts.URL, `{"grants":[{"environment":"alice","level":"observe"}]}`); code != http.StatusInternalServerError {
		t.Fatalf("downgrade: %d %s", code, body)
	}
	unblockAccountsFile(t, s)
	var list []accountJSON
	if code := getJSON(t, admin, ts.URL+"/api/accounts", &list); code != 200 {
		t.Fatal(code)
	}
	for _, a := range list {
		if a.Username == "anna" && (len(a.Grants) != 1 || a.Grants[0].Level != auth.LevelObserve) {
			t.Errorf("the Accounts page shows anna with %v", a.Grants)
		}
	}
	var env struct{ Access []accessGrant }
	if code := getJSON(t, admin, ts.URL+"/api/environments/alice", &env); code != 200 {
		t.Fatal(code)
	}
	if len(env.Access) != 1 || env.Access[0].Level != auth.LevelObserve {
		t.Errorf("the Access tab shows %v", env.Access)
	}
}

// Signing out everywhere ends the other sessions although the new session
// epoch can't be saved, and keeps this browser signed in.
func TestUnsavedSignOutEverywhereEndsSessions(t *testing.T) {
	s, ts := newTestServer(t)
	first, second := login(t, ts, "anna"), login(t, ts, "anna")
	blockAccountsFile(t, s)
	if code, body := send(t, first, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL, ""); code != http.StatusInternalServerError {
		t.Fatalf("sign out everywhere: %d %s", code, body)
	}
	if code := status(t, second, "GET", ts.URL+"/api/me", ""); code != http.StatusUnauthorized {
		t.Errorf("the other browser after signing out everywhere: %d", code)
	}
	if code := status(t, first, "GET", ts.URL+"/api/me", ""); code != http.StatusOK {
		t.Errorf("this browser after signing out everywhere: %d", code)
	}
	unblockAccountsFile(t, s)
	s.sweep()
	if s.Accounts.Unsaved() {
		t.Error("the sweep didn't save the sign-out")
	}
	if code := status(t, second, "GET", ts.URL+"/api/me", ""); code != http.StatusUnauthorized {
		t.Errorf("the other browser after the sign-out was saved: %d", code)
	}
}

// A later sign-out everywhere, or a password or admin change, starts from
// the session epoch held unsaved: sessions begun under it end as well.
func TestSessionEpochBumpsStartFromTheHeldEpoch(t *testing.T) {
	for _, change := range []string{"sign out everywhere", "own password", "admin password", "admin role"} {
		t.Run(change, func(t *testing.T) {
			s, ts := newTestServer(t)
			admin, first := login(t, ts, "admin"), login(t, ts, "anna")
			blockAccountsFile(t, s)
			if code, body := send(t, first, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL, ""); code != http.StatusInternalServerError {
				t.Fatalf("sign out everywhere: %d %s", code, body)
			}
			// Signed in after it, under the held epoch.
			later := login(t, ts, "anna")
			unblockAccountsFile(t, s)
			var code int
			var body string
			switch change {
			case "sign out everywhere":
				code, body = send(t, first, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL, "")
			case "own password":
				code, body = send(t, first, "POST", ts.URL+"/api/me/password", ts.URL, `{"current":"pw","new":"pw2"}`)
			case "admin password":
				code, body = send(t, admin, "PATCH", ts.URL+"/api/accounts/anna", ts.URL, `{"password":"pw2"}`)
			case "admin role":
				code, body = send(t, admin, "PATCH", ts.URL+"/api/accounts/anna", ts.URL, `{"admin":true}`)
			}
			if code >= 300 {
				t.Fatalf("%s: %d %s", change, code, body)
			}
			s.sweep()
			if code := status(t, later, "GET", ts.URL+"/api/me", ""); code != http.StatusUnauthorized {
				t.Errorf("a session begun under the held epoch after %s: %d", change, code)
			}
		})
	}
}
