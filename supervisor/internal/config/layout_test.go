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

func TestToHost(t *testing.T) {
	l := NewLayout("/data").WithHost("/srv/deskyard")
	for in, want := range map[string]string{
		"/data":                 "/srv/deskyard",
		"/data/home/a":          "/srv/deskyard/home/a",
		"/data//photos":         "/srv/deskyard/photos",
		"/data/volumes.json":    "/srv/deskyard/volumes.json",
		"/data/home/../../etc":  "/data/home/../../etc",
		"/database":             "/database",
		"/srv/elsewhere/folder": "/srv/elsewhere/folder",
	} {
		if got := l.ToHost(in); got != want {
			t.Errorf("ToHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostDefaultsToData(t *testing.T) {
	l := NewLayout("/data")
	if l.Host() != "/data" || l.ToHost("/data/home/a") != "/data/home/a" {
		t.Errorf("Host = %q, ToHost = %q", l.Host(), l.ToHost("/data/home/a"))
	}
}
