package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
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
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := false
	s.tokens.track(tok.ID, func() { canceled = true })
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
		t.Fatal(err)
	}
	if !canceled {
		t.Error("account access loss kept token connection open")
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Errorf("access loss still authorized: %d", code)
	}
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, s, bearer); code != http.StatusUnauthorized {
		t.Errorf("regrant revived token: %d", code)
	}
	persistedAccounts, err := accounts.Load(s.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Options{Log: s.Log, Eng: s.Eng, Desktops: s.Desktops, Accounts: persistedAccounts, StateDir: s.StateDir, DefaultImage: "img"})
	if err != nil {
		t.Fatal(err)
	}
	if code := agentRequestCode(t, restarted, bearer); code != http.StatusUnauthorized {
		t.Errorf("restart revived token: %d", code)
	}
}

func TestAgentDeletedCreatorCannotAuthorizeRecreatedAccount(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.tokens.path = blockedStatePath(t)
	if err := s.Accounts.Delete("anna"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts.Create("anna", "pw", false, []string{"id-alice"}); err != nil {
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

func TestAgentOpenStreamClosesWhenCreatorCleanupFails(t *testing.T) {
	s, ts := newTestServerWith(t, tokenAuthEngine{})
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
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
	if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
		t.Fatal(err)
	}
	_, _, err = ws.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("revoked open stream stayed connected: %v", err)
	}
}

func TestAgentRegistrationRechecksCreatorAfterLookup(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	_, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	// desktop lookup runs between initial authorization and registration.
	s.Eng = &accessChangeEngine{fakeEngine: fakeEngine{}, change: func() {
		s.tokens.path = blockedStatePath(t)
		if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
			t.Error(err)
		}
	}}
	s.Desktops.Eng = s.Eng
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

func (e *accessChangeEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	if e.change != nil {
		f := e.change
		e.change = nil
		f()
	}
	c := alice
	c.State = "running"
	return []engine.Container{c}, nil
}

func TestAgentGrantRevocationKeepsOtherDesktopsAndPasswordChanges(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("id-bob", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	aliceToken, _, err := s.tokens.create("alice-bot", "id-alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	bobToken, _, err := s.tokens.create("bob-bot", "id-bob", "anna", nil)
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
	if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
		t.Fatal(err)
	}
	if s.tokenCreatorAllowed(aliceToken) {
		t.Fatal("revoked grant still authorizes")
	}
	if !s.tokenCreatorAllowed(bobToken) {
		t.Fatal("unrelated desktop token was invalidated")
	}
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if s.tokenCreatorAllowed(aliceToken) {
		t.Fatal("regrant reset retained revision")
	}
}

func TestLegacyTokenCreatorMigrationFailsClosed(t *testing.T) {
	s, _ := newTestServer(t)
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	tok, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an old token file without creator identity/revision fields.
	tok.CreatorCreated = time.Time{}
	tok.CreatorEpoch = 0
	tok.GrantEpoch = 0
	originalPath := s.tokens.path
	if err := store.WriteJSON(originalPath, map[string]any{"tokens": []token{tok}}); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadTokens(s.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	legacy.path = blockedStatePath(t)
	if err := legacy.bindAccounts(s.Accounts); err == nil {
		t.Fatal("accepted unpersisted legacy migration")
	}
	legacy.path = originalPath
	if err := legacy.bindAccounts(s.Accounts); err != nil {
		t.Fatal(err)
	}
	migrated, ok := legacy.check(bearer)
	if !ok || !s.tokenCreatorAllowed(migrated) {
		t.Fatal("safe legacy token not migrated")
	}
	// A retained revocation revision forbids legacy rebinding after regrant.
	if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJSON(originalPath, map[string]any{"tokens": []token{tok}}); err != nil {
		t.Fatal(err)
	}
	legacy, err = loadTokens(s.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.bindAccounts(s.Accounts); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacy.check(bearer); ok {
		t.Fatal("legacy token rebound after revoked grant")
	}
	reloaded, err := loadTokens(s.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.check(bearer); ok {
		t.Fatal("legacy purge was not persisted")
	}
}

func TestAgentRegistrationRechecksExplicitRevocation(t *testing.T) {
	s, _ := newTestServer(t)
	tok, bearer, err := s.tokens.create("bot", "id-alice", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Eng = &accessChangeEngine{change: func() {
		if err := s.tokens.remove(func(t token) bool { return t.ID == tok.ID }); err != nil {
			t.Error(err)
		}
	}}
	s.Desktops.Eng = s.Eng
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
	if err := s.Accounts.SetAccess("id-alice", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.tokens.create("bot", "id-alice", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.SetAccess("id-alice", nil); err != nil {
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
	tok, bearer, err := s.tokens.create("bot", "id-alice", "anna", nil)
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
				if _, err := s.Accounts.Create("anna", "pw", false, []string{"id-alice"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
					t.Fatal(err)
				}
			}
			tok, bearer, err := s.tokens.create("new-bot", "id-alice", "anna", nil)
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
			reloaded, err := loadTokens(s.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := reloaded.check(bearer); !ok {
				t.Fatal("delayed callback deleted current token from disk")
			}
		})
	}
}
