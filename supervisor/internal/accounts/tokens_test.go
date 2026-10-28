package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// tokenStore is a store with the token account bob and the admin.
func tokenStore(t *testing.T) (*Store, Account) {
	t.Helper()
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAdmin("admin", "pw"); err != nil {
		t.Fatal(err)
	}
	bob, err := s.Create(NewAccount{Username: "bob", Grants: []Grant{{Environment: "alice", Level: auth.LevelFull}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, bob
}

func issue(t *testing.T, s *Store, a Account) string {
	t.Helper()
	code, err := s.IssueLink(a.Username, a.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func acceptAll(Account) error { return nil }

func pending(s *Store, username string) bool {
	a, _ := s.Get(username)
	return a.Link != nil
}

func TestLinkRedeemsOnce(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	if id, _, ok := auth.ParseBearer(LinkPrefix, code); !ok || len(id) != 8 {
		t.Fatalf("code %q", code)
	}
	if a, _ := s.Get("bob"); a.Link == nil || a.Link.CreatedBy != "admin" {
		t.Fatalf("issued link: %+v", a.Link)
	}
	a, _, bearer, err := s.RedeemLink(code, time.Now(), nil, acceptAll)
	if err != nil || a.ID != bob.ID || bearer != "" {
		t.Fatalf("redeem: %+v %q %v", a, bearer, err)
	}
	if pending(s, "bob") {
		t.Fatal("redeemed link still pending")
	}
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, acceptAll); !errors.Is(err, ErrLinkUsed) {
		t.Fatalf("second redemption: %v", err)
	}
	if _, ok := s.PeekLink(code, time.Now()); ok {
		t.Fatal("peek found a redeemed link")
	}
}

func TestRedeemMakesTheCredential(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	_, c, bearer, err := s.RedeemLink(code, time.Now(), &NewCredential{Kind: "cli", Name: "laptop"}, acceptAll)
	if err != nil || c.AccountID != bob.ID || c.Username != "bob" || c.Name != "laptop" {
		t.Fatalf("redeem: %+v %v", c, err)
	}
	if got, ok := s.CheckCredential(bearer); !ok || got.ID != c.ID {
		t.Fatalf("the credential doesn't check: %+v %v", got, ok)
	}
	// One write: the file has both.
	again, _ := Load(s.dataDir)
	if a, _ := again.Get("bob"); a.Link != nil || len(a.Credentials) != 1 {
		t.Fatalf("saved: link %+v, credentials %+v", a.Link, a.Credentials)
	}
}

func TestBadLinkCodesRejected(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	id, _, _ := auth.ParseBearer(LinkPrefix, code)
	for _, c := range []string{"", "nonsense", "dyl_" + id + "_wrong", "dyl_" + id, "dyc_" + strings.TrimPrefix(code, "dyl_")} {
		if _, ok := s.PeekLink(c, time.Now()); ok {
			t.Errorf("peek %q found a link", c)
		}
		if _, _, _, err := s.RedeemLink(c, time.Now(), nil, acceptAll); !errors.Is(err, ErrLinkUsed) {
			t.Errorf("redeem %q: %v", c, err)
		}
	}
	if !pending(s, "bob") {
		t.Fatal("bad codes consumed the link")
	}
}

func TestConcurrentRedeemHasOneWinner(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	errs := make([]error, 50)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, errs[i] = s.RedeemLink(code, time.Now(), &NewCredential{Kind: "cli"}, acceptAll)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrLinkUsed):
			t.Errorf("loser got %v", err)
		}
	}
	if wins != 1 || len(s.Credentials("")) != 1 {
		t.Fatalf("%d redemptions succeeded, %d credentials", wins, len(s.Credentials("")))
	}
}

func TestReissueInvalidatesPreviousLink(t *testing.T) {
	s, bob := tokenStore(t)
	carol, _ := s.Create(NewAccount{Username: "carol"})
	first := issue(t, s, bob)
	other := issue(t, s, carol)
	second := issue(t, s, bob)
	if _, _, _, err := s.RedeemLink(first, time.Now(), nil, acceptAll); !errors.Is(err, ErrLinkUsed) {
		t.Fatalf("replaced link: %v", err)
	}
	if _, ok := s.PeekLink(other, time.Now()); !ok {
		t.Fatal("reissuing bob's link replaced carol's")
	}
	if _, _, _, err := s.RedeemLink(second, time.Now(), nil, acceptAll); err != nil {
		t.Fatalf("new link: %v", err)
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	for range 3 {
		if a, ok := s.PeekLink(code, time.Now()); !ok || a.Username != "bob" {
			t.Fatalf("peek: %+v %v", a, ok)
		}
	}
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, acceptAll); err != nil {
		t.Fatalf("redeem after peeks: %v", err)
	}
}

// A link signs in only while its account may use anything.
func TestLinkOfUnusableAccountRejected(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	past := time.Now().Add(-time.Minute)
	if _, err := s.Update("bob", func(a *Account) { a.Expires = &past }); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.PeekLink(code, time.Now()); ok {
		t.Fatal("peek found the link of an expired account")
	}
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, acceptAll); !errors.Is(err, ErrLinkUsed) {
		t.Fatalf("redeemed the link of an expired account: %v", err)
	}
}

func TestAcceptErrorKeepsLink(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	refused := errors.New("observe-only access is for the browser")
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, func(Account) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("refused redemption: %v", err)
	}
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, acceptAll); err != nil {
		t.Fatalf("link after a refusal: %v", err)
	}
}

// A redemption, a link or a credential that can't be saved changes
// nothing: the link stays for trying again, the replaced link and old
// WebDAV password keep working.
func TestTokenWriteFailureKeepsWhatWas(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	_, old, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "webdav"}, true)
	path := s.path
	s.path = blocked(t, s.dataDir)
	if _, _, _, err := s.RedeemLink(code, time.Now(), &NewCredential{Kind: "cli"}, acceptAll); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("redeem without persisting: %v", err)
	}
	if _, err := s.IssueLink("bob", bob.ID, "admin"); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("issue without persisting: %v", err)
	}
	if _, _, err := s.AddCredential("bob", bob.ID, NewCredential{Kind: "webdav"}, true); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("add without persisting: %v", err)
	}
	if _, ok := s.CheckCredential(old); !ok {
		t.Fatal("a failed replacement dropped the old credential")
	}
	if s.Unsaved() {
		t.Fatal("held something")
	}
	s.path = path
	if _, _, _, err := s.RedeemLink(code, time.Now(), nil, acceptAll); err != nil {
		t.Fatalf("link after a failed write: %v", err)
	}
}

// Deleting a credential, or its account, ends it even when that can't be
// saved; the hook hears of it either way.
func TestCredentialRemovalFailsClosed(t *testing.T) {
	s, bob := tokenStore(t)
	var gone []string
	s.OnCredentialsGone = func(ids ...string) { gone = append(gone, ids...) }
	c, bearer, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "cli"}, false)
	d, other, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "cli"}, false)
	s.path = blocked(t, s.dataDir)
	if _, err := s.RemoveCredentials(func(o Credential) bool { return o.ID == c.ID }); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := s.CheckCredential(bearer); ok || s.HasCredential(c.ID) || !s.Unsaved() {
		t.Fatal("a removal that couldn't be saved kept the credential")
	}
	if err := s.Delete("bob"); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.CheckCredential(other); ok {
		t.Fatal("a deletion that couldn't be saved kept the account's credential")
	}
	if len(gone) != 2 || gone[0] != c.ID || gone[1] != d.ID {
		t.Fatalf("gone: %v", gone)
	}
}

func TestCredentialLastUseAndList(t *testing.T) {
	s, bob := tokenStore(t)
	first, _, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "cli", Name: "one"}, false)
	time.Sleep(time.Millisecond)
	second, bearer, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "cli", Name: "two"}, false)
	admin, _ := s.Get("admin")
	if _, _, err := s.AddCredential("admin", admin.ID, NewCredential{Kind: "cli"}, false); err != nil {
		t.Fatal(err)
	}
	if got := s.Credentials(bob.ID); len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID || got[0].Username != "bob" {
		t.Fatalf("bob's credentials, newest first: %+v", got)
	}
	if len(s.Credentials("")) != 3 {
		t.Fatal("all credentials")
	}
	got, ok := s.CheckCredential(bearer)
	if !ok || got.LastUsed == nil {
		t.Fatalf("last use not recorded: %+v", got)
	}
	again, _ := Load(s.dataDir)
	if c := again.Credentials(bob.ID)[0]; c.LastUsed == nil || !c.LastUsed.Equal(*got.LastUsed) {
		t.Fatalf("last use not saved: %+v", c)
	}
	if _, _, err := s.AddCredential("bob", "another-id", NewCredential{Kind: "cli"}, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("credential for another account of the name: %v", err)
	}
}

func TestCreateWithLink(t *testing.T) {
	s, _ := tokenStore(t)
	a, code, err := s.CreateWithLink(NewAccount{Username: "sanne"}, "admin")
	if err != nil || a.Link == nil {
		t.Fatalf("create: %+v %v", a, err)
	}
	if got, ok := s.PeekLink(code, time.Now()); !ok || got.ID != a.ID {
		t.Fatal("the link doesn't sign in as the account")
	}
	s.path = blocked(t, s.dataDir)
	if _, _, err := s.CreateWithLink(NewAccount{Username: "sven"}, "admin"); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("create without persisting: %v", err)
	}
	if _, ok := s.Get("sven"); ok {
		t.Fatal("an account that couldn't be saved exists")
	}
}

// The secrets aren't on disk, only their hashes.
func TestTokensKeepHashesOnly(t *testing.T) {
	s, bob := tokenStore(t)
	code := issue(t, s, bob)
	_, bearer, _ := s.AddCredential("bob", bob.ID, NewCredential{Kind: "cli"}, false)
	b, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{code, bearer} {
		_, secret, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(secret, LinkPrefix), CredentialPrefix), "_")
		if strings.Contains(string(b), secret) {
			t.Fatal("accounts.json holds a secret")
		}
	}
}

// links.json and credentials.json of earlier versions move into
// accounts.json, and are removed once it is saved.
func TestLoadMovesTokenFiles(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	_, _ = s.EnsureAdmin("admin", "pw")
	bob, _ := s.Create(NewAccount{Username: "bob"})
	old := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, code, hash := auth.NewSecret(LinkPrefix)
	linkID, _, _ := auth.ParseBearer(LinkPrefix, code)
	_, bearer, credHash := auth.NewSecret(CredentialPrefix)
	credID, _, _ := auth.ParseBearer(CredentialPrefix, bearer)
	old("links.json", `{"links":[
		{"id":"`+linkID+`","username":"bob","accountID":"`+bob.ID+`","createdBy":"admin","hash":"`+hash+`","created":"2026-01-01T00:00:00Z"},
		{"id":"gone1234","username":"carol","accountID":"carol-id","createdBy":"admin","hash":"x","created":"2026-01-01T00:00:00Z"}]}`)
	old("credentials.json", `{"credentials":[
		{"id":"`+credID+`","accountID":"`+bob.ID+`","username":"bob","kind":"cli","name":"laptop","hash":"`+credHash+`","created":"2026-01-01T00:00:00Z","lastUsed":null},
		{"id":"unfinish","accountID":"`+bob.ID+`","username":"bob","kind":"cli","name":"x","hash":"x","created":"2026-01-01T00:00:00Z","lastUsed":null,"link":"`+linkID+`"},
		{"id":"recreate","accountID":"old-bob","username":"bob","kind":"cli","name":"x","hash":"x","created":"2026-01-01T00:00:00Z","lastUsed":null}]}`)
	moved, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"links.json", "credentials.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", name, err)
		}
	}
	if a, ok := moved.PeekLink(code, time.Now()); !ok || a.ID != bob.ID {
		t.Fatal("the moved link doesn't sign in")
	}
	if c, ok := moved.CheckCredential(bearer); !ok || c.Name != "laptop" || c.Username != "bob" {
		t.Fatalf("the moved credential: %+v %v", c, ok)
	}
	// Not the credential of an unfinished redemption (its link is unused),
	// nor one of an earlier account of the name.
	if got := moved.Credentials(""); len(got) != 1 {
		t.Fatalf("moved credentials: %+v", got)
	}
	again, _ := Load(dir)
	if a, _ := again.Get("bob"); a.Link == nil || len(a.Credentials) != 1 {
		t.Fatalf("not saved: %+v", a)
	}
}

// When accounts.json can't be saved, the old files stay.
func TestLoadKeepsTokenFilesUntilSaved(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	_, _ = s.EnsureAdmin("admin", "pw")
	if err := os.WriteFile(filepath.Join(dir, "links.json"), []byte(`{"links":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := Load(dir); err == nil {
		t.Skip("the folder is writable regardless (running as root?)")
	}
	if _, err := os.Stat(filepath.Join(dir, "links.json")); err != nil {
		t.Fatalf("links.json removed before accounts.json was saved: %v", err)
	}
}
