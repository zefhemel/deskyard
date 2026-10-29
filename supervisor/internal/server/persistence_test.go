package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func blockedStatePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(p, "state.json")
}
func TestCatalogFailedMutationKeepsPublishedState(t *testing.T) {
	for _, op := range []string{"put", "append", "remove", "newest", "local"} {
		t.Run(op, func(t *testing.T) {
			c, err := loadCatalog(t.TempDir(), "img")
			if err != nil {
				t.Fatal(err)
			}
			if err := c.put(catalogEntry{Ref: "second", Newest: "old"}); err != nil {
				t.Fatal(err)
			}
			before := append([]catalogEntry(nil), c.list...)
			originalPath := c.path
			disk, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			c.path = blockedStatePath(t)
			switch op {
			case "put":
				err = c.put(catalogEntry{Ref: "second", Newest: "new"})
			case "append":
				err = c.put(catalogEntry{Ref: "third"})
			case "remove":
				err = c.remove("second")
			case "newest":
				err = c.setNewest("second", "new")
			case "local":
				err = c.noteLocal("second", "new")
			}
			if err == nil {
				t.Fatal("expected write failure")
			}
			if !reflect.DeepEqual(c.list, before) {
				t.Fatal("failed mutation changed catalog")
			}
			got, err := os.ReadFile(originalPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(disk) {
				t.Fatal("failed mutation changed disk")
			}
		})
	}
}
func TestServerUsesVisibleLayout(t *testing.T) {
	s, _ := newTestServer(t)
	l := s.Layout
	for _, p := range []string{
		filepath.Join(l.Data, "accounts.json"),
		filepath.Join(l.Data, "images.json"),
		filepath.Join(l.Secret, "session.key"),
		filepath.Join(l.Secret, "vpn"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(l.Data, ".deskyard"), filepath.Join(l.Data, "vpn")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}
	if s.clients.dir != filepath.Join(l.Cache, "client") {
		t.Errorf("client cache in %s", s.clients.dir)
	}
	if got := s.Computers.Layout.HomeOf("alice"); got != filepath.Join(l.Data, "home", "alice") {
		t.Errorf("home in %s", got)
	}
}
