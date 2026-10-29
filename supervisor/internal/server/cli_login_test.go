package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

type cliLoginStart struct {
	ID         string
	Code       string
	ApproveURL string
	PollSecret string
	Interval   int
	ExpiresIn  int
}

// startCLILogin starts a CLI login as deskyard login does.
func startCLILogin(t *testing.T, ts *httptest.Server) cliLoginStart {
	t.Helper()
	code, body := send(t, http.DefaultClient, "POST", ts.URL+"/api/cli-login", "", `{"client":"deskyard CLI on test"}`)
	var res cliLoginStart
	if err := json.Unmarshal([]byte(body), &res); code != 200 || err != nil {
		t.Fatalf("start: %d %s", code, body)
	}
	return res
}

func pollCLILogin(t *testing.T, ts *httptest.Server, id, pollSecret string) (int, string) {
	t.Helper()
	return send(t, http.DefaultClient, "POST", ts.URL+"/api/cli-login/"+id+"/token", "", `{"pollSecret":"`+pollSecret+`"}`)
}

func TestCLILoginApprove(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	start := startCLILogin(t, ts)
	if !regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`).MatchString(start.Code) ||
		start.ApproveURL != ts.URL+"/cli-login#"+start.ID || start.PollSecret == "" || start.Interval != 2 || start.ExpiresIn != 600 {
		t.Fatalf("start: %+v", start)
	}
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusAccepted || !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("pending poll: %d %s", code, body)
	}

	bob := login(t, ts, "bob")
	var view struct{ Client, IP, Code string }
	if code := getJSON(t, bob, ts.URL+"/api/cli-login/"+start.ID, &view); code != 200 ||
		view.Client != "deskyard CLI on test" || view.IP != "127.0.0.1" || view.Code != start.Code {
		t.Fatalf("view: %d %+v", code, view)
	}
	if code, body := send(t, bob, "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("approve: %d %s", code, body)
	}
	if code := getJSON(t, bob, ts.URL+"/api/cli-login/"+start.ID, nil); code != 404 {
		t.Fatalf("view after approval: %d", code)
	}

	code, body := pollCLILogin(t, ts, start.ID, start.PollSecret)
	var res struct {
		Credential string
		Username   string
		Computers  []computerLevel `json:"environments"`
	}
	_ = json.Unmarshal([]byte(body), &res)
	if code != 200 || !strings.HasPrefix(res.Credential, "dyc_") || res.Username != "bob" ||
		len(res.Computers) != 1 || res.Computers[0] != (computerLevel{Name: "alice", Level: "full"}) {
		t.Fatalf("approved poll: %d %s", code, body)
	}
	if code, body := sendBearer(t, "GET", ts.URL+"/api/me", res.Credential, ""); code != 200 || !strings.Contains(body, `"username":"bob"`) {
		t.Fatalf("me with the credential: %d %s", code, body)
	}
	creds := s.credentials.list(accountNamed(t, s, "bob").ID)
	if len(creds) != 1 || creds[0].Name != "deskyard CLI on test" || creds[0].Kind != "cli" {
		t.Fatalf("credentials: %+v", creds)
	}
	// The answer is given once.
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusGone {
		t.Fatalf("second poll: %d", code)
	}
}

func TestCLILoginDeny(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	start := startCLILogin(t, ts)
	bob := login(t, ts, "bob")
	if code, body := send(t, bob, "POST", ts.URL+"/api/cli-login/"+start.ID+"/deny", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("deny: %d %s", code, body)
	}
	if code, _ := send(t, bob, "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != 404 {
		t.Fatalf("approve after denial: %d", code)
	}
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusForbidden || !strings.Contains(body, `"error":"denied"`) {
		t.Fatalf("denied poll: %d %s", code, body)
	}
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusGone {
		t.Fatalf("poll after the denial was taken: %d", code)
	}
	if len(s.credentials.list("")) != 0 {
		t.Fatal("a denied login made a credential")
	}
}

func TestCLILoginExpires(t *testing.T) {
	s, ts := newTestServer(t)
	start := startCLILogin(t, ts)
	later := time.Now().Add(11 * time.Minute)
	s.cliLogins.setNow(func() time.Time { return later })
	admin := login(t, ts, "admin")
	if code := getJSON(t, admin, ts.URL+"/api/cli-login/"+start.ID, nil); code != 404 {
		t.Fatalf("view of an expired request: %d", code)
	}
	if code, _ := send(t, admin, "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != 404 {
		t.Fatalf("approving an expired request: %d", code)
	}
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusGone || !strings.Contains(body, "run deskyard login again") {
		t.Fatalf("expired poll: %d %s", code, body)
	}
}

func TestCLILoginExpiresAfterApproval(t *testing.T) {
	s, ts := newTestServer(t)
	start := startCLILogin(t, ts)
	if code, _ := send(t, login(t, ts, "admin"), "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("approve: %d", code)
	}
	later := time.Now().Add(11 * time.Minute)
	s.cliLogins.setNow(func() time.Time { return later })
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusGone {
		t.Fatalf("poll of an expired approval: %d", code)
	}
}

func TestCLILoginWrongPollSecret(t *testing.T) {
	_, ts := newTestServer(t)
	start := startCLILogin(t, ts)
	// Four failures in all here: a fifth would be throttled.
	for _, secret := range []string{"", start.PollSecret + "x"} {
		if code, _ := pollCLILogin(t, ts, start.ID, secret); code != http.StatusGone {
			t.Fatalf("poll with %q: %d", secret, code)
		}
	}
	if code, _ := pollCLILogin(t, ts, "nonsense", start.PollSecret); code != http.StatusGone {
		t.Fatalf("poll of an unknown request: %d", code)
	}
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusAccepted {
		t.Fatalf("poll with the right secret: %d", code)
	}
	if code, _ := send(t, login(t, ts, "admin"), "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("approve: %d", code)
	}
	if code, _ := pollCLILogin(t, ts, start.ID, "wrong"); code != http.StatusGone {
		t.Fatalf("approved poll with a wrong secret: %d", code)
	}
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != 200 || !strings.Contains(body, `"username":"admin"`) {
		t.Fatalf("approved poll: %d %s", code, body)
	}
}

func TestCLILoginObserverCannotApprove(t *testing.T) {
	s, ts := newTestServer(t)
	observeAlice(t, s)
	start := startCLILogin(t, ts)
	code, body := send(t, login(t, ts, "bob"), "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, "")
	if code != http.StatusForbidden || !strings.Contains(body, "observe-only access is for the browser") {
		t.Fatalf("observer approve: %d %s", code, body)
	}
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusAccepted {
		t.Fatalf("poll after the refused approval: %d", code)
	}
	// An observer may still deny.
	if code, _ := send(t, login(t, ts, "bob"), "POST", ts.URL+"/api/cli-login/"+start.ID+"/deny", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("observer deny: %d", code)
	}
}

func TestCLILoginApproveNeedsSameOrigin(t *testing.T) {
	s, ts := newTestServer(t)
	start := startCLILogin(t, ts)
	admin := login(t, ts, "admin")
	for _, origin := range []string{"", "http://evil.example"} {
		for _, action := range []string{"approve", "deny"} {
			if code, _ := send(t, admin, "POST", ts.URL+"/api/cli-login/"+start.ID+"/"+action, origin, ""); code != http.StatusForbidden {
				t.Fatalf("%s with origin %q: %d", action, origin, code)
			}
		}
	}
	// A client credential can't approve either: approval is the browser's.
	_, bearer := issueCredential(t, s, accountNamed(t, s, "admin"))
	if code, body := sendBearer(t, "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", bearer, ""); code != http.StatusForbidden || !strings.Contains(body, "approve in the browser") {
		t.Fatalf("approve with a credential: %d %s", code, body)
	}
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusAccepted {
		t.Fatalf("poll after refused approvals: %d", code)
	}
	// Viewing needs a session.
	if code := getJSON(t, http.DefaultClient, ts.URL+"/api/cli-login/"+start.ID, nil); code != 401 {
		t.Fatalf("view without a session: %d", code)
	}
}

func TestCLILoginOfDeletedAccountGone(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	start := startCLILogin(t, ts)
	if code, _ := send(t, login(t, ts, "bob"), "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("approve: %d", code)
	}
	if err := s.Accounts.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	createBob(t, s)
	if code, _ := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusGone {
		t.Fatalf("poll for a recreated account: %d", code)
	}
	if len(s.credentials.list("")) != 0 {
		t.Fatal("a credential was made for the recreated account")
	}
}

func TestCLILoginStartValidationAndThrottle(t *testing.T) {
	_, ts := newTestServer(t)
	for _, body := range []string{`{}`, `{"client":""}`, `{"client":"` + strings.Repeat("x", 101) + `"}`, `nonsense`} {
		if code, _ := send(t, http.DefaultClient, "POST", ts.URL+"/api/cli-login", "", body); code != 400 {
			t.Errorf("start %s: %d", body, code)
		}
	}
	throttled := false
	for range 20 {
		code, _ := send(t, http.DefaultClient, "POST", ts.URL+"/api/cli-login", "", `{"client":"c"}`)
		if code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("starting logins was never throttled")
	}
	// Wrong poll secrets are throttled too.
	throttled = false
	for range 20 {
		code, _ := pollCLILogin(t, ts, "nonsense", "wrong")
		if code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("bad polls were never throttled")
	}
}

func TestCLILoginCodes(t *testing.T) {
	cl := newCLILogins()
	seen := map[string]bool{}
	for range 50 {
		id, code, secret := cl.start("c", "1.2.3.4")
		if id == "" || secret == "" || seen[id] || len(code) != 9 || code[4] != '-' {
			t.Fatalf("start: %q %q %q", id, code, secret)
		}
		seen[id] = true
	}
}

// A credential that can't be saved answers 500 and keeps the approval, so
// the CLI's next poll gets it once saving works again.
func TestCLILoginKeepsApprovalWhenSavingFails(t *testing.T) {
	s, ts := newTestServer(t)
	fullOnAlice(t, s)
	start := startCLILogin(t, ts)
	bob := login(t, ts, "bob")
	if code, body := send(t, bob, "POST", ts.URL+"/api/cli-login/"+start.ID+"/approve", ts.URL, ""); code != http.StatusNoContent {
		t.Fatalf("approve: %d %s", code, body)
	}
	saved := s.credentials.path
	s.credentials.path = blockedStatePath(t)
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != http.StatusInternalServerError {
		t.Fatalf("poll while saving fails: %d %s", code, body)
	}
	s.credentials.path = saved
	if code, body := pollCLILogin(t, ts, start.ID, start.PollSecret); code != 200 || !strings.Contains(body, `"credential":"dyc_`) {
		t.Fatalf("poll once saving works: %d %s", code, body)
	}
	if n := len(s.credentials.list(accountNamed(t, s, "bob").ID)); n != 1 {
		t.Fatalf("%d credentials, want 1", n)
	}
}
