package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// unblockAccountsFile undoes blockAccountsFile.
func unblockAccountsFile(t *testing.T, s *Server) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(s.Layout.Data, "accounts.json")); err != nil {
		t.Fatal(err)
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
	if _, ok := s.Accounts.Get("anna"); !ok {
		t.Fatal("test's delete was saved")
	}
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
	if _, ok := s.Accounts.Get("anna"); ok {
		t.Error("the sweep didn't save the delete")
	}
	if len(s.unsaved.list()) != 0 {
		t.Error("the saved delete is still held")
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
			if a := accountNamed(t, s, "anna"); a.Level("alice") != auth.LevelFull {
				t.Fatal("test's change was saved")
			}
			if conn.Err() == nil {
				t.Error("the full connection stayed open")
			}
			if a, ok := s.accountNow("anna", accountNamed(t, s, "anna").ID, time.Now()); !ok || a.Level("alice") != auth.LevelObserve {
				t.Errorf("anna now: %v %v", ok, a.Grants)
			}
			if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/processes", bearer); code != http.StatusForbidden {
				t.Errorf("full access after the downgrade: %d", code)
			}
			unblockAccountsFile(t, s)
			s.sweep()
			if a := accountNamed(t, s, "anna"); a.Level("alice") != auth.LevelObserve {
				t.Errorf("the sweep didn't save the downgrade: %v", a.Grants)
			}
			// A later grant decides anew.
			if code, body := send(t, admin, "PUT", ts.URL+"/api/environments/alice/access", ts.URL, `{"grants":[{"username":"anna","level":"full"}]}`); code != http.StatusNoContent {
				t.Fatalf("upgrade: %d %s", code, body)
			}
			s.sweep()
			if a, _ := s.accountNow("anna", accountNamed(t, s, "anna").ID, time.Now()); a.Level("alice") != auth.LevelFull {
				t.Errorf("the upgrade didn't hold: %v", a.Grants)
			}
		})
	}
}

