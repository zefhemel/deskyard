package accounts

import (
	"errors"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

func TestFirstAdminAndLastAdminRules(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	pw, err := s.EnsureAdmin("admin", "")
	if err != nil || len(pw) != 24 {
		t.Fatalf("%q %v", pw, err)
	}
	if again, _ := s.EnsureAdmin("admin", ""); again != "" {
		t.Fatal("second EnsureAdmin created another password")
	}
	a, _ := s.Get("admin")
	if !a.Admin || !auth.CheckPassword(a.PasswordHash, pw) {
		t.Fatal("admin not usable")
	}
	if err := s.Delete("admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatal(err)
	}
	if _, err := s.Update("admin", func(a *Account) { a.Admin = false }); !errors.Is(err, ErrLastAdmin) {
		t.Fatal(err)
	}
	// Reload from disk.
	s2, _ := Load(dir)
	if _, ok := s2.Get("admin"); !ok {
		t.Fatal("not persisted")
	}
}

func TestPasswordChangeBumpsEpochAndAccess(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	if _, err := s.Create("anna", "pw", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Anna", "pw", false, nil); !errors.Is(err, ErrExists) {
		t.Fatal("case-insensitive duplicate allowed")
	}
	var notified []string
	s.OnChange = func(n ...string) { notified = append(notified, n...) }
	_ = s.SetPassword("anna", "new")
	if a, _ := s.Get("anna"); a.SessionEpoch != 1 {
		t.Fatal("epoch not bumped")
	}
	_ = s.SetAccess("c1", []string{"anna"})
	if a, _ := s.Get("anna"); !a.HasAccess("c1") || a.HasAccess("c2") {
		t.Fatal("access")
	}
	if got := s.WithAccess("c1"); len(got) != 1 || got[0] != "anna" {
		t.Fatal(got)
	}
	_ = s.Forget(func(id string) bool { return id == "c1" })
	if a, _ := s.Get("anna"); a.HasAccess("c1") {
		t.Fatal("forget")
	}
	if len(notified) < 2 {
		t.Fatal("no change notifications")
	}
}
