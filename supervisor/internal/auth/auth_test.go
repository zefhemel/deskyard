package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h := HashPassword("correct horse")
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong") || CheckPassword("garbage", "x") {
		t.Fatal("password check")
	}
}

// TestProductionParams guards the cost of real hashes: the other packages'
// tests lower it with UseTestParams.
func TestProductionParams(t *testing.T) {
	h := HashPassword("pw")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash parameters changed: %s", h)
	}
	// A hash made with the test parameters still checks.
	saved := []any{argonMemory, argonTime, argonThreads}
	UseTestParams()
	cheap := HashPassword("pw")
	argonMemory, argonTime, argonThreads = saved[0].(uint32), saved[1].(uint32), saved[2].(uint8)
	if !strings.HasPrefix(cheap, "$argon2id$v=19$m=64,t=1,p=1$") || !CheckPassword(cheap, "pw") || CheckPassword(cheap, "x") {
		t.Fatalf("test hash: %s", cheap)
	}
}

func TestSessionRoundTripAndExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := &Sessions{Key: []byte("k"), Now: func() time.Time { return now }}
	w := httptest.NewRecorder()
	s.Issue(w, "zef", "acct1234", 3)
	r := &http.Request{Header: http.Header{"Cookie": w.Header()["Set-Cookie"]}}
	if c, ok := s.Claims(r); !ok || c.Username != "zef" || c.AccountID != "acct1234" || c.Epoch != 3 || c.Refresh {
		t.Fatalf("%+v %v", c, ok)
	}
	now = now.Add(2 * 24 * time.Hour)
	if c, ok := s.Claims(r); !ok || !c.Refresh {
		t.Fatal("should be valid and due for refresh")
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, ok := s.Claims(r); ok {
		t.Fatal("expired session accepted")
	}
	other := &Sessions{Key: []byte("other")}
	if _, ok := other.Claims(r); ok {
		t.Fatal("cookie signed with another key accepted")
	}
}

// A session of an account that expires ends with it: the cookie and its
// claims expire then, also when renewed.
func TestSessionCappedAtNotAfter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := &Sessions{Key: []byte("k"), Now: func() time.Time { return now }}
	until := now.Add(3 * time.Hour)
	w := httptest.NewRecorder()
	s.IssueUntil(w, "sanne", "acct1234", 0, until)
	cookie := w.Result().Cookies()[0]
	if !cookie.Expires.Equal(until) {
		t.Fatalf("cookie expires %v, want %v", cookie.Expires, until)
	}
	r := &http.Request{Header: http.Header{"Cookie": w.Header()["Set-Cookie"]}}
	c, ok := s.Claims(r)
	if !ok || !c.Expires.Equal(until) {
		t.Fatalf("%+v %v", c, ok)
	}
	c.NotAfter = until
	w = httptest.NewRecorder()
	s.Refresh(w, c)
	if got := w.Result().Cookies()[0].Expires; !got.Equal(until) {
		t.Fatalf("renewed cookie expires %v, want %v", got, until)
	}
	now = until
	if _, ok := s.Claims(r); ok {
		t.Fatal("a session outlived its account's expiry")
	}
}

func TestThrottle(t *testing.T) {
	now := time.Unix(0, 0)
	th := &Throttle{Now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		if !th.Allow("u:zef", "ip:1") {
			t.Fatalf("attempt %d refused", i)
		}
		th.Fail("u:zef", "ip:1")
	}
	if th.Allow("u:zef") || th.Allow("ip:1") {
		t.Fatal("6th attempt allowed at once")
	}
	if !th.Allow("u:anna", "ip:2") {
		t.Fatal("unrelated keys throttled")
	}
	now = now.Add(11 * time.Second)
	if !th.Allow("u:zef", "ip:1") {
		t.Fatal("not allowed after 10 s")
	}
}

type subject struct {
	admin, share, password bool
	levels                 map[string]Level
}

func (s subject) IsAdmin() bool           { return s.admin }
func (s subject) IsShare() bool           { return s.share }
func (s subject) HasPassword() bool       { return s.password }
func (s subject) Level(name string) Level { return s.levels[name] }
func (s subject) Environments() []string {
	var out []string
	for name := range s.levels {
		out = append(out, name)
	}
	return out
}

func TestCanMatchesThePermissionsTable(t *testing.T) {
	admin := subject{admin: true}
	anna := subject{levels: map[string]Level{"alice": LevelFull}}
	rows := []struct {
		action  Action
		admin   bool
		withAcc bool
		without bool
	}{
		{Observe, true, true, false},
		{View, true, true, false},
		{Power, true, true, false},
		{Manage, true, false, false},
		{ManageAccounts, true, false, false},
		{Share, true, true, false},
	}
	for _, r := range rows {
		if Can(admin, r.action, "bob") != r.admin || Can(anna, r.action, "alice") != r.withAcc || Can(anna, r.action, "bob") != r.without {
			t.Errorf("action %d", r.action)
		}
	}
}

func TestCanByLevel(t *testing.T) {
	observer := subject{levels: map[string]Level{"alice": LevelObserve}}
	share := subject{share: true, levels: map[string]Level{"alice": LevelFull}}
	adminShare := subject{admin: true, share: true}
	rows := []struct {
		name    string
		who     subject
		action  Action
		allowed bool
	}{
		{"observer observes", observer, Observe, true},
		{"observer views", observer, View, false},
		{"observer powers", observer, Power, false},
		{"observer shares", observer, Share, false},
		{"observer manages", observer, Manage, false},
		{"share observes", share, Observe, true},
		{"share views", share, View, true},
		{"share powers", share, Power, true},
		{"share shares", share, Share, false},
		{"admin share shares", adminShare, Share, true},
	}
	for _, r := range rows {
		if got := Can(r.who, r.action, "alice"); got != r.allowed {
			t.Errorf("%s: %v", r.name, got)
		}
	}
	if Can(observer, Observe, "") {
		t.Error("observe without an environment")
	}
	for _, l := range []Level{LevelNone, "admin", "FULL"} {
		if ValidLevel(l) {
			t.Errorf("valid level %q", l)
		}
	}
	if !ValidLevel(LevelFull) || !ValidLevel(LevelObserve) {
		t.Error("levels not valid")
	}
}

// Check says why: environments without access look missing, and a refusal
// full access would lift says so.
func TestCheckReasons(t *testing.T) {
	observer := subject{levels: map[string]Level{"alice": LevelObserve}}
	full := subject{levels: map[string]Level{"alice": LevelFull}}
	share := subject{share: true, levels: map[string]Level{"alice": LevelObserve}}
	rows := []struct {
		name   string
		who    subject
		action Action
		env    string
		want   error
	}{
		{"no grant", full, View, "bob", ErrNoAccess},
		{"no grant, admin action", full, Manage, "bob", ErrNoAccess},
		{"observer views", observer, View, "alice", ErrObserveOnly},
		{"observer powers", observer, Power, "alice", ErrObserveOnly},
		{"observer shares", observer, Share, "alice", ErrObserveOnly},
		{"observer manages", observer, Manage, "alice", ErrAdminOnly},
		{"full manages", full, Manage, "alice", ErrAdminOnly},
		{"full manages accounts", full, ManageAccounts, "", ErrAdminOnly},
		{"share shares", share, Share, "alice", ErrSharesCantShare},
	}
	for _, r := range rows {
		if got := Check(r.who, r.action, r.env); got != r.want {
			t.Errorf("%s: %v, want %v", r.name, got, r.want)
		}
	}
}

func TestUseCLIAndWebDAV(t *testing.T) {
	observer := subject{password: true, levels: map[string]Level{"alice": LevelObserve}}
	full := subject{password: true, levels: map[string]Level{"alice": LevelObserve, "beta": LevelFull}}
	token := subject{levels: map[string]Level{"beta": LevelFull}}
	share := subject{share: true, levels: map[string]Level{"beta": LevelFull}}
	tokenAdmin := subject{admin: true}
	if Check(observer, UseCLI, "") != ErrBrowserOnly || !Can(full, UseCLI, "") || !Can(tokenAdmin, UseCLI, "") || !Can(share, UseCLI, "") {
		t.Error("UseCLI: an admin, or full somewhere")
	}
	rows := []struct {
		name string
		who  subject
		env  string
		want error
	}{
		{"full", full, "beta", nil},
		{"at all", observer, "", nil},
		{"observer", full, "alice", ErrObserveOnly},
		{"no grant", full, "gamma", ErrNoAccess},
		{"token account", token, "beta", ErrPasswordOnly},
		{"share", share, "beta", ErrPasswordOnly},
		{"token admin", tokenAdmin, "beta", ErrPasswordOnly},
		{"password admin", subject{admin: true, password: true}, "beta", nil},
	}
	for _, r := range rows {
		if got := Check(r.who, WebDAV, r.env); got != r.want {
			t.Errorf("WebDAV %s: %v, want %v", r.name, got, r.want)
		}
	}
}

func TestLevelAtLeast(t *testing.T) {
	order := []Level{LevelNone, LevelObserve, LevelFull}
	for i, a := range order {
		for j, b := range order {
			if a.AtLeast(b) != (i >= j) {
				t.Errorf("%q at least %q: %v", a, b, a.AtLeast(b))
			}
		}
	}
}
