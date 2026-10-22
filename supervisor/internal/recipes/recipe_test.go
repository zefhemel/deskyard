package recipes

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func valid() Recipe {
	return Recipe{Slug: "dev-box", Name: "Dev box", Base: "ghcr.io/x/debian-xfce:latest"}
}

func TestValidate(t *testing.T) {
	ok := func(mod func(*Recipe)) Recipe { r := valid(); mod(&r); return r }
	cases := []struct {
		name string
		r    Recipe
		bad  bool
	}{
		{"minimal", valid(), false},
		{"slug uppercase", ok(func(r *Recipe) { r.Slug = "Dev" }), true},
		{"slug too short", ok(func(r *Recipe) { r.Slug = "a" }), true},
		{"slug trailing dash", ok(func(r *Recipe) { r.Slug = "dev-" }), true},
		{"slug slash", ok(func(r *Recipe) { r.Slug = "a/b" }), true},
		{"no name", ok(func(r *Recipe) { r.Name = " " }), true},
		{"newline in name", ok(func(r *Recipe) { r.Name = "a\nb" }), true},
		{"no base", ok(func(r *Recipe) { r.Base = "" }), true},
		{"mise ok", ok(func(r *Recipe) { r.Mise = []string{"node@24", "npm:@openai/codex", "pipx:black", "go"} }), false},
		{"mise uppercase", ok(func(r *Recipe) { r.Mise = []string{"Node@24"} }), true},
		{"mise space", ok(func(r *Recipe) { r.Mise = []string{"node 24"} }), true},
		{"mise quote", ok(func(r *Recipe) { r.Mise = []string{"node'; rm -rf /"} }), true},
		{"mise flag", ok(func(r *Recipe) { r.Mise = []string{"--help"} }), true},
		{"mise empty", ok(func(r *Recipe) { r.Mise = []string{""} }), true},
		{"homeFrom image", ok(func(r *Recipe) { r.HomeFrom = "image" }), false},
		{"homeFrom other", ok(func(r *Recipe) { r.HomeFrom = "desktop" }), true},
		{"remote repo file", ok(func(r *Recipe) {
			r.Remotes = []Remote{{Name: "gnome-nightly", RepoFile: "https://x.org/a.flatpakrepo"}}
		}), false},
		{"remote url+key", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", URL: "https://x.org/repo", GPGKey: "QUJD\nREVG"}} }), false},
		{"remote http", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", RepoFile: "http://x.org/a.flatpakrepo"}} }), true},
		{"remote file scheme", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", RepoFile: "file:///etc/passwd"}} }), true},
		{"remote url with quote", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", RepoFile: "https://x.org/a'b"}} }), true},
		{"remote url with newline", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", RepoFile: "https://x.org/a\nRUN x"}} }), true},
		{"remote url no key", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", URL: "https://x.org/repo"}} }), true},
		{"remote bad key", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1", URL: "https://x.org/repo", GPGKey: "a'b"}} }), true},
		{"remote both forms", ok(func(r *Recipe) {
			r.Remotes = []Remote{{Name: "r1", RepoFile: "https://x.org/a.flatpakrepo", URL: "https://x.org/repo", GPGKey: "QUJD"}}
		}), true},
		{"remote neither", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "r1"}} }), true},
		{"remote flathub", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "flathub", RepoFile: "https://x.org/a.flatpakrepo"}} }), true},
		{"remote Flathub case", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "FlatHub", RepoFile: "https://x.org/a.flatpakrepo"}} }), true},
		{"remote bad name", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "a b", RepoFile: "https://x.org/a.flatpakrepo"}} }), true},
		{"remote dotdot name", ok(func(r *Recipe) { r.Remotes = []Remote{{Name: "../x", RepoFile: "https://x.org/a.flatpakrepo"}} }), true},
		{"remote duplicate", ok(func(r *Recipe) {
			rm := Remote{Name: "r1", RepoFile: "https://x.org/a.flatpakrepo"}
			r.Remotes = []Remote{rm, rm}
		}), true},
		{"flatpak id+remote", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "org.gimp.GIMP", Remote: "flathub"}} }), false},
		{"flatpak id default remote", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "org.gimp.GIMP"}} }), false},
		{"flatpak ref", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{Ref: "https://x.org/a.flatpakref"}} }), false},
		{"flatpak id+ref", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "org.gimp.GIMP", Ref: "https://x.org/a.flatpakref"}} }), true},
		{"flatpak remote+ref", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{Remote: "r1", Ref: "https://x.org/a.flatpakref"}} }), true},
		{"flatpak empty", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{}} }), true},
		{"flatpak not reverse dns", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "gimp"}} }), true},
		{"flatpak id injection", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "org.a.B; rm -rf /"}} }), true},
		{"flatpak bad remote", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{ID: "org.a.B", Remote: "x y"}} }), true},
		{"flatpak ref http", ok(func(r *Recipe) { r.Flatpaks = []Flatpak{{Ref: "http://x.org/a.flatpakref"}} }), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.r.Validate()
			if c.bad != (err != nil) {
				t.Fatalf("Validate() = %v, want error=%v", err, c.bad)
			}
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "recipes.json")
	s := NewStore(path)
	if rs, err := s.List(); err != nil || len(rs) != 0 {
		t.Fatalf("empty List = %v, %v", rs, err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
	full := Recipe{
		Slug: "zeta", Name: "Zeta", Base: "b:latest", Mise: []string{"node@24"},
		Remotes:  []Remote{{Name: "r1", RepoFile: "https://x.org/a.flatpakrepo"}},
		Flatpaks: []Flatpak{{ID: "org.gimp.GIMP", Remote: "flathub"}, {Ref: "https://x.org/a.flatpakref"}},
		Commands: "echo 'hi' $HOME", HomeFrom: "image", Secrets: true,
	}
	alpha := valid()
	alpha.Slug = "alpha"
	for _, r := range []Recipe{full, alpha} {
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewStore(path).List() // a fresh store reads the file
	if err != nil || len(got) != 2 || got[0].Slug != "alpha" || !reflect.DeepEqual(got[1], full) {
		t.Fatalf("List = %+v, %v", got, err)
	}
	alpha.Name = "Alpha 2"
	if err := s.Put(alpha); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Get("alpha"); r.Name != "Alpha 2" {
		t.Fatalf("Put did not replace: %+v", r)
	}
	if rs, _ := s.List(); len(rs) != 2 {
		t.Fatalf("replace added a duplicate: %d", len(rs))
	}
	if err := s.Put(Recipe{Slug: "BAD"}); err == nil {
		t.Fatal("Put accepted an invalid recipe")
	}
	if err := s.Delete("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v", err)
	}
	if rs, _ := s.List(); len(rs) != 1 || rs[0].Slug != "zeta" {
		t.Fatalf("after delete: %+v", rs)
	}
}

func TestRecipeJSONKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	r := Recipe{Slug: "ab", Name: "x", Base: "b", HomeFrom: "image", Remotes: []Remote{{Name: "r", URL: "https://a.b/c", GPGKey: "QUJD"}}}
	if err := NewStore(path).Put(r); err != nil {
		t.Fatal(err)
	}
	b := readFile(t, path)
	for _, k := range []string{`"homeFrom"`, `"gpgKey"`, `"slug"`} {
		if !strings.Contains(b, k) {
			t.Errorf("JSON lacks %s:\n%s", k, b)
		}
	}
}
