package server

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

func newLinkStore(t *testing.T) *linkStore {
	t.Helper()
	ls, err := loadLinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ls
}

func issueLink(t *testing.T, ls *linkStore, a accounts.Account) string {
	t.Helper()
	code, err := ls.issue(a, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func acceptAll(link) error { return nil }

func TestLinkRedeemsOnce(t *testing.T) {
	ls := newLinkStore(t)
	bob := accounts.Account{ID: "bob-id", Username: "bob"}
	code := issueLink(t, ls, bob)
	if id, _, ok := auth.ParseBearer(linkPrefix, code); !ok || len(id) != 8 {
		t.Fatalf("code %q", code)
	}
	if !ls.pending(bob.ID) {
		t.Fatal("issued link not pending")
	}
	l, err := ls.redeem(code, acceptAll)
	if err != nil || l.Username != "bob" || l.AccountID != bob.ID || l.CreatedBy != "admin" {
		t.Fatalf("redeem: %+v %v", l, err)
	}
	if ls.pending(bob.ID) {
		t.Fatal("redeemed link still pending")
	}
	if _, err := ls.redeem(code, acceptAll); !errors.Is(err, errLinkUsed) {
		t.Fatalf("second redemption: %v", err)
	}
	if _, ok := ls.peek(code); ok {
		t.Fatal("peek found a redeemed link")
	}
}

func TestBadLinkCodesRejected(t *testing.T) {
	ls := newLinkStore(t)
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	id, _, _ := auth.ParseBearer(linkPrefix, code)
	for _, c := range []string{"", "nonsense", "dyl_" + id + "_wrong", "dyl_" + id, "dyc_" + strings.TrimPrefix(code, "dyl_")} {
		if _, ok := ls.peek(c); ok {
			t.Errorf("peek %q found a link", c)
		}
		if _, err := ls.redeem(c, acceptAll); !errors.Is(err, errLinkUsed) {
			t.Errorf("redeem %q: %v", c, err)
		}
	}
	if !ls.pending("bob-id") {
		t.Fatal("bad codes consumed the link")
	}
}

func TestConcurrentRedeemHasOneWinner(t *testing.T) {
	ls := newLinkStore(t)
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	errs := make([]error, 50)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = ls.redeem(code, acceptAll)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, errLinkUsed):
			t.Errorf("loser got %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d redemptions succeeded", wins)
	}
}

func TestReissueInvalidatesPreviousLink(t *testing.T) {
	ls := newLinkStore(t)
	bob := accounts.Account{ID: "bob-id", Username: "bob"}
	first := issueLink(t, ls, bob)
	other := issueLink(t, ls, accounts.Account{ID: "carol-id", Username: "carol"})
	second := issueLink(t, ls, bob)
	if _, err := ls.redeem(first, acceptAll); !errors.Is(err, errLinkUsed) {
		t.Fatalf("replaced link: %v", err)
	}
	if _, ok := ls.peek(other); !ok {
		t.Fatal("reissuing bob's link replaced carol's")
	}
	if _, err := ls.redeem(second, acceptAll); err != nil {
		t.Fatalf("new link: %v", err)
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	ls := newLinkStore(t)
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	for range 3 {
		if l, ok := ls.peek(code); !ok || l.Username != "bob" {
			t.Fatalf("peek: %+v %v", l, ok)
		}
	}
	if _, err := ls.redeem(code, acceptAll); err != nil {
		t.Fatalf("redeem after peeks: %v", err)
	}
}

func TestAcceptErrorKeepsLink(t *testing.T) {
	ls := newLinkStore(t)
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	refused := errors.New("observe-only access is for the browser")
	if _, err := ls.redeem(code, func(link) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("refused redemption: %v", err)
	}
	if _, err := ls.redeem(code, acceptAll); err != nil {
		t.Fatalf("link after a refusal: %v", err)
	}
}

func TestRedeemWriteFailureKeepsLink(t *testing.T) {
	ls := newLinkStore(t)
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	path := ls.path
	ls.path = blockedStatePath(t)
	if _, err := ls.redeem(code, acceptAll); err == nil || errors.Is(err, errLinkUsed) {
		t.Fatalf("redeem without persisting: %v", err)
	}
	if _, err := ls.issue(accounts.Account{ID: "bob-id", Username: "bob"}, "admin"); err == nil {
		t.Fatal("issue reported success without persisting")
	}
	ls.path = path
	if _, err := ls.redeem(code, acceptAll); err != nil {
		t.Fatalf("link after a failed write: %v", err)
	}
}

func TestLinksSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	ls, err := loadLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	code := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	st, err := os.Stat(ls.path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("links.json: %v %v", st, err)
	}
	_, secret, _ := auth.ParseBearer(linkPrefix, code)
	if b, _ := os.ReadFile(ls.path); strings.Contains(string(b), secret) {
		t.Fatal("links.json holds the secret")
	}
	restarted, err := loadLinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.pending("bob-id") {
		t.Fatal("link not pending after restart")
	}
	if l, err := restarted.redeem(code, acceptAll); err != nil || l.Username != "bob" {
		t.Fatalf("redeem after restart: %+v %v", l, err)
	}
	if again, _ := loadLinks(dir); again.pending("bob-id") {
		t.Fatal("redemption not persisted")
	}
}

func TestLinkOfRecreatedAccountRejected(t *testing.T) {
	s, _ := newTestServer(t)
	ls := newLinkStore(t)
	code := issueLink(t, ls, createBob(t, s))
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	createBob(t, s)
	// The redemption route's accept checks the link against the current
	// account of that name.
	current := func(l link) error {
		a, ok := s.Accounts.Get(l.Username)
		if !ok || a.ID != l.AccountID {
			return errLinkUsed
		}
		return nil
	}
	if _, err := ls.redeem(code, current); !errors.Is(err, errLinkUsed) {
		t.Fatalf("link of the earlier bob redeemed for the recreated one: %v", err)
	}
}

func TestLinkRetain(t *testing.T) {
	dir := t.TempDir()
	ls, _ := loadLinks(dir)
	keep := issueLink(t, ls, accounts.Account{ID: "bob-id", Username: "bob"})
	drop := issueLink(t, ls, accounts.Account{ID: "carol-id", Username: "carol"})
	if err := ls.retain(func(l link) bool { return l.AccountID == "bob-id" }); err != nil {
		t.Fatal(err)
	}
	restarted, _ := loadLinks(dir)
	if _, ok := restarted.peek(drop); ok {
		t.Fatal("retain kept carol's link")
	}
	if _, ok := restarted.peek(keep); !ok {
		t.Fatal("retain dropped bob's link")
	}
	ls.path = blockedStatePath(t)
	if err := ls.retain(func(link) bool { return false }); err == nil {
		t.Fatal("retain reported success without persisting")
	}
}

// A CLI redemption stopped between saving its credential and deleting its
// link leaves a credential whose bearer was never handed out: the restart
// deletes it, and keeps the link and the credentials of finished ones.
func TestRestartDeletesCredentialOfUnfinishedRedemption(t *testing.T) {
	s, _ := newTestServer(t)
	bob := createBob(t, s)
	code := issueLink(t, s.links, bob)
	id, _, _ := auth.ParseBearer(linkPrefix, code)
	unfinished, _, err := s.credentials.createRedeeming(bob, defaultCredentialName, id)
	if err != nil {
		t.Fatal(err)
	}
	anna := accountNamed(t, s, "anna")
	annaCode := issueLink(t, s.links, anna)
	var finished credential
	if _, err := s.links.redeem(annaCode, func(l link) error {
		finished, _, err = s.credentials.createRedeeming(anna, defaultCredentialName, l.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	restarted := restart(t, s)
	if restarted.credentials.has(unfinished.ID) {
		t.Error("kept the credential of an unfinished redemption")
	}
	if !restarted.credentials.has(finished.ID) {
		t.Error("deleted the credential of a finished redemption")
	}
	if !restarted.links.pending(bob.ID) {
		t.Error("deleted the link of the unfinished redemption")
	}
}

// A redemption whose link deletion and cleanup both failed leaves a
// credential whose bearer was never handed out, and keeps the link: trying
// the link again deletes that credential, from a CLI or a browser.
func TestRedeemingAgainDeletesCredentialOfFailedRedemption(t *testing.T) {
	for _, client := range []string{"cli", "browser"} {
		t.Run(client, func(t *testing.T) {
			s, ts := newTestServer(t)
			bob := fullOnAlice(t, s)
			code := issueLink(t, s.links, bob)
			id, _, _ := auth.ParseBearer(linkPrefix, code)
			leftover, _, err := s.credentials.createRedeeming(bob, defaultCredentialName, id)
			if err != nil {
				t.Fatal(err)
			}
			var status int
			var body string
			if client == "cli" {
				status, body = redeemCLI(t, ts, code)
			} else {
				status, body = send(t, browser(), "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+code+`","client":"browser"}`)
			}
			if status != http.StatusOK {
				t.Fatalf("redeem again: %d %s", status, body)
			}
			if s.credentials.has(leftover.ID) {
				t.Error("kept the credential of the failed redemption")
			}
			if restart(t, s).credentials.has(leftover.ID) {
				t.Error("the credential of the failed redemption survives a restart")
			}
		})
	}
}
