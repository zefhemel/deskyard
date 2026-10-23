package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const testKDE = "ghcr.io/zefhemel/deskyard-debian-kde:latest"

func discoveryRows(t *testing.T, bt *buildTest) []map[string]any {
	t.Helper()
	code, b := bt.do(bt.admin, "GET", "/api/images", nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, b)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
func discoveryRow(t *testing.T, bt *buildTest, ref string) map[string]any {
	t.Helper()
	for _, r := range discoveryRows(t, bt) {
		if r["ref"] == ref {
			return r
		}
	}
	t.Fatalf("missing %s", ref)
	return nil
}
func TestOfficialDiscoveryWithoutPullingOrChangingSavedCatalog(t *testing.T) {
	bt := newBuildTest(t)
	path := filepath.Join(bt.s.StateDir, "images.json")
	before, _ := os.ReadFile(path)
	if rows := discoveryRows(t, bt); rows[0]["ref"] != "img" {
		t.Fatalf("default moved: %v", rows)
	}
	for _, name := range []string{"xfce", "kde", "mate"} {
		r := discoveryRow(t, bt, "ghcr.io/zefhemel/deskyard-debian-"+name+":latest")
		if r["installed"] != false || r["official"] != true || r["title"] == "" || r["description"] == "" {
			t.Errorf("available: %v", r)
		}
	}
	if len(bt.e.pulls) != 0 {
		t.Fatalf("listing pulled: %v", bt.e.pulls)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("browsing changed saved catalog")
	}
}
func TestOfficialCatalogDeduplicatesNormalizedReferences(t *testing.T) {
	bt := newBuildTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "ghcr.io/zefhemel/deskyard-debian-kde"})
	n := 0
	for _, r := range discoveryRows(t, bt) {
		if sameRef(r["ref"].(string), testKDE) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("KDE rows: %d", n)
	}
}
func TestUpdateChecksDoNotInstallAvailableImages(t *testing.T) {
	bt := newBuildTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "example.invalid/optional:latest"})
	if err := bt.s.checkImages(context.Background(), func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bt.e.pulls, []string{"img"}) {
		t.Fatalf("unexpected pulls: %v", bt.e.pulls)
	}
}

type discoveryEngine struct{ *buildEngine }

func (e discoveryEngine) Pull(ctx context.Context, ref string, p func(engine.PullProgress)) error {
	if err := e.buildEngine.Pull(ctx, ref, p); err != nil {
		return err
	}
	e.addImage(ref, "sha256:downloaded", "user")
	return nil
}
func TestInstallOfficialImageOnDemand(t *testing.T) {
	bt := newBuildTest(t)
	bt.s.Eng = discoveryEngine{bt.e}
	bt.s.Desktops.Eng = bt.s.Eng
	j := bt.wait(bt.start("POST", "/api/images/install", map[string]string{"ref": testKDE}))
	if j.Status != "done" {
		t.Fatalf("install: %+v", j)
	}
	r := discoveryRow(t, bt, testKDE)
	if r["installed"] != true || r["official"] != true {
		t.Fatalf("installed: %v", r)
	}
	if !slices.Equal(bt.e.pulls, []string{testKDE}) || bt.s.images.newest(testKDE) != "sha256:downloaded" {
		t.Fatal("install not recorded")
	}
}
func TestFailedOfficialInstallRemainsAvailable(t *testing.T) {
	bt := newBuildTest(t)
	bt.e.pullErr = errors.New("offline")
	j := bt.wait(bt.start("POST", "/api/images/install", map[string]string{"ref": testKDE}))
	if j.Status == "done" {
		t.Fatal("failed install succeeded")
	}
	if r := discoveryRow(t, bt, testKDE); r["installed"] != false {
		t.Fatalf("failed installed: %v", r)
	}
}
func TestInstallRequiresAdminAndKnownNonCustomImage(t *testing.T) {
	bt := newBuildTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "deskyard-custom/tools:latest", Custom: true})
	anna := login(t, bt.ts, "anna")
	if code, _ := bt.do(anna, "POST", "/api/images/install", map[string]string{"ref": testKDE}); code != http.StatusForbidden {
		t.Fatalf("nonadmin: %d", code)
	}
	for _, ref := range []string{"example.invalid/unknown", "deskyard-custom/tools:latest"} {
		if code, _ := bt.do(bt.admin, "POST", "/api/images/install", map[string]string{"ref": ref}); code != http.StatusBadRequest {
			t.Fatalf("%s: %d", ref, code)
		}
	}
	if len(bt.e.pulls) != 0 {
		t.Fatalf("invalid pull: %v", bt.e.pulls)
	}
}

func TestOfficialImageUsedOnDemandRecordsLocalVersion(t *testing.T) {
	bt := newBuildTest(t)
	bt.s.images.noteLocal(testKDE, "sha256:used")
	e, ok := bt.s.images.entry(testKDE)
	if !ok || e.Newest != "sha256:used" || e.Checked != nil {
		t.Fatalf("local use: %+v", e)
	}
	if bt.s.images.defaultRef() != "img" {
		t.Fatal("default changed")
	}
}
