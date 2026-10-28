//go:build engine

package environments

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// The root helper makes folders on the engine host under free names, owned
// by the desktop user, and removes trees the supervisor can't, reporting
// what fails; it leaves no container behind.
func TestFolderHelpersOnEngine(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	parent := Folder{Path: filepath.Join(filepath.Dir(s.Layout.Home), "host"), OnEngineHost: true}
	if err := os.MkdirAll(parent.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 2 {
		name, err := s.MakeFreeFolder(ctx, parent, "Restored 2026-10-28 1200")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
		st, err := os.Stat(filepath.Join(parent.Path, name))
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
			t.Fatalf("%s: %v %v", name, st, err)
		}
	}
	if strings.Join(got, ",") != "Restored 2026-10-28 1200,Restored 2026-10-28 1200 (2)" {
		t.Fatalf("names %q", got)
	}

	// A tree with a read-only folder in it, as Flatpak leaves, which the
	// supervisor (UID 1000) can't empty itself.
	home := Folder{Path: s.Layout.Home}
	stuck := filepath.Join(home.Path, "cct-gone", "app", "ro")
	if err := os.MkdirAll(stuck, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stuck, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTree(ctx, home, "cct-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home.Path, "cct-gone")); !os.IsNotExist(err) {
		t.Fatalf("not removed: %v", err)
	}
	left, err := s.Eng.List(ctx, map[string]string{helper.LabelInstance: s.Instance})
	if err != nil || len(left) != 0 {
		t.Fatalf("helpers left: %+v %v", left, err)
	}
	// The helpers' own folders are cleaned up as root too.
	t.Cleanup(func() { _ = s.RemoveTree(context.Background(), Folder{Path: filepath.Dir(parent.Path)}, "host") })
}
