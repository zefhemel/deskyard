package server

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for name := range ctx {
		for _, out := range []string{"hosts.yml", ".ssh", ".cache", ".bashrc", "flatpak", "config.toml"} {
			if strings.Contains(name, out) {
				t.Errorf("%s is in the build context", name)
			}
		}
	}
	df := bt.e.dockerfile
	for _, want := range []string{"COPY home-template/ /opt/deskyard/home-template/", "install -y --noninteractive 'extra' 'org.example.App'",
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
	if len(bt.e.context) != 2 || bt.e.context["home-template/"] != "<dir>" || !strings.Contains(bt.e.dockerfile, "COPY home-template/") {
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
