package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// send makes a request with a JSON body ("" for none) and, unless origin is
// "", an Origin; it returns the status and the body.
func send(t *testing.T, c *http.Client, method, url, origin, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, r)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// sendBearer is send with a client credential and no Origin.
func sendBearer(t *testing.T, method, url, bearer, body string) (int, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// browser is a signed-out browser: it keeps cookies and doesn't follow
// redirects.
func browser() *http.Client {
	return &http.Client{Jar: &cookieJar{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// fullOnAlice makes bob (a password account) a full user of alice.
func fullOnAlice(t *testing.T, s *Server) accounts.Account {
	t.Helper()
	createBob(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob")); err != nil {
		t.Fatal(err)
	}
	return accountNamed(t, s, "bob")
}

// shareAlice shares alice through the API as c and returns the link's code.
func shareAlice(t *testing.T, ts *httptest.Server, c *http.Client, username string, level auth.Level) string {
	t.Helper()
	code, body := send(t, c, "POST", ts.URL+"/api/environments/alice/shares", ts.URL, `{"username":"`+username+`","level":"`+string(level)+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("share: %d %s", code, body)
	}
	var res struct {
		Share shareJSON
		Link  string
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	base, linkCode, ok := strings.Cut(res.Link, "#")
	if !ok || base != ts.URL+"/link" || !strings.HasPrefix(linkCode, "dyl_") {
		t.Fatalf("link %q", res.Link)
	}
	if res.Share.Username != username || res.Share.Level != level || !res.Share.LinkPending {
		t.Fatalf("share %+v", res.Share)
	}
	return linkCode
}

// redeemCLI redeems a link code as the CLI does.
func redeemCLI(t *testing.T, ts *httptest.Server, linkCode string) (int, string) {
	t.Helper()
	return send(t, http.DefaultClient, "POST", ts.URL+"/api/links/redeem", "", `{"link":"`+linkCode+`","client":"cli","name":"deskyard CLI on test"}`)
}

func TestShareFlow(t *testing.T) {
	s, ts := newTestServer(t)
	bobAccount := fullOnAlice(t, s)
	bob := login(t, ts, "bob")
	linkCode := shareAlice(t, ts, bob, "sanne", auth.LevelObserve)

	// Inspection needs no session and uses nothing up.
	for range 2 {
		code, body := send(t, http.DefaultClient, "POST", ts.URL+"/api/links/inspect", "", `{"link":"`+linkCode+`"}`)
		var info linkInfoJSON
		_ = json.Unmarshal([]byte(body), &info)
		if code != 200 || info.Username != "sanne" || len(info.Grants) != 1 || info.Grants[0] != (accounts.Grant{Environment: "alice", Level: auth.LevelObserve}) ||
			info.SharedBy == nil || *info.SharedBy != (accounts.Sharer{Username: "bob", ID: bobAccount.ID}) {
			t.Fatalf("inspect: %d %s", code, body)
		}
	}

	// A browser redemption needs the same origin.
	sanne := browser()
	if code, _ := send(t, sanne, "POST", ts.URL+"/api/links/redeem", "", `{"link":"`+linkCode+`","client":"browser"}`); code != 403 {
		t.Fatalf("cross-origin redemption: %d", code)
	}
	code, body := send(t, sanne, "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+linkCode+`","client":"browser"}`)
	if code != 200 || !strings.Contains(body, `"username":"sanne"`) || !strings.Contains(body, `"next":"/alice/"`) {
		t.Fatalf("redeem: %d %s", code, body)
	}
	if got := status(t, sanne, "GET", ts.URL+"/api/environments/alice", ""); got != 200 {
		t.Fatalf("share GET alice: %d", got)
	}
	if got := status(t, sanne, "GET", ts.URL+"/api/environments/alice/processes", ""); got != 403 {
		t.Fatalf("observe share GET processes: %d", got)
	}
	_, body = get(t, sanne, ts.URL+"/api/me")
	if !strings.Contains(body, `"kind":"token"`) || !strings.Contains(body, `"share":true`) {
		t.Fatalf("share's me: %s", body)
	}
	// Used once.
	if code, _ := send(t, browser(), "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+linkCode+`","client":"browser"}`); code != http.StatusGone {
		t.Fatalf("second redemption: %d", code)
	}
	if code, _ := send(t, http.DefaultClient, "POST", ts.URL+"/api/links/inspect", "", `{"link":"`+linkCode+`"}`); code != http.StatusGone {
		t.Fatalf("inspecting a used link: %d", code)
	}

	var shares []shareJSON
	if code := getJSON(t, bob, ts.URL+"/api/environments/alice/shares", &shares); code != 200 || len(shares) != 1 {
		t.Fatalf("shares: %d %+v", code, shares)
	}
	if sh := shares[0]; sh.Username != "sanne" || sh.Environment != "alice" || sh.LinkPending || sh.LastLogin == nil || sh.SharedBy.ID != bobAccount.ID {
		t.Fatalf("share listed as %+v", sh)
	}
	// Shares stay off the environment's access list.
	admin := login(t, ts, "admin")
	_, body = get(t, admin, ts.URL+"/api/environments/alice")
	if strings.Contains(body, "sanne") || !strings.Contains(body, `"access":[{"username":"bob","level":"full"}]`) {
		t.Fatalf("access lists the share: %s", body)
	}
	// Observers can't list shares.
	if got := status(t, sanne, "GET", ts.URL+"/api/environments/alice/shares", ""); got != 403 {
		t.Fatalf("observer lists shares: %d", got)
	}
}

func TestShareCannotShare(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	linkCode := shareAlice(t, ts, admin, "carla", auth.LevelFull)
	code, body := redeemCLI(t, ts, linkCode)
	var res struct {
		Credential   string
		Username     string
		Environments []struct{ Name, Level string }
	}
	_ = json.Unmarshal([]byte(body), &res)
	if code != 200 || res.Username != "carla" || !strings.HasPrefix(res.Credential, "dyc_") ||
		len(res.Environments) != 1 || res.Environments[0].Name != "alice" || res.Environments[0].Level != "full" {
		t.Fatalf("CLI redemption: %d %s", code, body)
	}
	code, body = sendBearer(t, "POST", ts.URL+"/api/environments/alice/shares", res.Credential, `{"username":"dirk","level":"observe"}`)
	if code != 403 || !strings.Contains(body, "shares can't share") {
		t.Fatalf("a share shared: %d %s", code, body)
	}

	// Observers can't share either.
	observeAlice(t, s)
	code, body = send(t, login(t, ts, "bob"), "POST", ts.URL+"/api/environments/alice/shares", ts.URL, `{"username":"dirk","level":"observe"}`)
	if code != 403 || !strings.Contains(body, "observe-only") {
		t.Fatalf("an observer shared: %d %s", code, body)
	}
	if _, ok := s.Accounts.Get("dirk"); ok {
		t.Fatal("a refused share was made")
	}
	// Bad requests.
	for _, req := range []string{`{"username":"Not Valid","level":"full"}`, `{"username":"dirk","level":"write"}`,
		`{"username":"dirk","level":"full","expires":"2001-01-01T00:00:00Z"}`} {
		if code, body := send(t, admin, "POST", ts.URL+"/api/environments/alice/shares", ts.URL, req); code != 400 {
			t.Errorf("share %s: %d %s", req, code, body)
		}
	}
	if code, _ := send(t, admin, "POST", ts.URL+"/api/environments/alice/shares", ts.URL, `{"username":"anna","level":"full"}`); code != 409 {
		t.Errorf("share in use name: %d", code)
	}
}

func TestObserveShareRefusedForCLIKeepsLink(t *testing.T) {
	s, ts := newTestServer(t)
	linkCode := shareAlice(t, ts, login(t, ts, "admin"), "sanne", auth.LevelObserve)
	code, body := redeemCLI(t, ts, linkCode)
	if code != 403 || !strings.Contains(body, "observe-only access is for the browser") {
		t.Fatalf("observe share CLI redemption: %d %s", code, body)
	}
	if !linkPending(t, s, accountNamed(t, s, "sanne").ID) {
		t.Fatal("the refused redemption used the link")
	}
	if len(s.Accounts.Credentials(accountNamed(t, s, "sanne").ID)) != 0 {
		t.Fatal("the refused redemption left a credential")
	}
	if code, _ := send(t, browser(), "POST", ts.URL+"/api/links/redeem", ts.URL, `{"link":"`+linkCode+`","client":"browser"}`); code != 200 {
		t.Fatalf("browser redemption after the refusal: %d", code)
	}
}

func TestNewLinkByOtherUserForbidden(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob", "anna")); err != nil {
		t.Fatal(err)
	}
	bob := login(t, ts, "bob")
	first := shareAlice(t, ts, bob, "sanne", auth.LevelFull)

	anna := login(t, ts, "anna")
	for _, req := range [][2]string{{"POST", "/api/shares/sanne/link"}, {"DELETE", "/api/shares/sanne"}} {
		code, body := send(t, anna, req[0], ts.URL+req[1], ts.URL, "")
		if code != 403 || !strings.Contains(body, "only the person who shared it or an admin can do that") {
			t.Fatalf("anna %s %s: %d %s", req[0], req[1], code, body)
		}
	}
	// Full users see every share of the environment.
	var shares []shareJSON
	if getJSON(t, anna, ts.URL+"/api/environments/alice/shares", &shares); len(shares) != 1 {
		t.Fatalf("anna's shares: %+v", shares)
	}
	for _, path := range []string{"/api/shares/anna/link", "/api/shares/nobody/link"} {
		if code, _ := send(t, bob, "POST", ts.URL+path, ts.URL, ""); code != 404 {
			t.Fatalf("POST %s: %d", path, code)
		}
	}

	code, body := send(t, bob, "POST", ts.URL+"/api/shares/sanne/link", ts.URL, "")
	if code != 200 || !strings.Contains(body, `"link":"`+ts.URL+`/link#dyl_`) {
		t.Fatalf("sharer's new link: %d %s", code, body)
	}
	if code, _ := redeemCLI(t, ts, first); code != http.StatusGone {
		t.Fatalf("replaced link: %d", code)
	}
	if code, _ := send(t, login(t, ts, "admin"), "POST", ts.URL+"/api/shares/sanne/link", ts.URL, ""); code != 200 {
		t.Fatalf("admin's new link: %d", code)
	}
}

func TestRevokeShareClosesStream(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	bob := login(t, ts, "bob")
	linkCode := shareAlice(t, ts, bob, "sanne", auth.LevelFull)
	code, body := redeemCLI(t, ts, linkCode)
	if code != 200 {
		t.Fatalf("redeem: %d %s", code, body)
	}
	share := accountNamed(t, s, "sanne")
	creds := s.Accounts.Credentials(share.ID)
	if len(creds) != 1 {
		t.Fatalf("credentials %+v", creds)
	}
	exec, cancelExec := context.WithCancel(context.Background())
	defer trackConn(s, creds[0], "alice", cancelExec)()
	stream, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	cn := &conn{username: "sanne", environment: "alice", viewer: true, level: auth.LevelFull, cancel: cancelStream}
	s.conns.add(cn)
	defer s.conns.remove(cn)

	var shares []shareJSON
	if getJSON(t, bob, ts.URL+"/api/environments/alice/shares", &shares); len(shares) != 1 || !shares[0].Connected {
		t.Fatalf("connected share listed as %+v", shares)
	}
	if code, body := send(t, bob, "DELETE", ts.URL+"/api/shares/sanne", ts.URL, ""); code != 204 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if stream.Err() == nil || exec.Err() == nil {
		t.Fatalf("revoking left connections open: stream %v, exec %v", stream.Err(), exec.Err())
	}
	shareGone(t, s, share)
	if code, _ := send(t, bob, "DELETE", ts.URL+"/api/shares/sanne", ts.URL, ""); code != 404 {
		t.Fatalf("revoking again: %d", code)
	}
}

func TestLinkInspectionThrottled(t *testing.T) {
	_, ts := newTestServer(t)
	throttled := false
	for range 10 {
		code, _ := send(t, http.DefaultClient, "POST", ts.URL+"/api/links/inspect", "", `{"link":"dyl_00000000_nope"}`)
		if code == http.StatusTooManyRequests {
			throttled = true
			break
		}
		if code != http.StatusGone {
			t.Fatalf("bad code: %d", code)
		}
	}
	if !throttled {
		t.Fatal("repeated bad codes weren't throttled")
	}
	if code, _ := redeemCLI(t, ts, "dyl_00000000_nope"); code != http.StatusTooManyRequests {
		t.Fatalf("redemption after the throttle: %d", code)
	}
}

// A share's last use counts its CLI credential and its browser, not only
// its link's redemption.
func TestShareLastUsed(t *testing.T) {
	s, ts := newTestServer(t)
	admin := accountNamed(t, s, "admin")
	share, c := createShare(t, s, admin, "sanne", auth.LevelFull)
	if j := s.toShareJSON(share); j.LastUsed != nil {
		t.Fatalf("an unused share was used %v", j.LastUsed)
	}
	if err := s.Accounts.TouchLogin("sanne"); err != nil {
		t.Fatal(err)
	}
	redeemed := accountNamed(t, s, "sanne").LastLogin
	later := redeemed.Add(time.Hour)
	used := accountNamed(t, s, "sanne")
	if len(used.Credentials) != 1 || used.Credentials[0].ID != c.ID {
		t.Fatalf("credentials %+v", used.Credentials)
	}
	used.Credentials[0].LastUsed = &later
	if j := s.toShareJSON(used); j.LastUsed == nil || !j.LastUsed.Equal(later) {
		t.Fatalf("last used %v, want the credential's %v", j.LastUsed, later)
	}
	// So does a browser request.
	s.seen.touch(share.ID, later.Add(time.Hour))
	if j := s.toShareJSON(accountNamed(t, s, "sanne")); j.LastUsed == nil || !j.LastUsed.Equal(later.Add(time.Hour)) {
		t.Fatalf("last used %v, want the browser's", j.LastUsed)
	}
	before := time.Now()
	if code := status(t, login(t, ts, "anna"), "GET", ts.URL+"/api/me", ""); code != 200 {
		t.Fatal(code)
	}
	if seen := s.seen.get(accountNamed(t, s, "anna").ID); seen.Before(before) {
		t.Fatalf("a browser request wasn't seen: %v", seen)
	}
}
