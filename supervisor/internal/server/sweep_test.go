package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// createShare makes a share of alice by sharer at level, with a login link
// and a client credential, as the Share flow and a CLI sign-in would.
func createShare(t *testing.T, s *Server, sharer accounts.Account, username string, level auth.Level) (accounts.Account, credential) {
	t.Helper()
	a, err := s.Accounts.Create(accounts.NewAccount{Username: username,
		Grants:   []accounts.Grant{{Environment: "alice", Level: level}},
		SharedBy: &accounts.Sharer{Username: sharer.Username, ID: sharer.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.links.issue(a, sharer.Username); err != nil {
		t.Fatal(err)
	}
	c, _ := issueCredential(t, s, a)
	return a, c
}

// shareGone fails the test unless the share, its link and its credentials
// are all gone.
func shareGone(t *testing.T, s *Server, share accounts.Account) {
	t.Helper()
	if _, ok := s.Accounts.Get(share.Username); ok {
		t.Fatalf("share %s still exists", share.Username)
	}
	if s.links.pending(share.ID) {
		t.Fatalf("share %s kept its link", share.Username)
	}
	if n := len(s.credentials.list(share.ID)); n != 0 {
		t.Fatalf("share %s kept %d credentials", share.Username, n)
	}
}

func TestShareEndsWhenSharerLosesAccess(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) {
		a.Grants = []accounts.Grant{{Environment: "alice", Level: auth.LevelFull}}
	}); err != nil {
		t.Fatal(err)
	}
	share, c := createShare(t, s, bob, "sanne", auth.LevelObserve)
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	admin, _ := s.Accounts.Get("admin")
	byAdmin, _ := createShare(t, s, admin, "carla", auth.LevelFull)

	// Still full: the share stays.
	s.sweep()
	if _, ok := s.Accounts.Get("sanne"); !ok {
		t.Fatal("the sweep removed a share whose sharer has full access")
	}
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) {
		a.Grants = []accounts.Grant{{Environment: "alice", Level: auth.LevelObserve}}
	}); err != nil {
		t.Fatal(err)
	}
	shareGone(t, s, share)
	if ctx.Err() == nil {
		t.Fatal("the ended share's connection stayed open")
	}
	if _, ok := s.Accounts.Get(byAdmin.Username); !ok {
		t.Fatal("an admin's share ended with another sharer's")
	}
}

func TestShareEndsWhenSharerDeleted(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) {
		a.Grants = []accounts.Grant{{Environment: "alice", Level: auth.LevelFull}}
	}); err != nil {
		t.Fatal(err)
	}
	share, _ := createShare(t, s, bob, "sanne", auth.LevelFull)
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	shareGone(t, s, share)

	// A recreated bob with full access is not the sharer of shares the old
	// one made.
	bob = createBob(t, s)
	share, _ = createShare(t, s, bob, "sanne", auth.LevelFull)
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "bob", Password: "pw",
		Grants: []accounts.Grant{{Environment: "alice", Level: auth.LevelFull}}}); err != nil {
		t.Fatal(err)
	}
	shareGone(t, s, share)
}

func TestShareEndsWhenEnvironmentRemoved(t *testing.T) {
	s, _ := newTestServer(t)
	admin, _ := s.Accounts.Get("admin")
	share, _ := createShare(t, s, admin, "sanne", auth.LevelFull)
	if err := s.environmentRemoved("alice"); err != nil {
		t.Fatal(err)
	}
	shareGone(t, s, share)
}

func TestSweepDropsLinksAndCredentialsOfRecreatedAccounts(t *testing.T) {
	s, _ := newTestServer(t)
	old := createBob(t, s)
	if _, err := s.links.issue(old, "admin"); err != nil {
		t.Fatal(err)
	}
	c, _ := issueCredential(t, s, old)
	// Recreated without the change notifications that run the sweep, as
	// before a restart.
	s.Accounts.OnChange = nil
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	createBob(t, s)
	s.Accounts.OnChange = s.accountsChanged
	s.sweep()
	if s.links.pending(old.ID) {
		t.Fatal("kept the link of a recreated account")
	}
	if len(s.credentials.list(c.AccountID)) != 0 {
		t.Fatal("kept the credential of a recreated account")
	}
}

func TestSweepClosesCredentialConnectionsTheLevelNoLongerAllows(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob")); err != nil {
		t.Fatal(err)
	}
	c, _ := issueCredential(t, s, bob)
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("an exec connection stayed open after a downgrade to observe")
	}
	if len(s.credentials.list(bob.ID)) != 1 {
		t.Fatal("a downgrade deleted the account's credential")
	}
}

func TestExpiredShareDeletedAndConnectionsClosed(t *testing.T) {
	s, _ := newTestServer(t)
	admin, _ := s.Accounts.Get("admin")
	share, c := createShare(t, s, admin, "sanne", auth.LevelFull)
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	stream, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	cn := &conn{username: "sanne", environment: "alice", level: auth.LevelFull, cancel: cancelStream}
	s.viewers.add("alice", cn)
	defer s.viewers.remove("alice", cn)
	expire(t, s, "sanne")
	if ctx.Err() != nil || stream.Err() != nil {
		t.Fatal("the connections closed before the expiry tick")
	}
	s.expireAccounts(time.Now())
	shareGone(t, s, share)
	if ctx.Err() == nil || stream.Err() == nil {
		t.Fatalf("an expired share's connections stayed open: exec %v, stream %v", ctx.Err(), stream.Err())
	}
}

func TestExpiryClosesConnectionsWhenTheDeletionFails(t *testing.T) {
	s, _ := newTestServer(t)
	admin, _ := s.Accounts.Get("admin")
	_, c := createShare(t, s, admin, "sanne", auth.LevelFull)
	ctx, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	stream, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	cn := &conn{username: "sanne", environment: "alice", level: auth.LevelFull, cancel: cancelStream}
	s.viewers.add("alice", cn)
	defer s.viewers.remove("alice", cn)
	expire(t, s, "sanne")
	blockAccountsFile(t, s)
	if ctx.Err() != nil || stream.Err() != nil {
		t.Fatal("the connections closed before the expiry tick")
	}
	s.expireAccounts(time.Now())
	if _, ok := s.Accounts.Get("sanne"); !ok {
		t.Fatal("deleted the expired account although writing accounts.json fails")
	}
	if ctx.Err() == nil || stream.Err() == nil {
		t.Fatalf("an expired account's connections stayed open when its deletion failed: exec %v, stream %v", ctx.Err(), stream.Err())
	}
}

// expire makes the account expired without the change notification, as
// time passing does.
func expire(t *testing.T, s *Server, username string) {
	t.Helper()
	s.Accounts.OnChange = nil
	defer func() { s.Accounts.OnChange = s.accountsChanged }()
	past := time.Now().Add(-time.Minute)
	if _, err := s.Accounts.Update(username, func(a *accounts.Account) { a.Expires = &past }); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryEndsEventStreamWhenTheDeletionFails(t *testing.T) {
	s, ts := newTestServer(t)
	createBob(t, s)
	if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	bob := login(t, ts, "bob")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/events",
		&websocket.DialOptions{HTTPClient: bob, HTTPHeader: http.Header{"Origin": {ts.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if _, _, err := ws.Read(ctx); err != nil { // the snapshot
		t.Fatal(err)
	}
	expire(t, s, "bob")
	blockAccountsFile(t, s)
	s.expireAccounts(time.Now())
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("an expired account's event stream stayed open when its deletion failed")
			}
			return
		}
	}
}

func TestOrphanedShareRefusedWhenTheDeletionFails(t *testing.T) {
	s, ts := newTestServer(t)
	bob := createBob(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob")); err != nil {
		t.Fatal(err)
	}
	share, _ := createShare(t, s, bob, "sanne", auth.LevelFull)
	_, bearer := issueCredential(t, s, share)
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice", bearer); code != 200 {
		t.Fatalf("the share before its sharer lost access: %d", code)
	}
	// bob loses full access, but deleting his share fails.
	s.Accounts.OnChange = nil
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) {
		a.Grants = []accounts.Grant{{Environment: "alice", Level: auth.LevelObserve}}
	}); err != nil {
		t.Fatal(err)
	}
	s.Accounts.OnChange = s.accountsChanged
	blockAccountsFile(t, s)
	s.sweep()
	if _, ok := s.Accounts.Get("sanne"); !ok {
		t.Fatal("deleted the share although writing accounts.json fails")
	}
	for _, path := range []string{"/api/me", "/api/environments/alice"} {
		if code, _, _ := bearerRequest(t, "GET", ts.URL+path, bearer); code != http.StatusUnauthorized {
			t.Errorf("the orphaned share's GET %s: %d", path, code)
		}
	}
}

// blockAccountsFile makes every later write of accounts.json fail.
func blockAccountsFile(t *testing.T, s *Server) {
	t.Helper()
	p := filepath.Join(s.Layout.Data, "accounts.json")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDowngradeClosesViewerStream(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob")); err != nil {
		t.Fatal(err)
	}
	bob, _ = s.Accounts.Get("bob")
	term, cancelTerm := context.WithCancel(context.Background())
	defer cancelTerm()
	tc := &terminalConnection{environment: "alice", session: "main", cancel: cancelTerm,
		claims: auth.SessionClaims{Username: "bob", AccountID: bob.ID, Epoch: bob.SessionEpoch, ID: "sess-1", Expires: time.Now().Add(time.Hour)}}
	if !s.terminals.add(tc) {
		t.Fatal("the terminal wasn't registered")
	}
	defer s.terminals.remove(tc)
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) { a.Description = "colleague" }); err != nil {
		t.Fatal(err)
	}
	if term.Err() != nil {
		t.Fatal("an unrelated change closed a full user's terminal")
	}
	full, cancelFull := context.WithCancel(context.Background())
	defer cancelFull()
	stream := &conn{username: "bob", environment: "alice", level: auth.LevelFull, cancel: cancelFull}
	s.viewers.add("alice", stream)
	defer s.viewers.remove("alice", stream)
	watch, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	watcher := &conn{username: "bob", environment: "alice", level: auth.LevelFull, cancel: cancelWatch}
	s.screenWatchers.add("alice", watcher)
	defer s.screenWatchers.remove("alice", watcher)
	if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	if full.Err() == nil || watch.Err() == nil || term.Err() == nil {
		t.Fatalf("a downgrade left full-level connections open: stream %v, screen events %v, terminal %v", full.Err(), watch.Err(), term.Err())
	}

	// An observer's stream at observe level stays open on an unrelated change.
	observing, cancelObserving := context.WithCancel(context.Background())
	defer cancelObserving()
	obs := &conn{username: "bob", environment: "alice", level: auth.LevelObserve, cancel: cancelObserving}
	s.viewers.add("alice", obs)
	defer s.viewers.remove("alice", obs)
	if _, err := s.Accounts.Update("bob", func(a *accounts.Account) { a.Description = "a colleague" }); err != nil {
		t.Fatal(err)
	}
	if observing.Err() != nil {
		t.Fatal("an unrelated change closed an observer's stream")
	}
	if got := s.viewers.observers("alice"); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("observers %v", got)
	}
}

func TestTokensJSONRemovedAtStartup(t *testing.T) {
	s, _ := newTestServer(t)
	p := filepath.Join(s.Layout.Data, "tokens.json")
	if err := os.WriteFile(p, []byte(`{"tokens":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	restart(t, s)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("tokens.json after startup: %v", err)
	}
}
