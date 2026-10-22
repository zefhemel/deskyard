package server

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/recipes"
)

// withDesktop adds a stopped desktop "alice" on img, with these files in
// its folder.
func withDesktop(t *testing.T, bt *buildTest, files map[string]string) string {
	t.Helper()
	bt.e.desktops = []engine.Container{{ID: "c1", Name: "deskyard-alice", Image: "img", ImageID: "sha256:base1", State: "exited",
		Labels: map[string]string{desktops.LabelManaged: "true", desktops.LabelName: "alice", desktops.LabelID: "id-alice"}}}
	folder := filepath.Join(bt.s.Desktops.Cfg.DataDir, "alice")
	for p, data := range files {
		full := filepath.Join(folder, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

var aliceHome = map[string]string{
	".config/app/settings.json":         `{"theme":"dark"}`,
	".config/gh/hosts.yml":              "token: x",
	".ssh/id_test":                      "private",
	".codex/auth.json":                  "token",
	".bashrc":                           "# mine",
	".cache/big":                        "cache",
	".var/app/org.example.App/config/x": "app data",
	".local/share/flatpak/repo/config": "[core]\nmode=bare-user-only\n\n[remote \"flathub\"]\nurl=https://dl.flathub.org/repo/\n\n" +
		"[remote \"extra\"]\nurl=https://example.org/repo/\n\n[remote \"nokey\"]\nurl=https://nokey.example.org/repo/\n",
	".local/share/flatpak/repo/extra.trustedkeys.gpg":                                  "KEY",
	".local/share/flatpak/repo/refs/remotes/extra/app/org.example.App/x86_64/stable":   "c",
	".local/share/flatpak/repo/refs/remotes/flathub/app/org.gimp.GIMP/x86_64/stable":   "c",
	".local/share/flatpak/repo/refs/remotes/nokey/app/org.example.Other/x86_64/stable": "c",
	".local/share/flatpak/app/org.example.App/current":                                 "x",
	".local/share/flatpak/app/org.gimp.GIMP/current":                                   "x",
	".local/share/flatpak/app/org.example.Other/current":                               "x",
}

func TestImageScan(t *testing.T) {
	bt := newBuildTest(t)
	withDesktop(t, bt, aliceHome)
	if code, _ := bt.do(login(t, bt.ts, "anna"), "GET", "/api/desktops/alice/image-scan", nil); code != 404 && code != 403 {
		t.Errorf("non-admin: %d", code)
	}
	code, b := bt.do(bt.admin, "GET", "/api/desktops/alice/image-scan", nil)
	if code != 200 {
		t.Fatalf("scan: %d %s", code, b)
	}
	var scan imageScanJSON
	_ = json.Unmarshal(b, &scan)
	g := map[string]recipes.Group{}
	for _, x := range scan.Groups {
		g[x.Name] = x
	}
	if s := g[recipes.GroupSettings]; strings.Join(s.Secrets, ",") != ".config/gh/hosts.yml" || s.Bytes != int64(len(`{"theme":"dark"}`)) {
		t.Errorf("settings: %+v", s)
	}
	if d := g[recipes.GroupDotfiles]; strings.Join(d.Secrets, ",") != ".codex/auth.json,.ssh" || strings.Join(d.Paths, ",") != ".bashrc" {
		t.Errorf("dotfiles: %+v", d)
	}
	if len(scan.Flatpaks) != 3 || len(scan.Remotes) != 3 || len(scan.Notes) != 2 ||
		!strings.Contains(scan.Notes[0], "nokey") || !strings.Contains(scan.Notes[1], "org.example.Other") {
		t.Errorf("flatpak: %+v %+v %q", scan.Flatpaks, scan.Remotes, scan.Notes)
	}
	if want := int64(len(`{"theme":"dark"}`) + len("token: x") + len("private") + len("token") + len("# mine") + len("app data")); scan.TotalBytes != want {
		t.Errorf("total %d, want %d", scan.TotalBytes, want)
	}
}

func TestImageFromDesktop(t *testing.T) {
	bt := newBuildTest(t)
	withDesktop(t, bt, aliceHome)
	body := map[string]any{"name": "Alice's", "slug": "alice-img", "groups": []string{"settings", "flatpak"}, "include": []string{".codex/auth.json"}}
	if code, _ := bt.do(login(t, bt.ts, "anna"), "POST", "/api/desktops/alice/image", body); code != 404 && code != 403 {
		t.Errorf("non-admin: %d", code)
	}
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", body)); st.Status != "done" {
		t.Fatalf("build: %+v", st)
	}
	ctx := bt.e.context
	for name, want := range map[string]string{
		"home-template/": "<dir>",
		"home-template/.config/app/settings.json":         `{"theme":"dark"}`,
		"home-template/.codex/auth.json":                  "token",
		"home-template/.var/app/org.example.App/config/x": "app data",
	} {
		if ctx[name] != want {
			t.Errorf("%s: %q, want %q", name, ctx[name], want)
		}
	}
	if want := "[Flatpak Repo]\nTitle=extra\nUrl=https://example.org/repo/\nGPGKey=S0VZ\n"; ctx["remotes/extra.flatpakrepo"] != want {
		t.Errorf("remote file: %q", ctx["remotes/extra.flatpakrepo"])
	}
	for name := range ctx {
		if strings.HasPrefix(name, "remotes/") {
			continue
		}
		for _, out := range []string{"hosts.yml", ".ssh", ".cache", ".bashrc", "flatpak", "config.toml"} {
			if strings.Contains(name, out) {
				t.Errorf("%s is in the build context", name)
			}
		}
	}
	df := bt.e.dockerfile
	for _, want := range []string{"COPY --chown=1000:1000 home-template/ /opt/deskyard/home-template/", "install -y --noninteractive 'extra' 'org.example.App'",
		"install -y --noninteractive 'flathub' 'org.gimp.GIMP'", "remote-add --if-not-exists 'extra'"} {
		if !strings.Contains(df, want) {
			t.Errorf("dockerfile lacks %q:\n%s", want, df)
		}
	}
	if strings.Contains(df, "org.example.Other") || strings.Contains(df, "nokey") {
		t.Errorf("dockerfile has the remote without a key:\n%s", df)
	}
	img, _ := bt.e.ImageInspect(t.Context(), bt.e.tags["deskyard-custom/alice-img:latest"])
	var rec recipes.Recipe
	_ = json.Unmarshal([]byte(img.Labels[labelRecipe]), &rec)
	if img.Labels[labelSecrets] != "true" || !rec.Secrets || rec.HomeFrom != recipes.HomeFromImage || rec.Base != "img" {
		t.Errorf("labels %v, recipe %+v", img.Labels, rec)
	}
	if !bt.catalog()["deskyard-custom/alice-img:latest"].Secrets {
		t.Error("catalog: not marked as containing secrets")
	}

	// The same slug again is refused; nothing ticked still makes a template folder.
	if code, _ := bt.do(bt.admin, "POST", "/api/desktops/alice/image", body); code != 409 {
		t.Errorf("same slug: %d", code)
	}
	plain := map[string]any{"name": "Plain", "slug": "plain", "groups": []string{}, "include": []string{}}
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", plain)); st.Status != "done" {
		t.Fatalf("plain build: %+v", st)
	}
	if len(bt.e.context) != 2 || bt.e.context["home-template/"] != "<dir>" || !strings.Contains(bt.e.dockerfile, "COPY --chown=1000:1000 home-template/") {
		t.Errorf("plain context %v\n%s", bt.e.context, bt.e.dockerfile)
	}
	img, _ = bt.e.ImageInspect(t.Context(), bt.e.tags["deskyard-custom/plain:latest"])
	if img.Labels[labelSecrets] != "" {
		t.Errorf("plain image marked secret: %v", img.Labels)
	}

	for _, bad := range []map[string]any{
		{"name": "x", "slug": "bad-one", "groups": []string{"everything"}},
		{"name": "x", "slug": "bad-two", "include": []string{".bashrc"}},
		{"name": "x", "slug": "bad-three", "include": []string{"../other/.ssh"}},
		{"name": "x", "slug": "B"},
	} {
		if code, b := bt.do(bt.admin, "POST", "/api/desktops/alice/image", bad); code != 400 {
			t.Errorf("%v: %d %s", bad, code, b)
		}
	}
}

// A failed first build leaves no recipe behind, so the admin can retry from
// the desktop under the same name; neither is the recipe on top saved.
func TestImageFromDesktopFailedBuildForgetsRecipe(t *testing.T) {
	bt := newBuildTest(t)
	withDesktop(t, bt, aliceHome)
	bt.s.smokeTest = func(context.Context, string) error { return errors.New("didn't come up") }
	body := map[string]any{"name": "Alice's", "slug": "alice-img", "groups": []string{"settings"},
		"recipe": map[string]string{"slug": "alice-tools", "name": "Alice's tools"}}
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", body)); st.Status != "failed" || !strings.Contains(st.Error, "didn't come up") {
		t.Fatalf("want a failed build: %+v", st)
	}
	for _, slug := range []string{"alice-img", "alice-tools"} {
		if _, err := bt.s.recipes.Get(slug); !errors.Is(err, recipes.ErrNotFound) {
			t.Errorf("recipe %s kept after a failed build: %v", slug, err)
		}
	}
	bt.s.smokeTest = func(context.Context, string) error { return nil }
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", body)); st.Status != "done" {
		t.Fatalf("retry: %+v", st)
	}
	if _, err := bt.s.recipes.Get("alice-img"); err != nil {
		t.Errorf("recipe not kept after the build succeeded: %v", err)
	}
}

// A folder the supervisor can't read itself is read by a helper container.
func TestImageFromDesktopThroughHelper(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	bt := newBuildTest(t)
	bt.e.addImage("alpine:3.22", "sha256:alpine", "")
	folder := withDesktop(t, bt, map[string]string{".config/app/settings.json": "here"})
	locked := filepath.Join(folder, ".config/app/locked.json")
	if err := os.WriteFile(locked, []byte("locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct{ name, data string }{{"./", ""}, {"./.config/", ""}, {"./.config/app/", ""},
		{"./.config/app/settings.json", "from helper"}, {"./.config/app/locked.json", "locked"}, {"./.ssh/id_test", "private"}} {
		h := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(f.name, "/") {
			h.Typeflag, h.Mode, h.Size = tar.TypeDir, 0o700, 0
		}
		_ = tw.WriteHeader(h)
		_, _ = tw.Write([]byte(f.data))
	}
	_ = tw.Close()
	bt.e.folderTar = buf.Bytes()

	body := map[string]any{"name": "Helper", "slug": "helper", "groups": []string{"settings"}}
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", body)); st.Status != "done" {
		t.Fatalf("build: %+v", st)
	}
	ctx := bt.e.context
	if ctx["home-template/.config/app/settings.json"] != "from helper" || ctx["home-template/.config/app/locked.json"] != "locked" ||
		ctx["home-template/.config/app/"] != "<dir>" || len(ctx) != 6 {
		t.Errorf("context: %v", ctx)
	}
	var helper *engine.ContainerSpec
	for i, s := range bt.e.specs {
		if strings.HasPrefix(s.Name, "deskyard-read-") {
			helper = &bt.e.specs[i]
		}
	}
	if helper == nil || helper.Image != "alpine:3.22" || len(helper.Mounts) != 1 || !helper.Mounts[0].ReadOnly || helper.Mounts[0].Source != folder {
		t.Errorf("helper: %+v", helper)
	}
	if _, left := bt.e.containers[helper.Name]; left {
		t.Error("the helper container was left behind")
	}
}

// The supervisor's own state, next to the desktops' folders (as in a real
// install, where StateDir is DataDir/.deskyard).
func supervisorSecret(t *testing.T, bt *buildTest) {
	t.Helper()
	dir := filepath.Join(bt.s.Desktops.Cfg.DataDir, ".deskyard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokens.json"), []byte("SUPERVISOR-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoLeak(t *testing.T, ctx map[string]string) {
	t.Helper()
	for name, data := range ctx {
		if strings.Contains(data, "SUPERVISOR-SECRET") || strings.Contains(name, "tokens.json") {
			t.Errorf("%s leaks the supervisor's state: %q", name, data)
		}
	}
}

func buildAlice(t *testing.T, bt *buildTest, slug string) {
	t.Helper()
	body := map[string]any{"name": "Alice", "slug": slug, "groups": []string{"settings", "dotfiles"}}
	done := make(chan jobState, 1)
	go func() { done <- bt.wait(bt.start("POST", "/api/desktops/alice/image", body)) }()
	select {
	case st := <-done:
		if st.Status != "done" {
			t.Fatalf("build: %+v", st)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the build hangs")
	}
}

// Symlinks are copied as links and never followed; FIFOs and devices are
// left out; setuid, setgid and sticky bits are dropped.
func TestImageFromDesktopSpecialFiles(t *testing.T) {
	bt := newBuildTest(t)
	supervisorSecret(t, bt)
	folder := withDesktop(t, bt, map[string]string{".config/app/settings.json": "here", ".config/app/tool": "#!/bin/sh"})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink("../../../.deskyard/tokens.json", filepath.Join(folder, ".config/app/link.json")))
	must(os.Symlink("../.deskyard", filepath.Join(folder, ".state")))
	must(syscall.Mkfifo(filepath.Join(folder, ".config/app/pipe"), 0o644))
	must(os.Chmod(filepath.Join(folder, ".config/app/tool"), 0o755|os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
	device := os.Geteuid() == 0 && syscall.Mknod(filepath.Join(folder, ".config/app/null"), syscall.S_IFCHR|0o666, 0x103) == nil

	buildAlice(t, bt, "special")
	ctx := bt.e.context
	assertNoLeak(t, ctx)
	if ctx["home-template/.config/app/link.json"] != "-> ../../../.deskyard/tokens.json" || ctx["home-template/.state"] != "-> ../.deskyard" {
		t.Errorf("links: %v", ctx)
	}
	for _, out := range []string{"home-template/.config/app/pipe", "home-template/.config/app/null"} {
		if _, ok := ctx[out]; ok {
			t.Errorf("%s copied", out)
		}
	}
	if m := bt.e.modes["home-template/.config/app/tool"]; m != 0o755 {
		t.Errorf("tool mode %o, want 755", m)
	}
	if !device {
		t.Log("device file not tested (needs root)")
	}
}

// A running desktop swaps things while the walk runs: a listed folder for a
// symlink out of the folder, a listed file for a symlink, a FIFO.
func TestImageFromDesktopSwapsDuringWalk(t *testing.T) {
	bt := newBuildTest(t)
	supervisorSecret(t, bt)
	folder := withDesktop(t, bt, map[string]string{
		".config/app/settings.json": "here",
		".config/swapped/x":         "folder content",
		".config/app/file.json":     "file content",
		".config/app/fifo.json":     "fifo content",
	})
	walkHereHook = func(_, rel string) {
		p := filepath.Join(folder, filepath.FromSlash(rel))
		switch rel {
		case ".config/swapped":
			_ = os.RemoveAll(p)
			_ = os.Symlink("../../.deskyard", p)
		case ".config/app/file.json":
			_ = os.Remove(p)
			_ = os.Symlink("../../../.deskyard/tokens.json", p)
		case ".config/app/fifo.json":
			_ = os.Remove(p)
			_ = syscall.Mkfifo(p, 0o644)
		}
	}
	t.Cleanup(func() { walkHereHook = nil })

	buildAlice(t, bt, "swaps")
	ctx := bt.e.context
	assertNoLeak(t, ctx)
	if ctx["home-template/.config/app/settings.json"] != "here" {
		t.Errorf("settings.json missing: %v", ctx)
	}
	for name := range ctx {
		if strings.Contains(name, "file.json") || strings.Contains(name, "fifo.json") || strings.HasPrefix(name, "home-template/.config/swapped/") {
			t.Errorf("%s copied after it was swapped", name)
		}
	}
}

func TestStartupSweepsBuildContexts(t *testing.T) {
	e := newBuildEngine()
	s, _ := newTestServerWith(t, e)
	left := filepath.Join(s.StateDir, buildsDir, "context-1.tar")
	if err := os.MkdirAll(filepath.Dir(left), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte("a home"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Log: s.Log, Eng: e, Accounts: s.Accounts, StateDir: s.StateDir, Desktops: s.Desktops, DefaultImage: "img", UI: s.UI}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("left-over build context still there: %v", err)
	}
}

// "Also save a recipe": once the image is built, a recipe on it is saved, to
// add tools on top later; it builds like any other.
func TestImageFromDesktopSavesRecipeOnTop(t *testing.T) {
	bt := newBuildTest(t)
	withDesktop(t, bt, aliceHome)
	body := map[string]any{"name": "Alice's", "slug": "alice-img", "groups": []string{"settings"}, "include": []string{".codex/auth.json"},
		"recipe": map[string]string{"slug": "alice-tools", "name": "Alice's tools"}}
	if st := bt.wait(bt.start("POST", "/api/desktops/alice/image", body)); st.Status != "done" {
		t.Fatalf("build: %+v", st)
	}
	follow, err := bt.s.recipes.Get("alice-tools")
	if err != nil || follow.Base != "deskyard-custom/alice-img:latest" || follow.Name != "Alice's tools" || !follow.Secrets || follow.HomeFrom != "" {
		t.Fatalf("recipe on top: %+v %v", follow, err)
	}
	edit := tools("alice-tools", "deskyard-custom/alice-img:latest")
	if st := bt.wait(bt.start("PUT", "/api/recipes/alice-tools", edit)); st.Status != "done" {
		t.Fatalf("building the recipe on top: %+v", st)
	}
	if !bt.catalog()["deskyard-custom/alice-tools:latest"].Secrets {
		t.Error("the image on top isn't marked as containing secrets")
	}

	for _, bad := range []map[string]any{
		{"name": "x", "slug": "again", "recipe": map[string]string{"slug": "alice-tools", "name": "x"}}, // exists
		{"name": "x", "slug": "same", "recipe": map[string]string{"slug": "same", "name": "x"}},         // same as the image
	} {
		if code, b := bt.do(bt.admin, "POST", "/api/desktops/alice/image", bad); code != 409 {
			t.Errorf("%v: %d %s", bad, code, b)
		}
	}
	if code, _ := bt.do(bt.admin, "POST", "/api/desktops/alice/image", map[string]any{"name": "x", "slug": "other", "recipe": map[string]string{"slug": "B", "name": "x"}}); code != 400 {
		t.Errorf("bad recipe slug: %d", code)
	}
}

// A stopped desktop's Flatpak installation is read from its folder: a key or
// config linked out of the folder (to the supervisor's state) is never read.
func TestStoppedFlatpakReadsStayInFolder(t *testing.T) {
	bt := newBuildTest(t)
	supervisorSecret(t, bt)
	folder := withDesktop(t, bt, map[string]string{
		".local/share/flatpak/repo/config":                                           "[remote \"evil\"]\nurl=https://evil.example.org/repo/\n[remote \"fifo\"]\nurl=https://fifo.example.org/repo/\n",
		".local/share/flatpak/repo/refs/remotes/evil/app/org.evil.App/x86_64/stable": "c",
		".local/share/flatpak/app/org.evil.App/current":                              "x",
	})
	repo := filepath.Join(folder, ".local/share/flatpak/repo")
	if err := os.Symlink("../../../../../.deskyard/tokens.json", filepath.Join(repo, "evil.trustedkeys.gpg")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(repo, "fifo.trustedkeys.gpg"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, b := bt.do(bt.admin, "GET", "/api/desktops/alice/image-scan", nil)
	if code != 200 || strings.Contains(string(b), "SUPERVISOR-SECRET") || strings.Contains(string(b), "U1VQRVJWSVNPUi1TRUNSRVQ") {
		t.Fatalf("scan: %d %s", code, b)
	}
	if !strings.Contains(string(b), "Leaving out the Flatpak remote evil: it has no GPG key") {
		t.Errorf("no note about the remote: %s", b)
	}
	body := map[string]any{"name": "Evil", "slug": "evil", "groups": []string{"flatpak"}}
	done := make(chan jobState, 1)
	go func() { done <- bt.wait(bt.start("POST", "/api/desktops/alice/image", body)) }()
	select {
	case st := <-done:
		if st.Status != "done" {
			t.Fatalf("build: %+v", st)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the build hangs")
	}
	rec, _ := bt.s.recipes.Get("evil")
	recJSON, _ := json.Marshal(rec)
	for _, where := range []string{string(recJSON), bt.e.dockerfile, fmt.Sprint(bt.e.context)} {
		if strings.Contains(where, "SUPERVISOR-SECRET") || strings.Contains(where, "U1VQRVJWSVNPUi1TRUNSRVQ") {
			t.Errorf("the supervisor's state leaked: %s", where)
		}
	}
	if len(rec.Remotes) != 0 || len(rec.Flatpaks) != 0 {
		t.Errorf("recipe: %+v", rec)
	}
}

// The folder size shown on the details page doesn't follow links out either.
func TestFolderSizeStaysInFolder(t *testing.T) {
	bt := newBuildTest(t)
	supervisorSecret(t, bt)
	folder := withDesktop(t, bt, map[string]string{"a": "12345"})
	if err := os.Symlink("../.deskyard", filepath.Join(folder, "state")); err != nil {
		t.Fatal(err)
	}
	if n, _ := bt.s.Desktops.FolderSize("alice"); n != 5 {
		t.Errorf("size %d, want 5", n)
	}
}
