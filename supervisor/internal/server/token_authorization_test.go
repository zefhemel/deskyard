package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func agentRequestCode(t *testing.T, s *Server, bearer string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, agentPrefix+"/", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	s.serveAgent(w, req)
	return w.Code
}

func TestAgentAccessCleanupFailureCannotReviveToken(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := false
	s.tokens.track(tok.ID, func() { canceled = true })
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Error("account access loss kept token connection open")
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Errorf("access loss still authorized: %d", code)
	}
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Errorf("regrant revived token: %d", code)
	}
	persistedAccounts, err := accounts.Load(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Options{Log: s.Log, Eng: s.Eng, Environments: s.Environments, Accounts: persistedAccounts, Layout: s.Layout, DefaultImage: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, restarted, bearer); code != http.StatusUnauthorized {
		t.Errorf("restart revived token: %d", code)
	}
}

func TestAgentDeletedCreatorCannotAuthorizeRecreatedAccount(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.Delete("anna"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.Create("anna", "pw", false, []string{"alice"}); err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Errorf("new creator identity accepted old token: %d", code)
	}
}

type tokenAuthEngine struct{ fakeEngine }

func (e tokenAuthEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	c := alice
	c.State = "running"
	return []engine.Container{c}, nil
}

func (e tokenAuthEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	c := alice
	c.State = "running"
	if id == c.Name || id == c.ID {
		return c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

func TestAgentOpenStreamClosesWhenCreatorCleanupFails(t *testing.T) {
	s, ts := newTestServerWith(t, tokenAuthEngine{})
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+agentPrefix+"/exec", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + bearer}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("revoked open stream stayed connected: %v", err)
	}
}

func TestAgentRegistrationRechecksCreatorAfterLookup(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	// the environment lookup runs between initial authorization and registration.
	s.Eng = &accessChangeEngine{fakeEngine: fakeEngine{}, change: func() {
		s.tokens.path = blockedStatePath(t)
		if err := s.Accounts.SetAccess("alice", nil); err != nil {
			t.Error(err)
		}
	}}
	s.Environments.Eng = s.Eng
	req := httptest.NewRequest(http.MethodGet, agentPrefix+"/exec", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	s.serveAgent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("creator changed during lookup: %d %s", w.Code, w.Body.String())
	}
}

type accessChangeEngine struct {
	fakeEngine
	change func()
}

func (e *accessChangeEngine) lookup() engine.Container {
	if e.change != nil {
		f := e.change
		e.change = nil
		f()
	}
	c := alice
	c.State = "running"
	return c
}

func (e *accessChangeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return []engine.Container{e.lookup()}, nil
}

func (e *accessChangeEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	if c := e.lookup(); id == c.Name || id == c.ID {
		return c, nil
	}
	return engine.Container{}, engine.ErrNotFound
}

func TestAgentGrantRevocationKeepsOtherEnvironmentsAndPasswordChanges(t *testing.T) {
	s, _ := newTestServer(t)
	define(t, s, "bob", time.Now())
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("bob", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	aliceToken, _, err := s.tokens.create("alice-bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	bobToken, _, err := s.tokens.create("bob-bot", "bob", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetPassword("anna", "changed"); err != nil {
		t.Fatal(err)
	}
	if !s.tokenCreatorAllowed(aliceToken) || !s.tokenCreatorAllowed(bobToken) {
		t.Fatal("password change invalidated agent tokens")
	}
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if s.tokenCreatorAllowed(aliceToken) {
		t.Fatal("revoked grant still authorizes")
	}
	if !s.tokenCreatorAllowed(bobToken) {
		t.Fatal("unrelated environment token was invalidated")
	}
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if s.tokenCreatorAllowed(aliceToken) {
		t.Fatal("regrant reset retained revision")
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

func restart(t *testing.T, s *Server) *Server {
	t.Helper()
	persistedAccounts, err := accounts.Load(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Options{Log: s.Log, Eng: s.Eng, Environments: s.Environments, Accounts: persistedAccounts, Layout: s.Layout, DefaultImage: "img"})
	if err != nil {
		t.Fatal(err)
	}
	return restarted
}

func TestTokenWithoutCreatorIdentityIsPurged(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	tok.CreatorCreated = time.Time{}
	if err := store.WriteJSON(s.tokens.path, map[string]any{"tokens": []token{tok}}); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadTokens(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	legacy.path = blockedStatePath(t)
	if err := legacy.bind(s.Accounts, s.environmentCreated); err == nil {
		t.Fatal("accepted an unpersisted purge")
	}
	if code := agentRequestCode(t, restart(t, s), bearer); code != http.StatusUnauthorized {
		t.Fatalf("token without creator identity authorized: %d", code)
	}
	reloaded, err := loadTokens(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.list) != 0 {
		t.Fatal("purge was not persisted")
	}
}

// A removal whose grant and token cleanup didn't persist, then an environment
// of the same name: neither the old token nor the old grant carries over.
func TestRecreatedEnvironmentRejectsOldTokensAndGrants(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusOK {
		t.Fatalf("fresh token: %d", code)
	}
	if err := s.Environments.Defs.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	define(t, s, "alice", tok.EnvironmentCreated.Add(time.Second))
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Fatalf("token of the removed environment authorized its successor: %d", code)
	}
	if n := len(s.tokens.forEnvironment("alice")); n != 0 {
		t.Fatalf("successor lists %d old tokens", n)
	}
	restarted := restart(t, s)
	if code := agentRequestCode(t, restarted, bearer); code != http.StatusUnauthorized {
		t.Fatalf("restart revived old token: %d", code)
	}
	reloaded, err := loadTokens(s.Layout.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.list) != 0 {
		t.Fatal("startup kept the old environment's token")
	}
	// The grant survives only while the name stays defined; creating the
	// name again (below) drops it.
	if a, _ := restarted.Accounts.Get("anna"); !a.HasAccess("alice") {
		t.Fatal("startup dropped the grant of a defined environment")
	}
}

func TestStartupDropsGrantsAndTokensOfUndefinedEnvironments(t *testing.T) {
	s, _ := newTestServer(t)
	define(t, s, "carol", time.Now())
	if err := s.Accounts.SetAccess("carol", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, gone, err := s.tokens.create("bot", "carol", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, kept, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Environments.Defs.Delete("carol"); err != nil {
		t.Fatal(err)
	}
	restarted := restart(t, s)
	a, _ := restarted.Accounts.Get("anna")
	if a.HasAccess("carol") || !a.HasAccess("alice") {
		t.Fatalf("grants after startup: %v", a.Environments)
	}
	if _, ok := restarted.tokens.check(gone); ok {
		t.Fatal("startup kept the token of an undefined environment")
	}
	if code := agentRequestCode(t, restarted, kept); code != http.StatusOK {
		t.Fatalf("startup broke a current token: %d", code)
	}
}

type failingCreateEngine struct{ fakeEngine }

func (failingCreateEngine) ImageInspect(context.Context, string) (engine.Image, error) {
	return engine.Image{ID: "sha256:img", Labels: map[string]string{environments.LabelContract: environments.Contract}}, nil
}
func (failingCreateEngine) Networks(context.Context, map[string]string) ([]engine.Network, error) {
	return nil, errors.New("no networks in this test")
}

func TestCreateDropsLeftoverGrantsAndTokensOfTheName(t *testing.T) {
	s, ts := newTestServerWith(t, failingCreateEngine{})
	define(t, s, "carol", time.Now())
	if err := s.Accounts.SetAccess("carol", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.tokens.create("bot", "carol", "anna", nil); err != nil {
		t.Fatal(err)
	}
	// A removal that deleted the definition but not the grant or token.
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
	for _, tok := range s.tokens.list {
		if tok.Environment == "carol" {
			t.Fatal("new environment inherited the removed one's token")
		}
	}
}

func TestAgentRegistrationRechecksExplicitRevocation(t *testing.T) {
	s, _ := newTestServer(t)
	tok, bearer, err := s.tokens.create("bot", "alice", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Eng = &accessChangeEngine{change: func() {
		if err := s.tokens.remove(func(t token) bool { return t.ID == tok.ID }); err != nil {
			t.Error(err)
		}
	}}
	s.Environments.Eng = s.Eng
	req := httptest.NewRequest(http.MethodGet, agentPrefix+"/exec", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	s.serveAgent(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked during lookup: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentAdminKeepsAccessWhenRedundantGrantRemoved(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("alice", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.tokens.create("bot", "alice", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if !s.tokenCreatorAllowed(tok) {
		t.Fatal("redundant grant removal revoked admin's retained access")
	}
}

func TestAgentRoleChangeRetainsRevocationDespiteCleanupFailure(t *testing.T) {
	s, _ := newTestServer(t)
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Admin = true }); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := false
	s.tokens.track(tok.ID, func() { canceled = true })
	s.tokens.path = blockedStatePath(t)
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Admin = false }); err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Fatal("demotion retained live token connection")
	}
	if _, err := s.Accounts.Update("anna", func(a *accounts.Account) { a.Admin = true }); err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Fatalf("promotion revived old role token: %d", code)
	}
}

func TestDelayedCreatorNotificationKeepsCurrentTokens(t *testing.T) {
	for _, change := range []string{"grant", "identity"} {
		t.Run(change, func(t *testing.T) {
			s, _ := newTestServer(t)
			delayedChange := func() error { return s.tokens.creatorChanged(s, "anna") }
			if change == "identity" {
				if err := s.Accounts.Delete("anna"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Accounts.Create("anna", "pw", false, []string{"alice"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
					t.Fatal(err)
				}
			}
			tok, bearer, err := s.tokens.create("new-bot", "alice", "anna", nil)
			if err != nil {
				t.Fatal(err)
			}
			canceled := false
			s.tokens.track(tok.ID, func() { canceled = true })
			if err := delayedChange(); err != nil {
				t.Fatal(err)
			}
			if canceled {
				t.Error("delayed callback canceled current token connection")
			}
			if _, ok := s.tokens.check(bearer); !ok {
				t.Fatal("delayed callback revoked token created with current permission")
			}
			reloaded, err := loadTokens(s.Layout.Data)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := reloaded.check(bearer); !ok {
				t.Fatal("delayed callback deleted current token from disk")
			}
		})
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
// still purges the grants, tokens and activity, and reports the removal.
func TestRemovalWhoseFolderDeletionFailsStillPurgesAccess(t *testing.T) {
	s, ts := newTestServerWith(t, folderStuckEngine{})
	if err := s.Accounts.SetAccess("alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.tokens.create("bot", "alice", "anna", nil); err != nil {
		t.Fatal(err)
	}
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
	if n := len(s.tokens.forEnvironment("alice")); n != 0 {
		t.Errorf("removal kept %d tokens", n)
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
