package recipes

import (
	"encoding/base64"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFile(t, p); got != want {
		t.Errorf("%s differs from the golden file (run with -update to refresh):\n%s", name, got)
	}
}

func fullRecipe() Recipe {
	return Recipe{
		Slug: "full-box", Name: "Full box", Base: "ghcr.io/x/debian-xfce:latest",
		Mise: []string{"node@24", "npm:@openai/codex", "go"},
		Remotes: []Remote{
			{Name: "gnome-nightly", RepoFile: "https://nightly.gnome.org/gnome-nightly.flatpakrepo"},
			{Name: "myrepo", URL: "https://repo.example.com/repo", GPGKey: "QUJD\nREVG"},
		},
		Flatpaks: []Flatpak{
			{ID: "org.gimp.GIMP", Remote: "flathub"},
			{ID: "org.example.Nightly", Remote: "gnome-nightly"},
			{Ref: "https://dl.example.com/app.flatpakref"},
		},
		Commands: "apt-get update\napt-get install -y ripgrep",
		HomeFrom: HomeFromImage, Secrets: true,
	}
}

const baseID = "sha256:0123456789abcdef"

func TestDockerfileGolden(t *testing.T) {
	golden(t, "full.Dockerfile", Dockerfile(fullRecipe(), baseID, true))
	golden(t, "noflatpak.Dockerfile", Dockerfile(fullRecipe(), baseID, false))
	min := Recipe{Slug: "min-box", Name: "Min", Base: "b:latest"}
	golden(t, "minimal.Dockerfile", Dockerfile(min, baseID, true))
}

func TestDockerfileNeverTouchesEntrypointOrCmd(t *testing.T) {
	d := Dockerfile(fullRecipe(), baseID, true)
	for _, l := range strings.Split(d, "\n") {
		for _, bad := range []string{"ENTRYPOINT", "CMD ", "USER "} {
			if strings.HasPrefix(l, bad) && l != "USER root" {
				t.Errorf("unexpected instruction: %s", l)
			}
		}
	}
	if !strings.HasPrefix(d, "FROM "+baseID+"\nUSER root\n") {
		t.Errorf("starts wrong:\n%s", d)
	}
}

func TestDockerfileInvalidInput(t *testing.T) {
	if Dockerfile(Recipe{Slug: "BAD"}, baseID, true) != "" {
		t.Error("invalid recipe produced a Dockerfile")
	}
	for _, id := range []string{"", "x\nRUN evil", "a b", "-x"} {
		if Dockerfile(Recipe{Slug: "ab", Name: "n", Base: "b"}, id, true) != "" {
			t.Errorf("base ID %q produced a Dockerfile", id)
		}
	}
}

// Every build-command byte must reach the script unchanged, and nothing from
// it may show up as a Dockerfile instruction or shell word.
func TestCommandsSurviveUnchanged(t *testing.T) {
	cmds := "echo \"it's $HOME\" `id` \\\n" +
		"cat <<'EOF'\nx\nEOF\n" +
		"RUN rm -rf /\nENTRYPOINT [\"evil\"]\nFROM scratch\n# comment\n" +
		"printf '%s\\n' \"a\\\\b\" $(whoami) ${X:-y}\n"
	r := Recipe{Slug: "cmd-box", Name: "n", Base: "b", Commands: cmds}
	d := Dockerfile(r, baseID, true)

	for _, l := range strings.Split(d, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !regexp.MustCompile(`^(FROM|USER|RUN|COPY) `).MatchString(l) {
			t.Errorf("stray Dockerfile line %q", l)
		}
	}
	if strings.Count(d, "\nRUN ") != 1 {
		t.Errorf("expected exactly one RUN:\n%s", d)
	}
	m := regexp.MustCompile(`printf %s '([A-Za-z0-9+/=]*)' \| base64 -d`).FindStringSubmatch(d)
	if m == nil {
		t.Fatalf("no encoded script in:\n%s", d)
	}
	got, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil || string(got) != cmds {
		t.Errorf("script = %q (%v), want %q", got, err, cmds)
	}
	if strings.Contains(d, "evil") || strings.Contains(d, "whoami") {
		t.Errorf("command text leaked into the Dockerfile:\n%s", d)
	}
}

func TestCommandsCRLF(t *testing.T) {
	d := Dockerfile(Recipe{Slug: "ab", Name: "n", Base: "b", Commands: "a\r\nb\r\n"}, baseID, true)
	m := regexp.MustCompile(`printf %s '([^']*)'`).FindStringSubmatch(d)
	got, _ := base64.StdEncoding.DecodeString(m[1])
	if string(got) != "a\nb\n" {
		t.Errorf("got %q", got)
	}
}

func TestWithVersion(t *testing.T) {
	for in, want := range map[string]string{
		"node@24":               "node@24",
		"go":                    "go@latest",
		"npm:@openai/codex":     "npm:@openai/codex@latest",
		"npm:@openai/codex@1.2": "npm:@openai/codex@1.2",
		"pipx:black":            "pipx:black@latest",
		"node@":                 "node@latest",
	} {
		if got := withVersion(in); got != want {
			t.Errorf("withVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlatpakStepAlwaysAddsFlathub(t *testing.T) {
	for _, fp := range []Flatpak{
		{ID: "org.example.App", Remote: "other"},
		{Ref: "https://dl.example.com/app.flatpakref"},
		{ID: "org.gimp.GIMP"},
	} {
		d := Dockerfile(Recipe{Slug: "ab", Name: "n", Base: "b", Flatpaks: []Flatpak{fp}}, baseID, true)
		add := "remote-add --if-not-exists flathub /etc/workspace/flathub.flatpakrepo"
		i, j := strings.Index(d, add), strings.Index(d, "install -y")
		if i < 0 || j < i {
			t.Errorf("%+v: flathub not added before the install:\n%s", fp, d)
		}
	}
}

func TestSavedRefRemotesKeepTheirGPGKey(t *testing.T) {
	d := Dockerfile(Recipe{Slug: "ab", Name: "n", Base: "b", Flatpaks: []Flatpak{{Ref: "https://dl.example.com/a.flatpakref"}}}, baseID, true)
	for _, want := range []string{
		`/var/lib/flatpak/repo/$name.trustedkeys.gpg`,
		`GPGKey=%s`, `base64 -w0 "$k"`, `not saving flatpak remote $name`,
	} {
		if !strings.Contains(d, want) {
			t.Errorf("missing %q:\n%s", want, d)
		}
	}
	noRef := Dockerfile(Recipe{Slug: "ab", Name: "n", Base: "b", Flatpaks: []Flatpak{{ID: "org.a.B"}}}, baseID, true)
	if strings.Contains(noRef, "trustedkeys") {
		t.Error("remote-saving loop emitted without a .flatpakref")
	}
}
