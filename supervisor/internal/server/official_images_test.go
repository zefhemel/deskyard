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

const testXFCE = "ghcr.io/zefhemel/deskyard-debian-xfce:latest"

func TestRetiredDesktopsNoLongerOfficialButSavedReferencesRemain(t *testing.T) {
	for _, name := range []string{"kde", "mate"} {
		retired := "ghcr.io/zefhemel/deskyard-debian-" + name + ":latest"
		if _, ok := officialImageFor(retired); ok {
			t.Fatalf("%s must not be offered as an official image", name)
		}
		entries := availableEntries([]catalogEntry{{Ref: retired, Newest: "saved"}})
		if len(entries) != 2 || entries[0].Ref != retired || entries[0].Newest != "saved" {
			t.Fatalf("saved image lost or unexpected built-in choices: %+v", entries)
		}
	}
}

func discoveryRows(t *testing.T, bt *catalogTest) []map[string]any {
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
func discoveryRow(t *testing.T, bt *catalogTest, ref string) map[string]any {
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
	bt := newCatalogTest(t)
	path := filepath.Join(bt.s.Layout.Data, "images.json")
	before, _ := os.ReadFile(path)
	if rows := discoveryRows(t, bt); rows[0]["ref"] != "img" {
		t.Fatalf("default moved: %v", rows)
	}
	if r := discoveryRow(t, bt, testXFCE); r["installed"] != false || r["official"] != true || r["title"] == "" || r["description"] == "" {
		t.Errorf("available: %v", r)
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
	bt := newCatalogTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "ghcr.io/zefhemel/deskyard-debian-xfce"})
	n := 0
	for _, r := range discoveryRows(t, bt) {
		if sameRef(r["ref"].(string), testXFCE) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("XFCE rows: %d", n)
	}
}
func TestUpdateChecksDoNotInstallAvailableImages(t *testing.T) {
	bt := newCatalogTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "example.invalid/optional:latest"})
	if err := bt.s.checkImages(context.Background(), func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bt.e.pulls, []string{"img"}) {
		t.Fatalf("unexpected pulls: %v", bt.e.pulls)
	}
}

type discoveryEngine struct{ *catalogEngine }

func (e discoveryEngine) Pull(ctx context.Context, ref string, p func(engine.PullProgress)) error {
	if err := e.catalogEngine.Pull(ctx, ref, p); err != nil {
		return err
	}
	e.addImage(ref, "sha256:downloaded", "user")
	return nil
}
func TestInstallOfficialImageOnDemand(t *testing.T) {
	bt := newCatalogTest(t)
	bt.s.Eng = discoveryEngine{bt.e}
	bt.s.Computers.Eng = bt.s.Eng
	j := bt.wait(bt.start("POST", "/api/images/install", map[string]string{"ref": testXFCE}))
	if j.Status != "done" {
		t.Fatalf("install: %+v", j)
	}
	r := discoveryRow(t, bt, testXFCE)
	if r["installed"] != true || r["official"] != true {
		t.Fatalf("installed: %v", r)
	}
	if !slices.Equal(bt.e.pulls, []string{testXFCE}) || bt.s.images.newest(testXFCE) != "sha256:downloaded" {
		t.Fatal("install not recorded")
	}
}
func TestFailedOfficialInstallRemainsAvailable(t *testing.T) {
	bt := newCatalogTest(t)
	bt.e.pullErr = errors.New("offline")
	j := bt.wait(bt.start("POST", "/api/images/install", map[string]string{"ref": testXFCE}))
	if j.Status == "done" {
		t.Fatal("failed install succeeded")
	}
	if r := discoveryRow(t, bt, testXFCE); r["installed"] != false {
		t.Fatalf("failed installed: %v", r)
	}
}
func TestInstallRequiresAdminAndKnownImage(t *testing.T) {
	bt := newCatalogTest(t)
	_ = bt.s.images.put(catalogEntry{Ref: "deskyard-custom/tools:latest", Custom: true})
	anna := login(t, bt.ts, "anna")
	if code, _ := bt.do(anna, "POST", "/api/images/install", map[string]string{"ref": testXFCE}); code != http.StatusForbidden {
		t.Fatalf("nonadmin: %d", code)
	}
	for _, ref := range []string{"example.invalid/unknown"} {
		if code, _ := bt.do(bt.admin, "POST", "/api/images/install", map[string]string{"ref": ref}); code != http.StatusBadRequest {
			t.Fatalf("%s: %d", ref, code)
		}
	}
	if len(bt.e.pulls) != 0 {
		t.Fatalf("invalid pull: %v", bt.e.pulls)
	}
}

func TestOfficialImageUsedOnDemandRecordsLocalVersion(t *testing.T) {
	bt := newCatalogTest(t)
	bt.s.images.noteLocal(testXFCE, "sha256:used")
	e, ok := bt.s.images.entry(testXFCE)
	if !ok || e.Newest != "sha256:used" || e.Checked != nil {
		t.Fatalf("local use: %+v", e)
	}
	if bt.s.images.defaultRef() != "img" {
		t.Fatal("default changed")
	}
}
