package environments

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadVolumesMissingFileIsEmpty(t *testing.T) {
	vols, err := LoadVolumes(filepath.Join(t.TempDir(), "volumes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := vols.List(); len(got) != 0 {
		t.Fatalf("a missing file loads empty, got %v", got)
	}
}

func TestVolumesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volumes.json")
	vols, err := LoadVolumes(path)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	for _, name := range []string{"photos", "docs"} {
		if err := vols.Add(Volume{Name: name, Created: created, Kind: VolumeLocal}); err != nil {
			t.Fatal(err)
		}
	}
	again, err := LoadVolumes(path)
	if err != nil {
		t.Fatal(err)
	}
	got := again.List()
	if len(got) != 2 || got[0].Name != "docs" || got[1].Name != "photos" {
		t.Fatalf("List: %v", got)
	}
	for _, v := range got {
		if v.Kind != VolumeLocal || !v.Created.Equal(created) {
			t.Fatalf("reloaded %+v", v)
		}
	}
	if v, ok := again.Get("photos"); !ok || v.Name != "photos" {
		t.Fatalf("Get: %+v %v", v, ok)
	}
	if err := again.Delete("photos"); err != nil {
		t.Fatal(err)
	}
	if err := again.Delete("photos"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if third, err := LoadVolumes(path); err != nil || len(third.List()) != 1 {
		t.Fatalf("after delete: %v %v", third, err)
	}
}

func TestLoadVolumesDefaultsKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volumes.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	vols, err := LoadVolumes(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := vols.Get("photos"); !ok || v.Kind != VolumeLocal {
		t.Fatalf("got %+v %v", v, ok)
	}
}

func TestLoadVolumesHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volumes.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"host","hostPath":"/srv/photos"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	vols, err := LoadVolumes(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := vols.Get("photos"); !ok || v.Kind != VolumeHost || v.HostPath != "/srv/photos" {
		t.Fatalf("got %+v %v", v, ok)
	}
}

func TestLoadVolumesRejects(t *testing.T) {
	for _, tc := range []struct{ name, file string }{
		{"host without path", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"host"}]}`},
		{"relative host path", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"host","hostPath":"srv/photos"}]}`},
		{"unclean host path", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"host","hostPath":"/srv/../etc"}]}`},
		{"local with path", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"local","hostPath":"/srv/photos"}]}`},
		{"version", `{"version":2,"volumes":[]}`},
		{"kind", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","kind":"sftp"}]}`},
		{"unknown field", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z","size":3}]}`},
		{"duplicate", `{"version":1,"volumes":[{"name":"photos","created":"2026-10-26T10:00:00Z"},{"name":"photos","created":"2026-10-26T10:00:00Z"}]}`},
		{"invalid name", `{"version":1,"volumes":[{"name":"A","created":"2026-10-26T10:00:00Z"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "volumes.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadVolumes(path); err == nil {
				t.Fatal("loaded")
			}
		})
	}
}

func TestVolumesAddDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volumes.json")
	vols, err := LoadVolumes(path)
	if err != nil {
		t.Fatal(err)
	}
	v := Volume{Name: "photos", Created: time.Now().UTC(), Kind: VolumeLocal}
	if err := vols.Add(v); err != nil {
		t.Fatal(err)
	}
	if err := vols.Add(v); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
}

func TestValidHostPathRefusesDataEntries(t *testing.T) {
	if err := ValidHostPath("/data/home/x"); err == nil {
		t.Error("/data/home/x must be refused")
	}
	for _, p := range []string{"/data/photos", "/srv/photos"} {
		if err := ValidHostPath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}
