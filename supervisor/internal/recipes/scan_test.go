package recipes

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func file(n int) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(strings.Repeat("x", n)), Mode: 0o644}
}

// home is a desktop's home folder with something in every group, caches,
// Deskyard's own state, and secrets in several groups.
func home() fstest.MapFS {
	return fstest.MapFS{
		// tools
		".local/share/mise/installs/npm-cowsay/1.6.0/bin/cowsay":                                     file(100),
		".local/share/mise/installs/npm-cowsay/1.6.0/lib/node_modules/js-tokens/index.js":            file(10),
		".local/share/mise/installs/npm-cowsay/1.6.0/lib/node_modules/secret-handshake/package.json": file(10),
		".local/bin/mytool":        file(20),
		".npm-global/bin/thing":    file(30),
		".config/mise/config.toml": file(5),
		// flatpak: the installation is reinstalled, the app data copied
		".local/share/flatpak/app/org.gimp.GIMP/current/files/bin/gimp": file(5000),
		".var/app/org.gimp.GIMP/config/gimprc":                          file(7),
		".var/app/org.gimp.GIMP/cache/thumbs/a.png":                     file(900),
		// settings
		".config/app/settings.json":        file(40),
		".config/gh/hosts.yml":             file(3),
		".config/gh/config.yml":            file(4),
		".config/workspace/panel-migrated": file(1), // Deskyard's own marker
		// browser
		".config/chromium-shared/Default/Bookmarks":                            file(50),
		".config/chromium-shared/Default/Cookies":                              file(60),
		".config/chromium-shared/Default/Login Data":                           file(70),
		".config/chromium-shared/Default/Cache/Cache_Data/f1":                  file(8000),
		".config/chromium-shared/SingletonLock":                                file(1),
		".config/chromium-shared/Local State":                                  file(3),
		".config/chromium-shared/Default/Preferences":                          file(5),
		".config/chromium-shared/Default/Extensions/abc/1.0/auth.js":           file(6),
		".config/chromium-shared/Default/History":                              file(900),
		".config/chromium-shared/Default/Local Storage/leveldb/000003.log":     file(80),
		".config/chromium-shared/Default/IndexedDB/https_app.example_0/x":      file(81),
		".config/chromium-shared/Default/Local Extension Settings/abc/000.log": file(82),
		".config/chromium-shared/Default/Extension Cookies":                    file(83),
		".config/chromium-shared/Profile 2/Network/Cookies":                    file(84),
		// dotfiles
		".bashrc":            file(11),
		".bash_history":      file(12),
		".git-credentials":   file(13),
		".npmrc":             file(14),
		".ssh/id_ed25519":    file(15),
		".ssh/config":        file(16),
		".codex/auth.json":   file(17),
		".codex/config.toml": file(18),
		".workspace-seeded":  file(0),
		".Xauthority":        file(1),
		// other
		"foo/.env":                        file(19),
		"foo/main.go":                     file(21),
		"foo/node_modules/x/.env":         file(2), // inside an install: not a secret by name
		"notes.txt":                       file(22),
		".local/share/recently-used.xbel": file(23),
		// caches
		".cache/pip/x":    file(9000),
		".npm/_cacache/y": file(9000),
	}
}

func groupsByName(t *testing.T, gs Groups) map[string]Group {
	t.Helper()
	m := map[string]Group{}
	var names []string
	for _, g := range gs {
		m[g.Name] = g
		names = append(names, g.Name)
	}
	if want := []string{GroupTools, GroupFlatpak, GroupSettings, GroupDotfiles, GroupBrowser, GroupOther}; !reflect.DeepEqual(names, want) {
		t.Fatalf("groups %v, want %v", names, want)
	}
	return m
}

func TestScanGroups(t *testing.T) {
	gs, err := Scan(home())
	if err != nil {
		t.Fatal(err)
	}
	g := groupsByName(t, gs)
	cases := []struct {
		group   string
		bytes   int64
		paths   []string
		secrets []string
	}{
		{GroupTools, 100 + 10 + 10 + 20 + 30 + 5, []string{".config/mise", ".local/bin", ".local/share/mise", ".npm-global"}, nil},
		{GroupFlatpak, 7, []string{".var"}, nil},
		{GroupSettings, 40 + 4, []string{".config/app", ".config/gh"}, []string{".config/gh/hosts.yml"}},
		{GroupDotfiles, 11 + 18, []string{".bashrc", ".codex"},
			[]string{".bash_history", ".codex/auth.json", ".git-credentials", ".npmrc", ".ssh"}},
		{GroupBrowser, 50 + 5 + 6, []string{".config/chromium-shared"},
			[]string{".config/chromium-shared/Default/Cookies", ".config/chromium-shared/Default/Extension Cookies",
				".config/chromium-shared/Default/IndexedDB", ".config/chromium-shared/Default/Local Extension Settings",
				".config/chromium-shared/Default/Local Storage", ".config/chromium-shared/Default/Login Data",
				".config/chromium-shared/Profile 2/Network"}},
		{GroupOther, 21 + 2 + 22 + 23, []string{".local/share/recently-used.xbel", "foo", "notes.txt"}, []string{"foo/.env"}},
	}
	for _, c := range cases {
		got := g[c.group]
		if got.Bytes != c.bytes {
			t.Errorf("%s: %d bytes, want %d", c.group, got.Bytes, c.bytes)
		}
		if !reflect.DeepEqual(got.Paths, c.paths) {
			t.Errorf("%s: paths %q, want %q", c.group, got.Paths, c.paths)
		}
		if !reflect.DeepEqual(got.Secrets, nonNil(c.secrets)) {
			t.Errorf("%s: secrets %q, want %q", c.group, got.Secrets, c.secrets)
		}
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestCachesAndStateExcluded(t *testing.T) {
	for _, p := range []string{
		".cache", ".cache/pip/x", ".npm/_cacache/y", ".var/app/org.gimp.GIMP/cache/thumbs/a.png",
		".config/chromium-shared/Default/Cache/Cache_Data/f1", ".config/chromium-shared/Default/Code Cache/js/1",
		".config/chromium-shared/SingletonLock", ".config/chromium-shared/Local State", ".config/chromium-shared/Default/History",
		".config/chromium-shared/Default/Service Worker/CacheStorage/x", ".workspace-seeded", ".Xauthority", ".config/workspace/x",
		".local/share/flatpak/app/x", ".local/share/Trash/files/a",
	} {
		if g, ok := Classify(p); ok {
			t.Errorf("%s: in %s, want left out", p, g)
		}
	}
}

func TestSecrets(t *testing.T) {
	for _, p := range []string{
		".config/gh/hosts.yml", ".ssh/id_ed25519", ".git-credentials", ".bash_history", ".codex/auth.json",
		".npmrc", "foo/.env", "foo/.env.local", ".zsh_history", ".local/share/fish/fish_history",
		".claude/.credentials.json", ".aws/credentials", ".config/gcloud/x", ".kube/config", ".docker/config.json",
		".netrc", ".config/git/credentials", ".gnupg/private-keys-v1.d/a.key", ".local/share/keyrings/login.keyring",
		"work/server.pem", "work/api_token.txt", "work/client-secret.json", "keys/id_rsa",
		".config/chromium-shared/Default/Network/Cookies", ".config/chromium-shared/Default/Web Data",
		".config/chromium-shared/Default/Local Storage/leveldb/1.log", ".config/chromium-shared/Default/Session Storage/x",
		".config/chromium-shared/Default/IndexedDB/x", ".config/chromium-shared/Default/Extension Cookies",
		".config/chromium-shared/Default/Local Extension Settings/abc/1.log", ".config/chromium-shared/Profile 1/Login Data",
	} {
		if SecretOf(p) == "" {
			t.Errorf("%s: not a secret", p)
		}
	}
	for _, p := range []string{
		".bashrc", ".config/app/settings.json", ".codex/config.toml", "notes.txt", "keyboard/layout.txt",
		".ssh.txt", ".config/gh/config.yml", ".local/share/mise/installs/x/lib/node_modules/js-tokens/index.js",
		".npm-global/lib/node_modules/x/secret.js", "foo/node_modules/x/.env", ".envrc.example.txt",
	} {
		if s := SecretOf(p); s != "" {
			t.Errorf("%s: a secret (%s)", p, s)
		}
	}
	// A secret folder covers what's in it.
	if s := SecretOf(".ssh/id_ed25519"); s != ".ssh" {
		t.Errorf(".ssh/id_ed25519: secret %q, want .ssh", s)
	}
}

func TestSelectionKeepsSecretsOut(t *testing.T) {
	sel := Selection{Groups: []string{GroupSettings, GroupTools}}
	keep := map[string]bool{
		".config/app/settings.json":                              true,
		".config/gh/config.yml":                                  true,
		".config/gh/hosts.yml":                                   false, // a secret inside a ticked group
		".local/share/mise/installs/npm-cowsay/1.6.0/bin/cowsay": true,
		".ssh/id_ed25519":                                        false,
		".codex/auth.json":                                       false,
		".bashrc":                                                false, // dotfiles not ticked
		".cache/x":                                               false,
		".local/share/flatpak/app/x":                             false,
	}
	for p, want := range keep {
		if got, _ := sel.Keep(p); got != want {
			t.Errorf("%s: keep %v, want %v", p, got, want)
		}
	}

	sel.Include = []string{".config/gh/hosts.yml", ".codex/auth.json"}
	for _, p := range []string{".config/gh/hosts.yml", ".codex/auth.json"} {
		if got, secret := sel.Keep(p); !got || !secret {
			t.Errorf("%s: keep %v secret %v, want both (included)", p, got, secret)
		}
	}
	if got, _ := sel.Keep(".codex/config.toml"); got {
		t.Error(".codex/config.toml kept: including a secret doesn't tick its group")
	}
	if got, _ := sel.Keep(".ssh/id_ed25519"); got {
		t.Error(".ssh kept without being included")
	}
}

func TestFlatpaksFromHome(t *testing.T) {
	fsys := fstest.MapFS{
		".local/share/flatpak/repo/config": {Data: []byte("[core]\nrepo_version=1\nmode=bare-user-only\n\n" +
			"[remote \"flathub\"]\nurl=https://dl.flathub.org/repo/\ngpg-verify=true\n\n" +
			"[remote \"gnome-nightly\"]\nurl=https://nightly.gnome.org/repo/\ngpg-verify=true\n\n" +
			"[remote \"unsigned\"]\nurl=https://example.org/repo/\ngpg-verify=false\n")},
		".local/share/flatpak/repo/gnome-nightly.trustedkeys.gpg":                                  {Data: []byte("KEY")},
		".local/share/flatpak/repo/refs/remotes/flathub/app/org.gimp.GIMP/x86_64/stable":           {Data: []byte("c")},
		".local/share/flatpak/repo/refs/remotes/flathub/runtime/org.gnome.Platform/x86_64/48":      {Data: []byte("c")},
		".local/share/flatpak/repo/refs/remotes/gnome-nightly/app/org.gnome.Builder/x86_64/master": {Data: []byte("c")},
		".local/share/flatpak/repo/refs/remotes/unsigned/app/org.example.App/x86_64/stable":        {Data: []byte("c")},
		".local/share/flatpak/app/org.gimp.GIMP/current":                                           {Data: []byte("x")},
		".local/share/flatpak/app/org.gnome.Builder/current":                                       {Data: []byte("x")},
		".local/share/flatpak/app/org.example.App/current":                                         {Data: []byte("x")},
	}
	apps, remotes := FlatpaksInHome(fsys)
	wantApps := []InstalledFlatpak{{ID: "org.example.App", Remote: "unsigned"}, {ID: "org.gimp.GIMP", Remote: "flathub"}, {ID: "org.gnome.Builder", Remote: "gnome-nightly"}}
	if !reflect.DeepEqual(apps, wantApps) {
		t.Errorf("apps %+v, want %+v", apps, wantApps)
	}
	wantRemotes := []UserRemote{{Name: "flathub", URL: "https://dl.flathub.org/repo/"}, {Name: "gnome-nightly", URL: "https://nightly.gnome.org/repo/"}, {Name: "unsigned", URL: "https://example.org/repo/"}}
	if !reflect.DeepEqual(remotes, wantRemotes) {
		t.Errorf("remotes %+v, want %+v", remotes, wantRemotes)
	}

	// Into a recipe: Flathub is always there, a remote needs its key, and an
	// app from a skipped remote is skipped too.
	fps, rms, notes := FlatpakParts(apps, remotes, func(name string) []byte {
		b, _ := fsys.ReadFile(".local/share/flatpak/repo/" + name + ".trustedkeys.gpg")
		return b
	})
	if want := []Flatpak{{ID: "org.gimp.GIMP"}, {ID: "org.gnome.Builder", Remote: "gnome-nightly"}}; !reflect.DeepEqual(fps, want) {
		t.Errorf("flatpaks %+v, want %+v", fps, want)
	}
	if want := []Remote{{Name: "gnome-nightly", URL: "https://nightly.gnome.org/repo/", GPGKey: "S0VZ"}}; !reflect.DeepEqual(rms, want) {
		t.Errorf("remotes %+v, want %+v", rms, want)
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "unsigned") || !strings.Contains(notes[1], "org.example.App") {
		t.Errorf("notes %q", notes)
	}
}

func TestParseFlatpakColumns(t *testing.T) {
	apps := ParseFlatpakList("org.gimp.GIMP\tflathub\norg.gnome.Builder\tgnome-nightly\n\nbad line\n")
	if want := []InstalledFlatpak{{ID: "org.gimp.GIMP", Remote: "flathub"}, {ID: "org.gnome.Builder", Remote: "gnome-nightly"}}; !reflect.DeepEqual(apps, want) {
		t.Errorf("apps %+v", apps)
	}
	rms := ParseFlatpakRemotes("flathub\thttps://dl.flathub.org/repo/\ngnome-nightly\thttps://nightly.gnome.org/repo/\n")
	if want := []UserRemote{{Name: "flathub", URL: "https://dl.flathub.org/repo/"}, {Name: "gnome-nightly", URL: "https://nightly.gnome.org/repo/"}}; !reflect.DeepEqual(rms, want) {
		t.Errorf("remotes %+v", rms)
	}
}
