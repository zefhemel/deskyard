package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// A change that can't be saved leaves the file as it was and changes the
// accounts only by what it revokes, which holds until a later write saves it.
func TestFailedMutationHoldsOnlyRevocations(t *testing.T) {
	without := func(drop func(a *Account) bool) func([]Account) []Account {
		return func(before []Account) []Account {
			var out []Account
			for _, a := range before {
				if !drop(&a) {
					out = append(out, a)
				}
			}
			return out
		}
	}
	unchanged := func(before []Account) []Account { return before }
	anna := func(change func(a *Account)) func([]Account) []Account {
		return without(func(a *Account) bool {
			if a.Username == "anna" {
				change(a)
			}
			return false
		})
	}
	for _, tc := range []struct {
		op      string
		want    func(before []Account) []Account
		notices int
	}{
		{"ensure", unchanged, 0},
		{"create", unchanged, 0},
		{"promote", unchanged, 0},
		{"update", anna(func(a *Account) { a.Grants = []Grant{{Environment: "old", Level: auth.LevelObserve}} }), 1},
		{"delete", without(func(a *Account) bool { return a.Username == "anna" }), 1},
		{"deleteWhere", without(func(a *Account) bool { return a.Username == "anna" }), 1},
		{"bump", anna(func(a *Account) { a.SessionEpoch++ }), 1},
		{"password", unchanged, 0},
		{"grant", unchanged, 0},
		{"access", anna(func(a *Account) { a.Grants = []Grant{} }), 1},
		{"forget", anna(func(a *Account) { a.Grants = []Grant{} }), 0},
		{"login", unchanged, 0},
	} {
		t.Run(tc.op, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.op != "ensure" {
				if _, err := s.EnsureAdmin("admin", "password"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Create(NewAccount{Username: "anna", Password: "password", Grants: []Grant{{Environment: "old", Level: auth.LevelFull}}}); err != nil {
					t.Fatal(err)
				}
			}
			before := s.List()
			disk, _ := os.ReadFile(s.path)
			notices := 0
			s.OnChange = func(...string) { notices++ }
			p := filepath.Join(dir, "block")
			if err := os.WriteFile(p, nil, 0600); err != nil {
				t.Fatal(err)
			}
			path := s.path
			s.path = filepath.Join(p, "accounts.json")
			err = nil
			switch tc.op {
			case "ensure":
				_, err = s.EnsureAdmin("admin", "password")
			case "create":
				_, err = s.Create(NewAccount{Username: "bob", Password: "password"})
			case "promote":
				_, err = s.Update("anna", func(a *Account) { a.Admin = true })
			case "update":
				_, err = s.Update("anna", func(a *Account) {
					a.Description = "changed"
					a.Grants = []Grant{{Environment: "old", Level: auth.LevelObserve}, {Environment: "new", Level: auth.LevelFull}}
				})
			case "delete":
				err = s.Delete("anna")
			case "deleteWhere":
				_, err = s.DeleteWhere(func(a Account) bool { return a.Username == "anna" })
			case "bump":
				_, err = s.BumpSessionEpoch("anna")
			case "password":
				err = s.SetPassword("anna", "other")
			case "grant":
				err = s.SetAccess("new", map[string]auth.Level{"anna": auth.LevelObserve})
			case "access":
				err = s.SetAccess("old", nil)
			case "forget":
				err = s.Forget("old")
			case "login":
				err = s.TouchLogin("anna")
			}
			if !errors.Is(err, ErrNotSaved) {
				t.Fatalf("expected ErrNotSaved: %v", err)
			}
			want := tc.want(before)
			if got := s.List(); !reflect.DeepEqual(got, want) {
				t.Errorf("accounts after the failed change:\n%+v\nwant\n%+v", got, want)
			}
			if notices != tc.notices {
				t.Errorf("%d notices, want %d", notices, tc.notices)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "accounts.json")); string(got) != string(disk) {
				t.Error("disk changed")
			}
			if s.Unsaved() != !reflect.DeepEqual(want, before) {
				t.Errorf("unsaved: %v", s.Unsaved())
			}
			// Saving works again: the next write saves what it held.
			s.path = path
			if err := s.Flush(); err != nil {
				t.Fatal(err)
			}
			if s.Unsaved() {
				t.Error("still unsaved after a flush")
			}
			again, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := again.List(); !reflect.DeepEqual(got, want) {
				t.Errorf("saved accounts:\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// A later change saved after one that couldn't be saved decides anew, and
// starts from what was held: a sign-out held unsaved isn't undone.
func TestChangeAfterUnsavedRevocation(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	_, _ = s.EnsureAdmin("admin", "password")
	_, _ = s.Create(NewAccount{Username: "anna", Password: "password", Grants: []Grant{{Environment: "alice", Level: auth.LevelFull}}})
	path := s.path
	s.path = blocked(t, dir)
	if _, err := s.BumpSessionEpoch("anna"); err == nil {
		t.Fatal("saved")
	}
	if err := s.SetAccess("alice", nil); err == nil {
		t.Fatal("saved")
	}
	// Refused for another reason: what was held stays.
	if _, err := s.Update("anna", func(a *Account) { a.Grants = []Grant{{Environment: "alice", Level: "admin"}} }); err == nil || errors.Is(err, ErrNotSaved) {
		t.Fatalf("invalid change: %v", err)
	}
	if a, _ := s.Get("anna"); a.Level("alice") != auth.LevelNone || a.SessionEpoch != 1 {
		t.Fatalf("held: %+v", a)
	}
	s.path = path
	if err := s.SetAccess("alice", map[string]auth.Level{"anna": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	again, _ := Load(dir)
	if a, _ := again.Get("anna"); a.Level("alice") != auth.LevelObserve || a.SessionEpoch != 1 {
		t.Fatalf("saved: %+v", a)
	}
	if s.Unsaved() {
		t.Error("still unsaved")
	}
	if a, _ := s.BumpSessionEpoch("anna"); a.SessionEpoch != 2 {
		t.Fatalf("a bump after the held one: epoch %d", a.SessionEpoch)
	}
}

// A share is usable while its sharer, by ID, is full on its environment.
func TestUsableAndOrphans(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "password")
	bob, _ := s.Create(NewAccount{Username: "bob", Password: "pw", Grants: []Grant{{Environment: "alice", Level: auth.LevelFull}}})
	share, _ := s.Create(NewAccount{Username: "sanne", SharedBy: &Sharer{Username: "bob", ID: bob.ID},
		Grants: []Grant{{Environment: "alice", Level: auth.LevelObserve}}})
	now := time.Now()
	if _, ok := s.Usable("sanne", share.ID, now); !ok {
		t.Fatal("the share isn't usable")
	}
	if _, ok := s.Usable("sanne", "other", now); ok {
		t.Fatal("usable by another ID")
	}
	if _, ok := s.Usable("bob", bob.ID, now); !ok {
		t.Fatal("bob isn't usable")
	}
	if gone, err := s.DeleteOrphans(now); err != nil || len(gone) != 0 {
		t.Fatalf("orphans while bob is full: %v %v", gone, err)
	}
	if err := s.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Usable("sanne", share.ID, now); ok {
		t.Fatal("the share is usable after its sharer lost full access")
	}
	if gone, err := s.DeleteOrphans(now); err != nil || len(gone) != 1 || gone[0] != "sanne" {
		t.Fatalf("orphans: %v %v", gone, err)
	}
	later := now.Add(time.Hour)
	_, _ = s.Update("bob", func(a *Account) { a.Expires = &later })
	if _, ok := s.Usable("bob", bob.ID, later.Add(time.Second)); ok {
		t.Fatal("usable after it expired")
	}
}

func TestLoadRestrictsWideAccountsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(p, []byte(`{"accounts":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

// blocked is a path in dir that can't be written.
func blocked(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "block")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p, "accounts.json")
}
