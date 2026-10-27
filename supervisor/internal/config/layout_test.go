package config

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLegacyRefusesDotDeskyard(t *testing.T) {
	data := t.TempDir()
	if err := os.Mkdir(filepath.Join(data, ".deskyard"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := CheckLegacy(data)
	if !errors.Is(err, ErrLegacyLayout) {
		t.Fatalf("got %v, want ErrLegacyLayout", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(data, ".deskyard")) || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("message should name the folder and say to remove it: %v", err)
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 1 {
		t.Fatalf("CheckLegacy created files: %v", entries)
	}
}

func TestCheckLegacyFreshDir(t *testing.T) {
	if err := CheckLegacy(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := CheckLegacy(filepath.Join(t.TempDir(), "not-yet")); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareModes(t *testing.T) {
	l := NewLayout(t.TempDir())
	for _, dir := range []string{l.Secret, l.State} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Secret, l.State} {
		st, err := os.Stat(dir)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", dir, err, st)
		}
	}
	for _, dir := range []string{l.Cache, l.Home} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("%s: %v", dir, err)
		}
	}
	if got := l.HomeOf("alice"); got != filepath.Join(l.Data, "home", "alice") {
		t.Fatalf("HomeOf: %s", got)
	}
	if st, err := os.Stat(l.Volume); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Fatalf("%s: %v %v", l.Volume, err, st)
	}
	if got := l.VolumeOf("photos"); got != filepath.Join(l.Data, "volume", "photos") {
		t.Fatalf("VolumeOf: %s", got)
	}
}

func TestInstanceIDStable(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := l.Prepare(); err != nil {
		t.Fatal(err)
	}
	a, err := InstanceID(l)
	if err != nil {
		t.Fatal(err)
	}
	b, err := InstanceID(l)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 32 {
		t.Fatalf("instance IDs %q %q", a, b)
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("not hex: %q", a)
	}
	st, err := os.Stat(filepath.Join(l.State, "instance"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("instance file: %v %v", err, st)
	}
}

func TestToHostTranslatesLayoutEntries(t *testing.T) {
	l := NewLayout("/data").WithHost("/srv/deskyard")
	for in, want := range map[string]string{
		"/data/home/alice":                   "/srv/deskyard/home/alice",
		"/data/secret/remotes/x/rclone.conf": "/srv/deskyard/secret/remotes/x/rclone.conf",
		"/data/environments.json":            "/srv/deskyard/environments.json",
		"/data":                              "/srv/deskyard",
		"/data/photos":                       "/data/photos",
		"/srv/photos":                        "/srv/photos",
		"/data/homework":                     "/data/homework",
	} {
		if got := l.ToHost(in); got != want {
			t.Errorf("ToHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromHostIsTheInverse(t *testing.T) {
	l := NewLayout("/data").WithHost("/srv/deskyard")
	if got := l.FromHost("/srv/deskyard/volume/v"); got != "/data/volume/v" {
		t.Errorf("FromHost volume = %q", got)
	}
	if got := l.FromHost("/srv/other"); got != "/srv/other" {
		t.Errorf("FromHost other = %q", got)
	}
	plain := NewLayout("/data")
	if plain.ToHost("/data/home/a") != "/data/home/a" || plain.FromHost("/srv/x/home/a") != "/srv/x/home/a" {
		t.Error("without WithHost both translations must be the identity")
	}
}

func TestOwns(t *testing.T) {
	l := NewLayout("/data")
	for p, want := range map[string]bool{
		"/data": true, "/data/home/x": true, "/data/state": true, "/data/accounts.json": true,
		"/data/photos": false, "/data/homework": false, "/srv/home": false, "/data/sub/x.json": false,
	} {
		if got := l.Owns(p); got != want {
			t.Errorf("Owns(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestFromHostOnlyTranslatesLayoutEntries(t *testing.T) {
	l := NewLayout("/data").WithHost("/srv/deskyard")
	for in, want := range map[string]string{
		"/srv/deskyard/photos":  "/srv/deskyard/photos",
		"/srv/deskyard/home/a":  "/data/home/a",
		"/srv/deskyard":         "/data",
		"/srv/deskyard/../etc":  "/srv/deskyard/../etc",
		"/srv/deskyard//home/a": "/data/home/a",
	} {
		if got := l.FromHost(in); got != want {
			t.Errorf("FromHost(%q) = %q, want %q", in, got, want)
		}
	}
	short := NewLayout("/data").WithHost("/srv/d")
	if got := short.FromHost("/srv/data/home/x"); got != "/srv/data/home/x" {
		t.Errorf("sibling-prefix host path translated: %q", got)
	}
}

func TestPathsAreCleanedBeforeTranslation(t *testing.T) {
	l := NewLayout("/data").WithHost("/srv/deskyard")
	if l.Owns("/data/home/../../etc") || l.ToHost("/data/home/../../etc") != "/data/home/../../etc" {
		t.Error("a path escaping /data must not be owned or translated")
	}
	if got := l.ToHost("/data//photos"); got != "/data//photos" {
		t.Errorf("ToHost(/data//photos) = %q", got)
	}
	if got := l.ToHost("/data//home/a"); got != "/srv/deskyard/home/a" {
		t.Errorf("ToHost(/data//home/a) = %q", got)
	}
	for p, want := range map[string]bool{"/data/": true, "/datax": false, "/datax/home": false} {
		if got := l.Owns(p); got != want {
			t.Errorf("Owns(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWithHostEmptyIsIdentity(t *testing.T) {
	l := NewLayout("/data").WithHost("")
	if l.ToHost("/data/home/a") != "/data/home/a" || l.FromHost("./home/a") != "./home/a" {
		t.Error("an empty host must leave paths unchanged")
	}
}
