package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func blockedStatePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p, "state.json")
}
func TestCatalogFailedMutationKeepsPublishedState(t *testing.T) {
	for _, op := range []string{"put", "append", "remove", "newest", "local"} {
		t.Run(op, func(t *testing.T) {
			c, err := loadCatalog(t.TempDir(), "img")
			if err != nil {
				t.Fatal(err)
			}
			if err := c.put(catalogEntry{Ref: "second", Newest: "old"}); err != nil {
				t.Fatal(err)
			}
			before := append([]catalogEntry(nil), c.list...)
			originalPath := c.path
			disk, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			c.path = blockedStatePath(t)
			switch op {
			case "put":
				err = c.put(catalogEntry{Ref: "second", Newest: "new"})
			case "append":
				err = c.put(catalogEntry{Ref: "third"})
			case "remove":
				err = c.remove("second")
			case "newest":
				err = c.setNewest("second", "new")
			case "local":
				err = c.noteLocal("second", "new")
			}
			if err == nil {
				t.Fatal("expected write failure")
			}
			if !reflect.DeepEqual(c.list, before) {
				t.Fatal("failed mutation changed catalog")
			}
			got, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(disk) {
				t.Fatal("failed mutation changed disk")
			}
		})
	}
}
func TestTokenFailedRevokeKeepsConnectionsAndReloadState(t *testing.T) {
	dir := t.TempDir()
	ts, err := loadTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := ts.create("agent", "desktop", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, notified := false, false
	ts.track(tok.ID, func() { canceled = true })
	ts.onGone = func(token) { notified = true }
	original := ts.path
	ts.path = blockedStatePath(t)
	if err := ts.remove(func(token) bool { return true }); err == nil {
		t.Error("revocation must report persistence failure")
	}
	if _, ok := ts.check(bearer); !ok {
		t.Error("failed revocation removed live token")
	}
	if canceled || notified {
		t.Error("failed revocation closed/notified connections")
	}
	reloaded, err := loadTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.check(bearer); !ok {
		t.Error("failed revoke changed disk")
	}
	ts.path = original
	if err := ts.remove(func(token) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if !canceled || !notified {
		t.Error("successful revocation did not close/notify")
	}
	reloaded, err = loadTokens(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.check(bearer); ok {
		t.Error("revoked token resurrected")
	}
}

func TestTokenFailedCreateDoesNotPublish(t *testing.T) {
	ts, err := loadTokens(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts.path = blockedStatePath(t)
	tok, secret, err := ts.create("agent", "desktop", "anna", nil)
	if err == nil || secret != "" || tok.ID != "" {
		t.Fatalf("failed creation exposed token: %+v %q %v", tok, secret, err)
	}
	if len(ts.forEnvironment("desktop")) != 0 {
		t.Fatal("failed creation published token")
	}
}

func TestDeleteTokenReportsPersistenceFailure(t *testing.T) {
	bt := newCatalogTest(t)
	withEnvironment(t, bt, aliceHome)
	tok, bearer, err := bt.s.tokens.create("agent", "alice", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := false
	bt.s.tokens.track(tok.ID, func() { canceled = true })
	bt.s.tokens.path = blockedStatePath(t)
	code, body := bt.do(bt.admin, "DELETE", "/api/environments/alice/tokens/"+tok.ID, nil)
	if code != 500 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if canceled {
		t.Fatal("failed delete closed connection")
	}
	if _, ok := bt.s.tokens.check(bearer); !ok {
		t.Fatal("failed delete removed live token")
	}
}

func TestServerUsesVisibleLayout(t *testing.T) {
	s, _ := newTestServer(t)
	l := s.Layout
	for _, p := range []string{
		filepath.Join(l.Data, "accounts.json"),
		filepath.Join(l.Data, "images.json"),
		filepath.Join(l.Secret, "session.key"),
		filepath.Join(l.Secret, "vpn"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(l.Data, ".deskyard"), filepath.Join(l.Data, "vpn")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}
	if s.clients.dir != filepath.Join(l.Cache, "client") {
		t.Errorf("client cache in %s", s.clients.dir)
	}
	if got := s.Environments.Layout.HomeOf("alice"); got != filepath.Join(l.Data, "home", "alice") {
		t.Errorf("home in %s", got)
	}
}

func TestLoadTokensRestrictsWideFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tokens.json")
	if err := os.WriteFile(p, []byte(`{"tokens":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTokens(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}
