package server

import (
	"net/http"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
)

// issueLink issues a login link for a by the admin.
func issueLink(t *testing.T, s *Server, a accounts.Account) string {
	t.Helper()
	code, err := s.Accounts.IssueLink(a.Username, a.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// A link of an earlier account of the name doesn't sign in the recreated
// one.
func TestLinkOfRecreatedAccountRejected(t *testing.T) {
	s, ts := newTestServer(t)
	code := issueLink(t, s, createBob(t, s))
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	createBob(t, s)
	if status, body := send(t, browser(), "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+code+`","client":"browser"}`); status != http.StatusGone {
		t.Fatalf("link of the earlier bob redeemed for the recreated one: %d %s", status, body)
	}
}

// A redemption that can't be saved keeps the link, for trying again.
func TestRedeemWriteFailureKeepsLink(t *testing.T) {
	for _, client := range []string{"cli", "browser"} {
		t.Run(client, func(t *testing.T) {
			s, ts := newTestServer(t)
			bob := fullOnAlice(t, s)
			code := issueLink(t, s, bob)
			redeem := func() (int, string) {
				if client == "cli" {
					return redeemCLI(t, ts, code)
				}
				return send(t, browser(), "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+code+`","client":"browser"}`)
			}
			blockAccountsFile(t, s)
			if status, body := redeem(); status != http.StatusInternalServerError {
				t.Fatalf("redeem while saving fails: %d %s", status, body)
			}
			unblockAccountsFile(t, s)
			if status, body := redeem(); status != http.StatusOK {
				t.Fatalf("redeem again: %d %s", status, body)
			}
			if n := len(s.Accounts.Credentials(bob.ID)); client == "cli" && n != 1 || client == "browser" && n != 0 {
				t.Fatalf("%d credentials", n)
			}
		})
	}
}
