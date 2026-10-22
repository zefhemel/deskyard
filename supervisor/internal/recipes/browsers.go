package recipes

import (
	"regexp"
	"strings"
)

// Browser profiles are copied from an allow-list. In each profile folder,
// what holds no sign-in (bookmarks, preferences, the extensions' code) is
// kept; each sign-in store (cookies, saved logins, web apps' and
// extensions' storage, sessions) is one secret; the rest (history, caches,
// the browser's own state) is left out.

// browser is one browser's profile layout: root/<profile>/<entry>.
type browser struct {
	root string
	// profile matches a profile folder's name.
	profile *regexp.Regexp
	// rootKeep are files in root itself that are kept.
	rootKeep map[string]bool
	keep     map[string]bool
	signIns  map[string]bool
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Chromium and the browsers built on it share a layout.
var (
	chromiumProfile = regexp.MustCompile(`^(Default|Profile [0-9]+)$`)
	chromiumKeep    = set("Bookmarks", "Bookmarks.bak", "Preferences", "Secure Preferences", "Extensions",
		"Custom Dictionary.txt", "Favicons", "Favicons-journal")
	chromiumSignIns = set(
		"Cookies", "Cookies-journal", "Network",
		"Login Data", "Login Data-journal", "Login Data For Account", "Login Data For Account-journal",
		"Web Data", "Web Data-journal", "Account Web Data", "Account Web Data-journal",
		"Local Storage", "Session Storage", "IndexedDB", "WebStorage", "Storage",
		"databases", "File System", "Shared Storage", "Service Worker",
		"Trust Tokens", "Trust Tokens-journal", "Sessions", "Sync Data",
		"Extension Cookies", "Extension Cookies-journal", "Local Extension Settings",
		"Sync Extension Settings", "Managed Extension Settings", "Extension State",
		"Extension Rules", "Extension Scripts",
	)
)

func chromium(root string) browser {
	return browser{root: root, profile: chromiumProfile, rootKeep: set(), keep: chromiumKeep, signIns: chromiumSignIns}
}

// browserProfile is the shared Chromium's profile (start-chromium).
const browserProfile = ".config/chromium-shared"

var browsers = []browser{
	chromium(browserProfile),
	chromium(".config/chromium"),
	chromium(".config/google-chrome"),
	chromium(".config/BraveSoftware/Brave-Browser"),
	{
		root:     ".mozilla/firefox",
		profile:  regexp.MustCompile(`^[A-Za-z0-9]+\.[A-Za-z0-9_-]+$`), // abcd1234.default-release
		rootKeep: set("profiles.ini", "installs.ini"),
		// Bookmarks come back from bookmarkbackups (places.sqlite is history too).
		keep: set("prefs.js", "user.js", "extensions", "extensions.json", "extension-preferences.json",
			"extension-settings.json", "bookmarkbackups", "chrome", "containers.json", "handlers.json",
			"search.json.mozlz4", "xulstore.json"),
		signIns: set("cookies.sqlite", "cookies.sqlite-wal", "logins.json", "logins-backup.json", "key4.db", "cert9.db",
			"signedInUser.json", "sessionstore.jsonlz4", "sessionstore-backups", "storage", "webappsstore.sqlite",
			"webappsstore.sqlite-wal", "formhistory.sqlite", "storage-sync-v2.sqlite", "storage-sync-v2.sqlite-wal",
			"browser-extension-data"),
	},
}

// browserPath finds the browser p is in, and p's parts below its root.
func browserPath(p string) (*browser, []string) {
	for i := range browsers {
		b := &browsers[i]
		if under(p, b.root) {
			rest := strings.TrimPrefix(strings.TrimPrefix(p, b.root), "/")
			if rest == "" {
				return b, nil
			}
			return b, strings.Split(rest, "/")
		}
	}
	return nil, nil
}

// kept reports whether a path below the root (rp) may be copied.
func (b *browser) kept(rp []string) bool {
	switch {
	case len(rp) == 0:
		return true
	case len(rp) == 1:
		return b.rootKeep[rp[0]] || b.profile.MatchString(rp[0])
	case !b.profile.MatchString(rp[0]):
		return false
	default:
		return b.keep[rp[1]] || b.signIns[rp[1]]
	}
}

// signIn returns the sign-in store rp is in (relative to the root), or "".
func (b *browser) signIn(rp []string) string {
	if len(rp) >= 2 && b.profile.MatchString(rp[0]) && b.signIns[rp[1]] {
		return rp[0] + "/" + rp[1]
	}
	return ""
}

// inKept reports whether rp is inside a kept entry (where names say
// nothing about secrets: the extensions' code).
func (b *browser) inKept(rp []string) bool {
	return len(rp) >= 2 && b.profile.MatchString(rp[0]) && b.keep[rp[1]]
}
