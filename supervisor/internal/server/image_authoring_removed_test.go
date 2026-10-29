package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestImageAuthoringRoutesRemoved(t *testing.T) {
	bt := newCatalogTest(t)
	for _, req := range []struct{ method, path string }{{"GET", "/api/recipes"}, {"GET", "/api/recipes/template?base=img"}, {"POST", "/api/recipes"}, {"PUT", "/api/recipes/test"}, {"DELETE", "/api/recipes/test"}, {"POST", "/api/recipes/test/rebuild"}, {"GET", "/api/environments/alice/image-scan"}, {"POST", "/api/environments/alice/image"}} {
		code, body := bt.do(bt.admin, req.method, req.path, map[string]string{})
		if code != 404 {
			t.Errorf("%s %s = %d %s; want 404", req.method, req.path, code, body)
		}
	}
}

func TestAddPrebuiltLocalImageWithoutPull(t *testing.T) {
	bt := newCatalogTest(t)
	ref := "deskyard-custom/existing:latest"
	bt.e.addImage(ref, "sha256:prebuilt", "user")
	state := bt.wait(bt.start("POST", "/api/images", map[string]string{"ref": ref}))
	if state.Status != "done" {
		t.Fatalf("add local: %+v", state)
	}
	if len(bt.e.pulls) != 0 {
		t.Fatalf("local add pulled: %v", bt.e.pulls)
	}
	if bt.s.images.newest(ref) != "sha256:prebuilt" {
		t.Fatal("local version not recorded")
	}
}

func TestRemoveLegacyCustomCatalogEntryPreservesImageAndSource(t *testing.T) {
	bt := newCatalogTest(t)
	ref := "deskyard-custom/existing:latest"
	bt.e.addImage(ref, "sha256:prebuilt", "user")
	if err := bt.s.images.put(catalogEntry{Ref: ref, Custom: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bt.s.Layout.Data, "recipes.json")
	original := []byte("legacy source stays untouched")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	code, body := bt.do(bt.admin, "DELETE", "/api/images?ref="+ref, nil)
	if code != 204 {
		t.Fatalf("remove: %d %s", code, body)
	}
	if _, err := bt.e.ImageInspect(t.Context(), ref); err != nil {
		t.Fatalf("prebuilt image removed: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("source changed: %q %v", got, err)
	}
}

// A definition references its image whether or not it has a container.
func TestRemoveImageRefusedForContainerlessComputer(t *testing.T) {
	bt := newCatalogTest(t)
	ref := "deskyard-custom/existing:latest"
	bt.e.addImage(ref, "sha256:prebuilt", "user")
	if err := bt.s.images.put(catalogEntry{Ref: ref, Newest: "sha256:prebuilt", Custom: true}); err != nil {
		t.Fatal(err)
	}
	for _, image := range []computers.ImageRef{{Ref: ref, ID: "sha256:prebuilt"}, {Ref: "elsewhere/other:1", ID: "sha256:prebuilt"}} {
		def := computers.Definition{Name: "alice", Created: time.Now().UTC(), Revision: 1, Image: image,
			Network: computers.Network{Kind: computers.NetworkIsolated}, Power: computers.PowerStopped}
		if err := bt.s.Computers.Defs.Put(def); err != nil {
			t.Fatal(err)
		}
		if code, body := bt.do(bt.admin, "DELETE", "/api/images?ref="+ref, nil); code != 409 || !strings.Contains(string(body), "alice") {
			t.Fatalf("removed an image a stopped computer without a container uses (%+v): %d %s", image, code, body)
		}
	}
	if err := bt.s.Computers.Defs.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if code, body := bt.do(bt.admin, "DELETE", "/api/images?ref="+ref, nil); code != 204 {
		t.Fatalf("remove unused: %d %s", code, body)
	}
}

func TestAddLocalImageRequiresDesktopContract(t *testing.T) {
	bt := newCatalogTest(t)
	ref := "local/invalid:latest"
	bt.e.addImage(ref, "sha256:invalid", "user")
	bt.e.images["sha256:invalid"] = engine.Image{ID: "sha256:invalid"}
	st := bt.wait(bt.start("POST", "/api/images", map[string]string{"ref": ref}))
	if st.Status != "failed" {
		t.Fatalf("invalid image accepted: %+v", st)
	}
	if len(bt.e.pulls) != 0 {
		t.Fatalf("invalid local image triggered pull: %v", bt.e.pulls)
	}
	if _, ok := bt.s.images.entry(ref); ok {
		t.Fatal("invalid image added to catalog")
	}
}

func TestInstallLegacyCustomRetainsLocalUpdatePolicy(t *testing.T) {
	bt := newCatalogTest(t)
	ref := "deskyard-custom/existing:latest"
	bt.e.addImage(ref, "sha256:prebuilt", "user")
	if err := bt.s.images.put(catalogEntry{Ref: ref, Custom: true}); err != nil {
		t.Fatal(err)
	}
	st := bt.wait(bt.start("POST", "/api/images/install", map[string]string{"ref": ref}))
	if st.Status != "done" {
		t.Fatalf("install: %+v", st)
	}
	if err := bt.s.checkImages(t.Context(), func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	for _, pulled := range bt.e.pulls {
		if pulled == ref {
			t.Fatal("legacy local image pulled after install")
		}
	}
}
