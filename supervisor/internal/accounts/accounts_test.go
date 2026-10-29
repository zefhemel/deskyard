package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if _, err := s.Create(NewAccount{Username: "anna", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(NewAccount{Username: "Anna", Password: "pw"}); !errors.Is(err, ErrExists) {
		t.Fatal("case-insensitive duplicate allowed")
	}
	var notified []string
	s.OnChange = func(n ...string) { notified = append(notified, n...) }
	_ = s.SetPassword("anna", "new")
	if a, _ := s.Get("anna"); a.SessionEpoch != 1 {
		t.Fatal("epoch not bumped")
	}
	_ = s.SetAccess("c1", map[string]auth.Level{"anna": auth.LevelFull})
	if a, _ := s.Get("anna"); !a.HasAccess("c1") || a.HasAccess("c2") {
		t.Fatal("access")
	}
	if got := s.WithAccess("c1"); len(got) != 1 || got["anna"] != auth.LevelFull {
		t.Fatal(got)
	}
	_ = s.Forget("c1")
	if a, _ := s.Get("anna"); a.HasAccess("c1") {
		t.Fatal("forget")
	}
	if len(notified) < 2 {
		t.Fatal("no change notifications")
	}
}

func TestRetainDropsGrantsOfUndefinedComputers(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	_, _ = s.EnsureAdmin("admin", "x")
	if _, err := s.Create(NewAccount{Username: "anna", Password: "pw", Grants: []Grant{{Computer: "alice", Level: auth.LevelFull}, {Computer: "gone", Level: auth.LevelObserve}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Retain(func(name string) bool { return name == "alice" }); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := Load(dir)
	a, _ := reloaded.Get("anna")
	if !a.HasAccess("alice") || a.HasAccess("gone") {
		t.Fatalf("grants after retain: %v", a.Grants)
	}
	if !strings.Contains(readFile(t, dir), `"grants"`) {
		t.Fatal("grants not stored under grants")
	}
}

func readFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLegacyComputersBecomeFullGrants(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"accounts":[{"username":"bob","passwordHash":"x","environments":["alice"],"tokenEpoch":2}]}`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("bob")
	if a.Level("alice") != auth.LevelFull || a.ID == "" {
		t.Fatalf("legacy account: %+v", a)
	}
	disk := readFile(t, dir)
	if !strings.Contains(disk, `"grants"`) || strings.Contains(disk, `"environments"`) || strings.Contains(disk, "tokenEpoch") {
		t.Fatalf("not rewritten with grants: %s", disk)
	}
	reloaded, _ := Load(dir)
	if b, _ := reloaded.Get("bob"); b.ID != a.ID || b.Level("alice") != auth.LevelFull {
		t.Fatalf("reload changed the account: %+v", b)
	}
}

func TestTokenAccountHasNoPassword(t *testing.T) {
	s, _ := Load(t.TempDir())
	a, err := s.Create(NewAccount{Username: "sanne", Grants: []Grant{{Computer: "alice", Level: auth.LevelObserve}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.HasPassword() || a.PasswordHash != "" || a.ID == "" || a.Level("alice") != auth.LevelObserve || a.HasAccess("alice") {
		t.Fatalf("token account: %+v", a)
	}
	if _, err := s.Create(NewAccount{Username: "piet", Grants: []Grant{{Computer: "alice", Level: "admin"}}}); err == nil {
		t.Fatal("invalid level accepted")
	}
}

func TestLastPasswordAdminGuard(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	if _, err := s.Create(NewAccount{Username: "robot", Admin: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Update("admin", func(a *Account) { a.Admin = false }); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote: %v", err)
	}
	if _, err := s.Update("admin", func(a *Account) { a.PasswordHash = "" }); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("drop password: %v", err)
	}
	if _, err := s.DeleteWhere(func(a Account) bool { return a.Admin }); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete where: %v", err)
	}
	gone, err := s.DeleteWhere(func(a Account) bool { return !a.HasPassword() })
	if err != nil || len(gone) != 1 || gone[0] != "robot" {
		t.Fatalf("delete token admin: %v %v", gone, err)
	}
}

func TestPasswordAdminsCantExpire(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	soon := time.Now().Add(time.Hour)
	if _, err := s.Create(NewAccount{Username: "boss", Password: "y", Admin: true, Expires: &soon}); !errors.Is(err, ErrAdminExpires) {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Update("admin", func(a *Account) { a.Expires = &soon }); !errors.Is(err, ErrAdminExpires) {
		t.Fatalf("expire admin: %v", err)
	}
	if _, err := s.Create(NewAccount{Username: "bob", Password: "y", Expires: &soon}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("bob", func(a *Account) { a.Admin = true }); !errors.Is(err, ErrAdminExpires) {
		t.Fatalf("promote expiring account: %v", err)
	}
	if _, err := s.Create(NewAccount{Username: "robot", Admin: true, Expires: &soon}); err != nil {
		t.Fatalf("token admin with expiry: %v", err)
	}
}

func TestSetAccessLevels(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	if _, err := s.Create(NewAccount{Username: "bob", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	var notified []string
	s.OnChange = func(n ...string) { notified = append(notified, n...) }
	if err := s.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelObserve}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("bob"); a.Level("alice") != auth.LevelObserve || a.HasAccess("alice") {
		t.Fatalf("observe: %+v", a)
	}
	if got := s.WithAccess("alice"); len(got) != 1 || got["bob"] != auth.LevelObserve {
		t.Fatalf("with access: %v", got)
	}
	if err := s.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelFull}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("bob"); !a.HasAccess("alice") || len(a.Grants) != 1 {
		t.Fatalf("full: %+v", a)
	}
	if err := s.SetAccess("alice", map[string]auth.Level{}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("bob"); a.Level("alice") != auth.LevelNone || len(a.Grants) != 0 {
		t.Fatalf("none: %+v", a)
	}
	if len(notified) != 3 {
		t.Fatalf("notified %v", notified)
	}
	if err := s.SetAccess("alice", map[string]auth.Level{"bob": "write"}); err == nil {
		t.Fatal("invalid level accepted")
	}
}

// Lowering a grant keeps the account's browser sessions: the connections
// the new level doesn't allow are closed, not the account's other ones.
func TestLoweredGrantKeepsSessionEpoch(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.Create(NewAccount{Username: "bob", Password: "pw", Grants: []Grant{{Computer: "alice", Level: auth.LevelFull}}})
	a, _ := s.Update("bob", func(a *Account) { a.Grants = append(a.Grants, Grant{Computer: "carol", Level: auth.LevelObserve}) })
	if a.SessionEpoch != 0 {
		t.Fatal("added grant bumped the epoch")
	}
	a, _ = s.Update("bob", func(a *Account) { a.Grants[0].Level = auth.LevelObserve })
	if a.SessionEpoch != 0 {
		t.Fatal("downgrade bumped the epoch")
	}
	a, _ = s.Update("bob", func(a *Account) { a.Grants = a.Grants[:1] })
	if a.SessionEpoch != 0 {
		t.Fatal("removed grant bumped the epoch")
	}
	if a, _ = s.BumpSessionEpoch("bob"); a.SessionEpoch != 1 {
		t.Fatal("bump")
	}
}

// A bump starts from the session epoch held unsaved, when it is ahead.
func TestBumpStartsFromHeldSessionEpoch(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.Create(NewAccount{Username: "bob", Password: "pw"})
	s.HeldSessionEpoch = func(a Account) int { return 3 }
	if a, _ := s.BumpSessionEpoch("bob"); a.SessionEpoch != 4 {
		t.Errorf("sign out everywhere: epoch %d, want 4", a.SessionEpoch)
	}
	if a, _ := s.Update("bob", func(a *Account) { a.Admin = true }); a.SessionEpoch != 5 {
		t.Errorf("admin change: epoch %d, want 5", a.SessionEpoch)
	}
	if a, _ := s.Update("bob", func(a *Account) { a.Description = "Bob" }); a.SessionEpoch != 5 {
		t.Errorf("description change: epoch %d, want 5", a.SessionEpoch)
	}
}

func TestLoweredLevels(t *testing.T) {
	before := []Grant{{Computer: "alice", Level: auth.LevelFull}, {Computer: "beta", Level: auth.LevelFull}, {Computer: "carol", Level: auth.LevelObserve}}
	after := []Grant{{Computer: "alice", Level: auth.LevelObserve}, {Computer: "beta", Level: auth.LevelFull}, {Computer: "dora", Level: auth.LevelFull}}
	got := Lowered(before, after)
	if len(got) != 2 || got["alice"] != auth.LevelObserve || got["carol"] != auth.LevelNone {
		t.Fatalf("lowered %v", got)
	}
	capped := Capped(after, map[string]auth.Level{"beta": auth.LevelObserve, "dora": auth.LevelNone})
	if grantLevel(capped, "alice") != auth.LevelObserve || grantLevel(capped, "beta") != auth.LevelObserve || grantLevel(capped, "dora") != auth.LevelNone || len(after) != 3 {
		t.Fatalf("capped %v (from %v)", capped, after)
	}
}

func TestRecreatedUsernameGetsNewID(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	first, _ := s.Create(NewAccount{Username: "bob", Password: "pw"})
	if _, err := s.Update("bob", func(a *Account) { a.ID = "forged" }); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("bob"); a.ID != first.ID {
		t.Fatal("update changed the ID")
	}
	if err := s.Delete("bob"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.Create(NewAccount{Username: "bob", Password: "pw"})
	if second.ID == "" || second.ID == first.ID {
		t.Fatalf("IDs %q %q", first.ID, second.ID)
	}
	if admin, _ := s.Get("admin"); admin.ID == "" {
		t.Fatal("first admin has no ID")
	}
}

func TestShareAndExpiry(t *testing.T) {
	s, _ := Load(t.TempDir())
	past := time.Now().Add(-time.Minute)
	a, _ := s.Create(NewAccount{Username: "sanne", SharedBy: &Sharer{Username: "zef", ID: "z1"}, Expires: &past,
		Grants: []Grant{{Computer: "alice", Level: auth.LevelFull}}})
	if !a.IsShare() || !a.Expired(time.Now()) || a.Expired(past.Add(-time.Second)) {
		t.Fatalf("share: %+v", a)
	}
	if names := a.GrantNames(); len(names) != 1 || names[0] != "alice" {
		t.Fatal(names)
	}
}

func TestSetAccessLeavesAdminsAndSharesAlone(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.EnsureAdmin("admin", "x")
	admin, _ := s.Get("admin")
	if _, err := s.Create(NewAccount{Username: "sanne", Grants: []Grant{{Computer: "alice", Level: auth.LevelObserve}},
		SharedBy: &Sharer{Username: "admin", ID: admin.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(NewAccount{Username: "bob", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccess("alice", map[string]auth.Level{"bob": auth.LevelFull, "admin": auth.LevelFull}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("admin"); len(a.Grants) != 0 {
		t.Fatalf("an admin got a grant: %+v", a.Grants)
	}
	if got := s.WithAccess("alice"); len(got) != 1 || got["bob"] != auth.LevelFull {
		t.Fatalf("with access lists shares or admins: %v", got)
	}
	if err := s.SetAccess("alice", nil); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Get("sanne"); a.Level("alice") != auth.LevelObserve {
		t.Fatalf("setting access changed a share: %+v", a.Grants)
	}
	if a, _ := s.Get("bob"); len(a.Grants) != 0 {
		t.Fatalf("bob kept %+v", a.Grants)
	}
}

// Grants saved before computers were called computers name them under
// "environment": they load, the file is rewritten under "computer", and a
// grant of a computer that no longer exists is kept, as before.
func TestGrantReadsLegacyEnvironmentField(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"accounts":[{"id":"a1","username":"bob","passwordHash":"x","admin":false,"sessionEpoch":0,"created":"2026-10-01T00:00:00Z",` +
		`"grants":[{"environment":"alice","level":"full"},{"environment":"gone","level":"observe"},{"computer":"carl","environment":"ignored","level":"full"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("bob")
	want := []Grant{{Computer: "alice", Level: auth.LevelFull}, {Computer: "gone", Level: auth.LevelObserve}, {Computer: "carl", Level: auth.LevelFull}}
	if len(a.Grants) != len(want) {
		t.Fatalf("grants %+v", a.Grants)
	}
	for i := range want {
		if a.Grants[i] != want[i] {
			t.Fatalf("grants %+v, want %+v", a.Grants, want)
		}
	}
	disk := readFile(t, dir)
	if strings.Contains(disk, `"environment"`) || !strings.Contains(disk, `"computer": "alice"`) || !strings.Contains(disk, `"computer": "gone"`) {
		t.Fatalf("not rewritten under computer: %s", disk)
	}
	if _, err := s.Update("bob", func(a *Account) { a.Description = "x" }); err != nil {
		t.Fatal(err)
	}
	if disk := readFile(t, dir); strings.Contains(disk, `"environment"`) || !strings.Contains(disk, `"computer": "carl"`) {
		t.Fatalf("save wrote %s", disk)
	}
	reloaded, _ := Load(dir)
	if b, _ := reloaded.Get("bob"); len(b.Grants) != 3 || b.Level("gone") != auth.LevelObserve {
		t.Fatalf("reload: %+v", b)
	}
}
