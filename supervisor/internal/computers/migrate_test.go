package computers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
)

const legacyDefinitions = `{
  "version": 1,
  "environments": [
    {
      "name": "alice",
      "created": "2026-10-26T10:00:00Z",
      "revision": 4,
      "image": {"ref": "ghcr.io/x/deskyard-xfce:latest", "id": "sha256:1"},
      "limits": {"memoryBytes": 0, "nanoCPUs": 0},
      "network": {"kind": "isolated"},
      "volumes": [],
      "dockerAccess": false,
      "memoryReclaim": true,
      "power": "running"
    }
  ]
}
`

func migrateLayout(t *testing.T) config.Layout {
	t.Helper()
	return config.NewLayout(t.TempDir())
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still there: %v", filepath.Base(path), err)
	}
}

func TestMigrateRenamesEnvironmentsJSON(t *testing.T) {
	l := migrateLayout(t)
	old := filepath.Join(l.Data, "environments.json")
	writeFile(t, old, legacyDefinitions)
	if err := MigrateDataFiles(l); err != nil {
		t.Fatal(err)
	}
	assertGone(t, old)
	path := filepath.Join(l.Data, "computers.json")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["environments"]; ok {
		t.Fatalf("old key kept: %s", readFile(t, path))
	}
	if _, ok := raw["computers"]; !ok {
		t.Fatalf("no computers key: %s", readFile(t, path))
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := defs.Get("alice")
	if !ok || d.Revision != 4 || !d.MemoryReclaim || d.Power != PowerRunning || d.Image.ID != "sha256:1" {
		t.Fatalf("alice = %+v, %v", d, ok)
	}
}

// A supervisor stopped after writing computers.json but before removing
// environments.json: the new file wins and the old one goes.
func TestMigrateBothFilesKeepsNew(t *testing.T) {
	l := migrateLayout(t)
	old := filepath.Join(l.Data, "environments.json")
	path := filepath.Join(l.Data, "computers.json")
	writeFile(t, old, legacyDefinitions)
	current := `{"version":1,"computers":[]}` + "\n"
	writeFile(t, path, current)
	if err := MigrateDataFiles(l); err != nil {
		t.Fatal(err)
	}
	assertGone(t, old)
	if got := readFile(t, path); got != current {
		t.Fatalf("computers.json changed: %s", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	empty := migrateLayout(t)
	if err := MigrateDataFiles(empty); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(empty.Data); err != nil || len(entries) != 0 {
		t.Fatalf("empty data folder now has %v, %v", entries, err)
	}
	missing := config.NewLayout(filepath.Join(t.TempDir(), "absent"))
	if err := MigrateDataFiles(missing); err != nil {
		t.Fatalf("missing data folder: %v", err)
	}

	l := migrateLayout(t)
	writeFile(t, filepath.Join(l.Data, "environments.json"), legacyDefinitions)
	if err := MigrateDataFiles(l); err != nil {
		t.Fatal(err)
	}
	once := readFile(t, filepath.Join(l.Data, "computers.json"))
	if err := MigrateDataFiles(l); err != nil {
		t.Fatal(err)
	}
	if twice := readFile(t, filepath.Join(l.Data, "computers.json")); twice != once {
		t.Fatalf("second run changed computers.json:\n%s\n%s", once, twice)
	}
	if entries, _ := os.ReadDir(l.Data); len(entries) != 1 {
		t.Fatalf("data folder has %v", entries)
	}
}

// An unreadable old file stops the start rather than losing definitions.
func TestMigrateRefusesInvalidOldFile(t *testing.T) {
	l := migrateLayout(t)
	old := filepath.Join(l.Data, "environments.json")
	writeFile(t, old, `{"version":1,"environments":[`)
	if err := MigrateDataFiles(l); err == nil {
		t.Fatal("migrated a broken file")
	}
	if got := readFile(t, old); got != `{"version":1,"environments":[` {
		t.Fatalf("old file changed: %s", got)
	}
	assertGone(t, filepath.Join(l.Data, "computers.json"))
}

func TestDefinitionsPathIsComputersJSON(t *testing.T) {
	l := migrateLayout(t)
	if got := definitionsPath(l); got != filepath.Join(l.Data, "computers.json") {
		t.Fatalf("definitionsPath = %s", got)
	}
}
