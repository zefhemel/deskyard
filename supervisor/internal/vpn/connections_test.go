package vpn

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func ptr(s string) *string { return &s }
func validInput() Input {
	return Input{Name: "Home", Provider: "nordvpn", Country: "Netherlands", Username: ptr("test-user-never-public"), Password: ptr("test-password-never-public")}
}
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestConnectionsSecretsAndReload(t *testing.T) {
	s, dir := newTestStore(t)
	c, err := s.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Revision != 1 || !c.CredentialsConfigured {
		t.Fatalf("invalid public connection: %+v", c)
	}
	files, err := s.SecretFiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{files.Username: "test-user-never-public", files.Password: "test-password-never-public"} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Fatalf("credential contents mismatch: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("file permissions: %v %v", info, err)
		}
		info, err = os.Stat(filepath.Dir(path))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory permissions: %v %v", info, err)
		}
	}
	edit := validInput()
	edit.Username = nil
	edit.Password = nil
	edit.Name = "Travel"
	updated, err := s.Update(c.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != c.ID || updated.Revision != 2 {
		t.Fatalf("identity/revision: %+v", updated)
	}
	again, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := again.Get(c.ID)
	if err != nil || saved != updated {
		t.Fatalf("reload: %+v %v", saved, err)
	}
	retained, err := again.SecretFiles(c.ID)
	if err != nil || retained != files {
		t.Fatalf("credentials not retained: %v", err)
	}
	list, err := again.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v", err)
	}
	for _, v := range []any{saved, list, files} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("never-public")) || bytes.Contains(b, []byte(dir)) {
			t.Fatal("credentials or secret paths leaked")
		}
	}
	metadata, err := os.ReadFile(filepath.Join(dir, "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "networks.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata permissions: %v %v", info, err)
	}
	if bytes.Contains(metadata, []byte("never-public")) {
		t.Fatal("metadata contains credentials")
	}
	if _, err = json.Marshal(validInput()); err == nil {
		t.Fatal("write-only input can be serialized")
	}
	if err := again.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Get(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if _, err := os.Stat(files.Username); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted secret remains: %v", err)
	}
}

func TestConnectionValidationAndDuplicateNames(t *testing.T) {
	s, _ := newTestStore(t)
	for _, mutate := range []func(*Input){func(i *Input) { i.Username = nil }, func(i *Input) { i.Password = ptr("") }, func(i *Input) { i.Provider = "arbitrary" }, func(i *Input) { i.Country = "Atlantis" }, func(i *Input) { i.Name = "  " }, func(i *Input) { i.Username = ptr("u\nOTHER=value") }} {
		in := validInput()
		mutate(&in)
		if _, err := s.Create(in); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected validation: %v", err)
		}
	}
	first, err := s.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := validInput()
	duplicate.Name = " HOME "
	if _, err := s.Create(duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	duplicate.Name = "Second"
	second, err := s.Create(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	duplicate.Name = "home"
	if _, err := s.Update(second.ID, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate update: %v", err)
	}
	duplicate = validInput()
	duplicate.Password = ptr("")
	if _, err := s.Update(first.ID, duplicate); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty update: %v", err)
	}
	for _, id := range []string{"../x", "missing"} {
		if err := s.Delete(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing delete: %v", err)
		}
	}
}

func TestFailedMetadataCommitKeepsPreviousCredentials(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("filesystem permission failure needs an unprivileged process")
	}
	s, dir := newTestStore(t)
	c, err := s.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.SecretFiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Secret versions can be prepared below secret/vpn/, but the metadata's
	// parent rejects CreateTemp. This forces failure after preparation, before commit.
	vpnDir := dir
	if err := os.Chmod(vpnDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(vpnDir, 0o700)
	edit := validInput()
	edit.Password = ptr("replacement-secret")
	if _, err := s.Update(c.ID, edit); err == nil {
		t.Fatal("update should fail")
	}
	if err := os.Chmod(vpnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := reloaded.SecretFiles(c.ID)
	if err != nil || after != files {
		t.Fatalf("lost credentials: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "secret", "vpn", c.ID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("uncommitted secrets left: %v %d", err, len(entries))
	}
	saved, err := reloaded.Get(c.ID)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("failed edit committed: %+v %v", saved, err)
	}
}

func TestCredentialRotationAndDeletion(t *testing.T) {
	s, _ := newTestStore(t)
	c, err := s.Create(validInput())
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.SecretFiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	edit := validInput()
	edit.Username = nil
	edit.Password = ptr("replacement-password")
	updated, err := s.Update(c.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("revision: %d", updated.Revision)
	}
	current, err := s.SecretFiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == old {
		t.Fatal("rotation mutated an active credential mount")
	}
	for path, want := range map[string]string{old.Password: "test-password-never-public", current.Password: "replacement-password", current.Username: "test-user-never-public"} {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Fatalf("rotation contents: %v", err)
		}
	}
	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old.Username, current.Password} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("secret version remains after deletion: %v", err)
		}
	}
}
func TestCountriesAreCanonicalAndCopied(t *testing.T) {
	choices := Countries()
	if len(choices) == 0 {
		t.Fatal("no countries")
	}
	choices[0] = "Atlantis"
	s, _ := newTestStore(t)
	in := validInput()
	in.Country = "  netHERlands "
	in.Provider = " NORDVPN "
	c, err := s.Create(in)
	if err != nil || c.Country != "Netherlands" || c.Provider != "nordvpn" {
		t.Fatalf("canonical country: %+v %v", c, err)
	}
	in.Name = "Other"
	in.Country = "Albania"
	if _, err := s.Create(in); err != nil {
		t.Fatalf("caller corrupted country validator: %v", err)
	}
}
