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
	s.Issue(w, "zef", 3)
	r := &http.Request{Header: http.Header{"Cookie": w.Header()["Set-Cookie"]}}
	if u, e, refresh, ok := s.Read(r); !ok || u != "zef" || e != 3 || refresh {
		t.Fatalf("%v %v %v %v", u, e, refresh, ok)
	}
	now = now.Add(2 * 24 * time.Hour)
	if _, _, refresh, ok := s.Read(r); !ok || !refresh {
		t.Fatal("should be valid and due for refresh")
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, _, _, ok := s.Read(r); ok {
		t.Fatal("expired session accepted")
	}
	other := &Sessions{Key: []byte("other")}
	if _, _, _, ok := other.Read(r); ok {
		t.Fatal("cookie signed with another key accepted")
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
	admin  bool
	access map[string]bool
}

func (s subject) IsAdmin() bool            { return s.admin }
func (s subject) HasAccess(id string) bool { return s.admin || s.access[id] }

func TestCanMatchesThePermissionsTable(t *testing.T) {
	admin := subject{admin: true}
	anna := subject{access: map[string]bool{"alice": true}}
	rows := []struct {
		action  Action
		admin   bool
		withAcc bool
		without bool
	}{
		{View, true, true, false},
		{Power, true, true, false},
		{Manage, true, false, false},
		{ManageAccounts, true, false, false},
		{CreateToken, true, true, false},
		{RevokeAnyToken, true, false, false},
	}
	for _, r := range rows {
		if Can(admin, r.action, "bob") != r.admin || Can(anna, r.action, "alice") != r.withAcc || Can(anna, r.action, "bob") != r.without {
			t.Errorf("action %d", r.action)
		}
	}
}
