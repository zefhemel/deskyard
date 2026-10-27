package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
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

// define defines (or redefines) the named environment as created then.
func define(t *testing.T, s *Server, name string, created time.Time) {
	t.Helper()
	def := environments.Definition{Name: name, Created: created.UTC(), Revision: 1, Image: environments.ImageRef{Ref: "img"},
		Network: environments.Network{Kind: environments.NetworkIsolated}, Power: environments.PowerStopped}
	if err := s.Environments.Defs.Put(def); err != nil {
		t.Fatal(err)
	}
}

func TestStartupDropsGrantsOfUndefinedEnvironments(t *testing.T) {
	s, _ := newTestServer(t)
	define(t, s, "carol", time.Now())
	if err := s.Accounts.SetAccess("carol", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	if err := s.Environments.Defs.Delete("carol"); err != nil {
		t.Fatal(err)
	}
	restarted := restart(t, s)
	a, _ := restarted.Accounts.Get("anna")
	if a.HasAccess("carol") || !a.HasAccess("alice") {
		t.Fatalf("grants after startup: %v", a.Grants)
	}
}

type failingCreateEngine struct{ fakeEngine }

func (failingCreateEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{ID: "sha256:img", Labels: map[string]string{environments.LabelContract: environments.Contract}}, nil
}
func (failingCreateEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return nil, errors.New("no networks in this test")
}

func TestCreateDropsLeftoverGrantsOfTheName(t *testing.T) {
	s, ts := newTestServerWith(t, failingCreateEngine{})
	define(t, s, "carol", time.Now())
	if err := s.Accounts.SetAccess("carol", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	// A removal that deleted the definition but not the grant.
	if err := s.Environments.Defs.Delete("carol"); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	req, _ := http.NewRequest("POST", ts.URL+"/api/environments", strings.NewReader(`{"name":"carol","image":"img"}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Job string }
	_ = json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	select { // the create itself fails: this engine has no networks
	case <-s.jobs.get(res.Job).done:
	case <-time.After(5 * time.Second):
		t.Fatal("create job timed out")
	}
	if a, _ := s.Accounts.Get("anna"); a.HasAccess("carol") {
		t.Fatal("new environment inherited the removed one's grant")
	}
}

// folderStuckEngine removes containers but can't run the helper that deletes
// a folder the supervisor itself can't.
type folderStuckEngine struct{ fakeEngine }

func (folderStuckEngine) Remove(context.Context, string) error { return nil }
func (folderStuckEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return nil, nil
}
func (folderStuckEngine) Pull(context.Context, string, func(engine.PullProgress)) error {
	return errors.New("no registry in this test")
}

// A removal that deletes the definition but then fails to delete the files
// still purges the grants and activity, closing the connections, and reports the removal.
func TestRemovalWhoseFolderDeletionFailsStillPurgesAccess(t *testing.T) {
	s, ts := newTestServerWith(t, folderStuckEngine{})
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	c, _ := issueCredential(t, s, accountNamed(t, s, "anna"))
	conn, cancel := context.WithCancel(context.Background())
	defer s.credentials.track(c.ID, "alice", cancel)()
	s.activity.Inside(activity.Ref{Name: "alice", ID: "alice"}, activity.Event{Agent: "terminal", Kind: "cmd-start", ID: "cmd", Cmd: "sleep", T: time.Now()})
	if len(s.activity.Present("alice")) == 0 {
		t.Fatal("test did not record activity")
	}
	sub := &subscriber{username: "admin", c: make(chan event, 16), refresh: make(chan struct{}, 1), overflow: make(chan struct{}, 1)}
	s.events.mu.Lock()
	s.events.subs[sub] = true
	s.events.mu.Unlock()
	// A folder the supervisor can't delete itself.
	home := s.Environments.Layout.HomeOf("alice")
	if err := os.WriteFile(filepath.Join(home, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	admin := login(t, ts, "admin")
	if code := status(t, admin, "DELETE", ts.URL+"/api/environments/alice?files=1", ts.URL); code < 400 {
		t.Fatalf("failed folder deletion reported %d", code)
	}
	if _, ok := s.Environments.Defs.Get("alice"); ok {
		t.Fatal("test did not delete the definition")
	}
	if a, _ := s.Accounts.Get("anna"); a.HasAccess("alice") {
		t.Error("removal kept the grant")
	}
	if conn.Err() == nil {
		t.Error("removal left a credential's connection open")
	}
	if len(s.activity.Present("alice")) != 0 {
		t.Error("removal kept the activity")
	}
	for removed := false; !removed; {
		select {
		case e := <-sub.c:
			removed = e.kind == "removed" && e.environment == "alice"
		default:
			t.Fatal("removal not published")
		}
	}
}

// A creation that fails but leaves the environment defined, to be recovered,
// still applies the requested access.
func TestCreateFailureKeepingTheEnvironmentAppliesAccess(t *testing.T) {
	e := &journalEngine{}
	s, ts := newTestServerWith(t, e)
	st := s.Environments.Networking.Connections
	u, p := "user", "password"
	conn, err := st.Create(vpn.Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: &u, Password: &p})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Environments.ConfigureNetworking(st, &rolloutRuntime{fail: true}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	req, _ := http.NewRequest("POST", ts.URL+"/api/environments", strings.NewReader(`{"name":"carol","image":"img","access":["anna"],"vpnConnectionId":"`+conn.ID+`"}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Job string }
	_ = json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	select {
	case <-s.jobs.get(res.Job).done:
	case <-time.After(5 * time.Second):
		t.Fatal("create job timed out")
	}
	if j := s.jobs.get(res.Job); j.state.Status != "failed" {
		t.Fatal("test's creation did not fail")
	}
	if _, ok := s.Environments.Defs.Get("carol"); !ok {
		t.Fatal("test's failed creation did not keep the definition")
	}
	if a, _ := s.Accounts.Get("anna"); !a.HasAccess("carol") {
		t.Error("environment kept for recovery lost its requested access")
	}
}

// trackCredential registers a live connection of a new credential of the
// account to the environment, as exec does.
func trackCredential(t *testing.T, s *Server, a accounts.Account, environment string) context.Context {
	t.Helper()
	c, _ := issueCredential(t, s, a)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(s.credentials.track(c.ID, environment, cancel))
	return ctx
}

func TestGrantRemovalClosesOnlyThatEnvironmentsConnections(t *testing.T) {
	s, _ := newTestServer(t)
	define(t, s, "bob", time.Now())
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("bob", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	anna := accountNamed(t, s, "anna")
	onAlice, onBob := trackCredential(t, s, anna, "alice"), trackCredential(t, s, anna, "bob")
	if err := s.Accounts.SetPassword("anna", "changed"); err != nil {
		t.Fatal(err)
	}
	if onAlice.Err() != nil || onBob.Err() != nil {
		t.Fatal("a password change closed client credential connections")
	}
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if onAlice.Err() == nil {
		t.Fatal("a removed grant left its connection open")
	}
	if onBob.Err() != nil {
		t.Fatal("removing one grant closed another environment's connection")
	}
}

func TestAdminKeepsCredentialConnectionsWhenRedundantGrantRemoved(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", fullAccess("admin")); err != nil {
		t.Fatal(err)
	}
	conn := trackCredential(t, s, accountNamed(t, s, "admin"), "alice")
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if conn.Err() != nil {
		t.Fatal("removing an admin's redundant grant closed their connection")
	}
}

func TestDemotionClosesCredentialConnections(t *testing.T) {
	s, ts := newTestServer(t)
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Admin = true }); err != nil {
		t.Fatal(err)
	}
	conn := trackCredential(t, s, accountNamed(t, s, "anna"), "alice")
	_, bearer := issueCredential(t, s, accountNamed(t, s, "anna"))
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Admin = false }); err != nil {
		t.Fatal(err)
	}
	if conn.Err() == nil {
		t.Fatal("a demotion left the credential's connection open")
	}
	if code, _, _ := bearerRequest(t, "GET", ts.URL+"/api/environments/alice/status", bearer); code != 404 {
		t.Fatalf("a demoted account's credential on alice: %d", code)
	}
}
