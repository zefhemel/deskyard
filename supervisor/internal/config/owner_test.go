package config

import (
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// chownLog records absolute paths of what reownOnce chowns.
type chownLog struct {
	data  string
	paths []string
}

func (c *chownLog) chown(name string, uid, gid int) error {
	if uid != UID || gid != GID {
		panic("chown to the wrong owner")
	}
	c.paths = append(c.paths, filepath.Join(c.data, name))
	return nil
}

// reown runs reownOnce on l with a recording chown.
func reown(t *testing.T, l Layout, c *chownLog) error {
	t.Helper()
	root, err := os.OpenRoot(l.Data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	c.data = l.Data
	return reownOnce(root, c.chown)
}

func (c *chownLog) sorted() []string {
	out := append([]string(nil), c.paths...)
	sort.Strings(out)
	return out
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func oldLayout(t *testing.T) Layout {
	t.Helper()
	l := NewLayout(t.TempDir())
	for _, f := range []string{"environments.json", "accounts.json", "notes.txt", "secret/key", "secret/certmagic/a/b", "state/instance", "cache/web/x", "home/alice/.bashrc", "home/alice/deep/file", "volume/data/file", "volume/data/sub/file"} {
		touch(t, filepath.Join(l.Data, f))
	}
	return l
}

func TestReownOnceOwnsSupervisorFilesAndTopFolders(t *testing.T) {
	l := oldLayout(t)
	var c chownLog
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	want := []string{
		l.Data, filepath.Join(l.Data, "accounts.json"), filepath.Join(l.Data, "environments.json"),
		l.Secret, filepath.Join(l.Secret, "key"), filepath.Join(l.Secret, "certmagic"),
		filepath.Join(l.Secret, "certmagic/a"), filepath.Join(l.Secret, "certmagic/a/b"),
		l.State, filepath.Join(l.State, "instance"), filepath.Join(l.State, "owner.tmp"),
		l.Cache, filepath.Join(l.Cache, "web"), filepath.Join(l.Cache, "web/x"),
		l.Home, filepath.Join(l.Home, "alice"),
		l.Volume, filepath.Join(l.Volume, "data"),
	}
	sort.Strings(want)
	got := c.sorted()
	if len(got) != len(want) {
		t.Fatalf("chowned\n%v\nwant\n%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chowned\n%v\nwant\n%v", got, want)
		}
	}
}

// A planted symlink in place of home/, volume/, secret/ or a child must not
// carry the re-own outside the data folder (say onto the Docker socket).
func TestReownOnceDoesNotFollowSymlinks(t *testing.T) {
	for _, dir := range []string{"home", "volume", "secret", "cache"} {
		t.Run(dir, func(t *testing.T) {
			l := oldLayout(t)
			outside := t.TempDir()
			touch(t, filepath.Join(outside, "child", "file"))
			target := filepath.Join(l.Data, dir)
			if err := os.RemoveAll(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			var c chownLog
			if err := reown(t, l, &c); err != nil {
				t.Fatal(err)
			}
			for _, p := range c.paths {
				if strings.HasPrefix(p, outside) || p == target || strings.HasPrefix(p, target+"/") {
					t.Fatalf("chowned %s through the %s symlink", p, dir)
				}
			}
		})
	}
}

func TestReownOnceDoesNotFollowSymlinkedChildren(t *testing.T) {
	l := oldLayout(t)
	outside := t.TempDir()
	touch(t, filepath.Join(outside, "file"))
	if err := os.Symlink(outside, filepath.Join(l.Secret, "link")); err != nil {
		t.Fatal(err)
	}
	var c chownLog
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	for _, p := range c.paths {
		if strings.HasPrefix(p, outside) {
			t.Fatalf("followed a symlink to %s", p)
		}
	}
}

func TestReownOnceRefusesSymlinkedState(t *testing.T) {
	l := NewLayout(t.TempDir())
	outside := t.TempDir()
	if err := os.Symlink(outside, l.State); err != nil {
		t.Fatal(err)
	}
	var c chownLog
	if err := reown(t, l, &c); err == nil {
		t.Fatal("expected an error")
	}
	if len(c.paths) != 0 {
		t.Fatalf("chowned %v", c.paths)
	}
}

func TestReownOnceWritesMarkerAndSkipsNextTime(t *testing.T) {
	l := oldLayout(t)
	var c chownLog
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(l.State, "owner"))
	if err != nil || string(b) != "1000\n" {
		t.Fatalf("marker = %q, %v", b, err)
	}
	if fi, _ := os.Stat(filepath.Join(l.State, "owner")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode %v", fi.Mode())
	}
	c.paths = nil
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.paths) != 0 {
		t.Fatalf("second run chowned %v", c.paths)
	}
}

// A supervisor killed after re-owning but before the marker was written
// re-owns again on the next start.
func TestReownOnceRerunsWithoutMarker(t *testing.T) {
	l := oldLayout(t)
	var c chownLog
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	first := len(c.paths)
	if err := os.Remove(filepath.Join(l.State, "owner")); err != nil {
		t.Fatal(err)
	}
	c.paths = nil
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.paths) != first {
		t.Fatalf("rerun chowned %d paths, want %d", len(c.paths), first)
	}
	if _, err := os.Stat(filepath.Join(l.State, "owner")); err != nil {
		t.Fatal("marker not rewritten")
	}
}

func TestReownOnceOnFreshFolder(t *testing.T) {
	l := NewLayout(t.TempDir())
	var c chownLog
	if err := reown(t, l, &c); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(l.State)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(l.State, "owner")); err != nil {
		t.Fatal(err)
	}
	got := c.sorted()
	if len(got) != 3 || got[0] != l.Data {
		t.Fatalf("chowned %v", got)
	}
}

func TestSocketGroupTCPHasNone(t *testing.T) {
	gid, ok, err := SocketGroup("tcp://h:2375")
	if err != nil || ok || gid != 0 {
		t.Fatalf("got %d %v %v", gid, ok, err)
	}
}

func TestSocketGroupStatsUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "sg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := int(fi.Sys().(*syscall.Stat_t).Gid)
	gid, ok, err := SocketGroup("unix://" + path)
	if err != nil || !ok || gid != want {
		t.Fatalf("got %d %v %v, want %d", gid, ok, err, want)
	}
}

func TestSocketGroupMissingSocketErrors(t *testing.T) {
	if _, _, err := SocketGroup("unix://" + filepath.Join(t.TempDir(), "none.sock")); err == nil {
		t.Fatal("expected an error for a missing socket")
	}
}
