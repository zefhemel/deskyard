package recipes

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

// The groups a desktop's home folder is sorted into when an image is made
// from it, in the order the dialog shows them.
const (
	GroupTools    = "tools"    // installed tools: mise, ~/.local/bin, npm globals, uv tools
	GroupFlatpak  = "flatpak"  // Flatpak apps (installed again) and their data in ~/.var
	GroupSettings = "settings" // ~/.config
	GroupDotfiles = "dotfiles" // the other top-level dotfiles
	GroupBrowser  = "browser"  // browser profiles (browsers.go)
	GroupOther    = "other"    // everything else
)

var groupOrder = []string{GroupTools, GroupFlatpak, GroupSettings, GroupDotfiles, GroupBrowser, GroupOther}

// flatpakInstall is the desktop's per-user Flatpak installation: its apps are
// installed again in the image, never copied.
const flatpakInstall = ".local/share/flatpak"

var toolPaths = []string{".local/share/mise", ".local/bin", ".npm-global", ".local/share/uv", ".config/mise"}

// leftOut is never copied: caches, the Flatpak installation, and Deskyard's
// own runtime state. (Not ~/.config/workspace: the panel migrations' run-once
// markers go with the panel settings, in the settings group, or a desktop
// made from the image would run them again over the user's panel.)
var leftOut = []string{
	".cache", ".npm", ".local/share/Trash", flatpakInstall,
	".cargo/registry", ".cargo/git", ".rustup/downloads", ".gradle/caches", ".m2/repository", "go/pkg/mod",
	".pnpm-store", ".local/share/pnpm/store", ".yarn/cache", ".bun/install/cache",
	".workspace-seeded", ".Xauthority", ".ICEauthority", ".xsession-errors",
	".xsession-errors.old", ".dbus",
	browserProfile + "/SingletonLock", browserProfile + "/SingletonSocket", browserProfile + "/SingletonCookie",
	browserProfile + "/.open",
}

// Chromium's cache folders, anywhere in its profile.
var browserCaches = map[string]bool{
	"Cache": true, "Code Cache": true, "GPUCache": true, "ShaderCache": true, "GrShaderCache": true,
	"GraphiteDawnCache": true, "DawnCache": true, "DawnGraphiteCache": true, "DawnWebGPUCache": true,
	"CacheStorage": true, "ScriptCache": true, "component_crx_cache": true,
}

// Classify gives the group of p (relative to the home folder), or false when
// p is always left out.
func Classify(p string) (group string, ok bool) {
	g, _, ok := classify(p)
	return g, ok
}

// classify also gives the root of p in its group: the folder or file the
// dialog lists.
func classify(p string) (group, root string, ok bool) {
	p = strings.Trim(path.Clean(p), "/")
	if p == "." || p == "" {
		return "", "", false
	}
	for _, d := range leftOut {
		if under(p, d) {
			return "", "", false
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 4 && parts[0] == ".var" && parts[1] == "app" && parts[3] == "cache" {
		return "", "", false
	}
	if b, rp := browserPath(p); b != nil {
		if !b.kept(rp) {
			return "", "", false
		}
		for _, part := range rp {
			if browserCaches[part] {
				return "", "", false
			}
		}
		return GroupBrowser, b.root, true
	}
	for _, d := range toolPaths {
		if under(p, d) {
			return GroupTools, d, true
		}
	}
	switch {
	case parts[0] == ".var":
		return GroupFlatpak, ".var", true
	case parts[0] == ".config":
		return GroupSettings, firstParts(parts, 2), true
	case parts[0] == ".local" && len(parts) > 1 && parts[1] == "share":
		return GroupOther, firstParts(parts, 3), true
	case parts[0] == ".local":
		return GroupOther, firstParts(parts, 2), true
	case strings.HasPrefix(parts[0], "."):
		return GroupDotfiles, parts[0], true
	default:
		return GroupOther, parts[0], true
	}
}

func firstParts(parts []string, n int) string {
	return strings.Join(parts[:min(n, len(parts))], "/")
}

// Group is one group of a scanned home folder.
type Group struct {
	Name string `json:"name"`
	// Bytes is the size of what the group copies: its files without its secrets.
	Bytes int64 `json:"bytes"`
	// Paths are the group's folders and files that hold something besides secrets.
	Paths []string `json:"paths"`
	// Secrets are left out unless ticked one by one.
	Secrets []string `json:"secrets"`
}

// Groups are all six groups, in the dialog's order.
type Groups []Group

// Scanner sorts a home folder's files into groups, one file at a time.
type Scanner struct {
	bytes   map[string]int64
	paths   map[string]map[string]bool
	secrets map[string]map[string]bool
	total   int64
}

func NewScanner() *Scanner {
	s := &Scanner{bytes: map[string]int64{}, paths: map[string]map[string]bool{}, secrets: map[string]map[string]bool{}}
	for _, g := range groupOrder {
		s.paths[g], s.secrets[g] = map[string]bool{}, map[string]bool{}
	}
	return s
}

// Add counts a file (or symbolic link) of size bytes at p.
func (s *Scanner) Add(p string, size int64) {
	g, root, ok := classify(p)
	if !ok {
		return
	}
	s.total += size
	if sec := SecretOf(p); sec != "" {
		if sg, _, ok := classify(sec); ok {
			g = sg
		}
		s.secrets[g][sec] = true
		return
	}
	s.bytes[g] += size
	s.paths[g][root] = true
}

// Total is the size of everything that could be copied, secrets included.
func (s *Scanner) Total() int64 { return s.total }

func (s *Scanner) Groups() Groups {
	out := make(Groups, 0, len(groupOrder))
	for _, g := range groupOrder {
		out = append(out, Group{Name: g, Bytes: s.bytes[g], Paths: sortedKeys(s.paths[g]), Secrets: sortedKeys(s.secrets[g])})
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Selection is what the admin ticked: whole groups, and secrets one by one.
type Selection struct {
	Groups  []string
	Include []string // secrets, as Scan lists them
}

// Keep reports whether p goes into the image, and whether it is (part of) a
// secret. A secret is kept only when it is included itself, whatever its
// group.
func (s Selection) Keep(p string) (keep, secret bool) {
	g, ok := Classify(p)
	if !ok {
		return false, false
	}
	if sec := SecretOf(p); sec != "" {
		return slices.Contains(s.Include, sec), true
	}
	return slices.Contains(s.Groups, g), false
}

// ValidGroup reports whether g names a group.
func ValidGroup(g string) bool { return slices.Contains(groupOrder, g) }

// --- Flatpak ---------------------------------------------------------------------------

// InstalledFlatpak is an app in a desktop's per-user installation.
type InstalledFlatpak struct {
	ID     string `json:"id"`
	Remote string `json:"remote"`
}

// UserRemote is a remote of a desktop's per-user installation.
type UserRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ParseFlatpakList reads `flatpak list --app --columns=application,origin`.
func ParseFlatpakList(out string) []InstalledFlatpak {
	var apps []InstalledFlatpak
	for _, f := range tabLines(out) {
		apps = append(apps, InstalledFlatpak{ID: f[0], Remote: f[1]})
	}
	return apps
}

// ParseFlatpakRemotes reads `flatpak remotes --columns=name,url`.
func ParseFlatpakRemotes(out string) []UserRemote {
	var rms []UserRemote
	for _, f := range tabLines(out) {
		rms = append(rms, UserRemote{Name: f[0], URL: f[1]})
	}
	return rms
}

func tabLines(out string) [][]string {
	var rows [][]string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 2 || strings.TrimSpace(f[0]) == "" {
			continue
		}
		rows = append(rows, []string{strings.TrimSpace(f[0]), strings.TrimSpace(f[1])})
	}
	return rows
}

// FlatpaksInHome reads the apps and remotes of the per-user Flatpak
// installation in a home folder, for a desktop that isn't running: the
// remotes from the repository's config, and each app's remote from the
// repository's refs.
func FlatpaksInHome(home fs.FS) ([]InstalledFlatpak, []UserRemote) {
	repo := flatpakInstall + "/repo"
	var rms []UserRemote
	if b, err := fs.ReadFile(home, repo+"/config"); err == nil {
		var cur string
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if name, ok := strings.CutPrefix(line, `[remote "`); ok {
				cur = strings.TrimSuffix(name, `"]`)
				continue
			}
			if strings.HasPrefix(line, "[") {
				cur = ""
				continue
			}
			if u, ok := strings.CutPrefix(line, "url="); ok && cur != "" {
				rms = append(rms, UserRemote{Name: cur, URL: strings.TrimSpace(u)})
			}
		}
	}
	sort.Slice(rms, func(i, j int) bool { return rms[i].Name < rms[j].Name })

	origin := map[string]string{}
	remotes, _ := fs.ReadDir(home, repo+"/refs/remotes")
	for _, r := range remotes {
		ids, _ := fs.ReadDir(home, repo+"/refs/remotes/"+r.Name()+"/app")
		for _, id := range ids {
			origin[id.Name()] = r.Name()
		}
	}
	var apps []InstalledFlatpak
	ids, _ := fs.ReadDir(home, flatpakInstall+"/app")
	for _, id := range ids {
		if id.IsDir() || id.Type().IsRegular() {
			apps = append(apps, InstalledFlatpak{ID: id.Name(), Remote: origin[id.Name()]})
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps, rms
}

// FlatpakKeyPath is where a per-user remote's GPG key is, in the home folder.
func FlatpakKeyPath(remote string) string {
	return flatpakInstall + "/repo/" + remote + ".trustedkeys.gpg"
}

// FlatpakParts turns a desktop's Flatpak apps and remotes into a recipe's.
// Flathub is always there. Another remote needs its GPG key (key gives it,
// nil when there's none) so the image can verify it, as for remotes a
// .flatpakref adds; a remote without one is left out, and so are its apps.
// notes say what was left out and why.
func FlatpakParts(apps []InstalledFlatpak, remotes []UserRemote, key func(remote string) []byte) ([]Flatpak, []Remote, []string) {
	var (
		rms   []Remote
		notes []string
		kept  = map[string]bool{}
	)
	for _, ur := range remotes {
		if strings.EqualFold(ur.Name, defaultRemote) {
			continue
		}
		k := key(ur.Name)
		if len(k) == 0 {
			notes = append(notes, fmt.Sprintf("Leaving out the Flatpak remote %s: it has no GPG key, so the image couldn't verify it.", ur.Name))
			continue
		}
		rm := Remote{Name: ur.Name, URL: ur.URL, GPGKey: base64.StdEncoding.EncodeToString(k)}
		if err := rm.validate(); err != nil {
			notes = append(notes, fmt.Sprintf("Leaving out the Flatpak remote %s: %v.", ur.Name, err))
			continue
		}
		rms = append(rms, rm)
		kept[ur.Name] = true
	}
	var fps []Flatpak
	for _, a := range apps {
		f := Flatpak{ID: a.ID, Remote: a.Remote}
		switch {
		case strings.EqualFold(a.Remote, defaultRemote):
			f.Remote = ""
		case a.Remote == "":
			notes = append(notes, fmt.Sprintf("Leaving out %s: where it came from isn't known.", a.ID))
			continue
		case !kept[a.Remote]:
			notes = append(notes, fmt.Sprintf("Leaving out %s: its remote %s is left out.", a.ID, a.Remote))
			continue
		}
		if err := f.validate(); err != nil {
			notes = append(notes, fmt.Sprintf("Leaving out %s: %v.", a.ID, err))
			continue
		}
		fps = append(fps, f)
	}
	return fps, rms, notes
}
