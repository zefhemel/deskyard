package recipes

import (
	"path"
	"regexp"
	"strings"
)

// What looks like a secret in a home folder, by path and name only: file
// contents are never read. A secret is never copied into an image unless the
// admin ticks it, and ticking any marks the image "contains secrets".
//
// Paths are relative to the home folder, with slashes and no leading "./".

// secretPaths are known places, relative to the home folder: a folder covers
// everything in it.
var secretPaths = []string{
	// SSH, GPG and keyrings.
	".ssh",
	".gnupg",
	".password-store",
	".local/share/keyrings",
	// Git credentials.
	".git-credentials",
	".config/git/credentials",
	// Tool logins.
	".codex/auth.json",
	".claude/.credentials.json",
	".claude.json",
	".gemini/oauth_creds.json",
	".config/gh/hosts.yml",
	".config/hub",
	".config/github-copilot",
	".docker/config.json",
	".vault-token",
	".terraform.d/credentials.tfrc.json",
	".cargo/credentials",
	".cargo/credentials.toml",
	".gem/credentials",
	".config/rclone/rclone.conf",
	// Cloud CLIs.
	".aws",
	".azure",
	".config/gcloud",
	".kube",
	".oci",
	".config/doctl",
	// Credential files.
	".npmrc",
	".netrc",
	".pypirc",
	".pgpass",
	// Shell history kept outside the home folder's top level.
	".local/share/fish/fish_history",
}

// secretNames are file or folder names that are secrets wherever they are
// (outside installed software, see notScanned).
var secretNames = map[string]bool{
	// Credential files.
	".npmrc": true, ".netrc": true, ".pypirc": true, ".pgpass": true, ".git-credentials": true,
	// Browser cookies and logins (Chromium, Firefox).
	"Cookies": true, "Cookies-journal": true,
	"Login Data": true, "Login Data-journal": true,
	"Login Data For Account": true, "Login Data For Account-journal": true,
	"Web Data": true, "Web Data-journal": true,
	"cookies.sqlite": true, "logins.json": true, "key4.db": true,
	// Shell history.
	".histfile": true, ".zhistory": true, ".lesshst": true, "fish_history": true,
}

var (
	// Shell and REPL history: .bash_history, .python_history, …
	historyRE = regexp.MustCompile(`^\..+_history$`)
	// .env files: .env, .env.local, .env.production, …
	envRE = regexp.MustCompile(`^\.env(\..+)?$`)
	// SSH private keys, wherever they are (not the .pub halves).
	sshKeyRE = regexp.MustCompile(`^id_(rsa|dsa|ecdsa|ed25519)(_sk)?$`)
	// Key and certificate files.
	keyExtRE = regexp.MustCompile(`(?i)\.(pem|key|p12|pfx|jks|keystore|kdbx|ppk)$`)
	// Named like a token or key: a whole word of the name, so "keyboard"
	// and "js-tokens" don't count but "api_token.txt" does.
	wordRE = regexp.MustCompile(`(?i)(^|[._-])(tokens?|secrets?|credentials?|passwords?|passwd|apikeys?|api[_-]keys?|private[_-]?keys?|auth)([._-]|$)`)
)

// notScanned are folders of installed software, where names say nothing
// about secrets (node_modules is full of "token" and "secret" packages).
// The known paths still apply there.
var notScanned = []string{".local/share/mise", ".npm-global", ".local/share/uv", ".local/bin", ".local/share/flatpak"}

// SecretOf returns the secret that covers p: p itself or the folder above it
// that looks like a secret, or "" when p isn't one.
func SecretOf(p string) string {
	p = strings.Trim(path.Clean(p), "/")
	for _, s := range secretPaths {
		if under(p, s) {
			return s
		}
	}
	parts := strings.Split(p, "/")
	for i, name := range parts {
		if name == "node_modules" || name == "site-packages" || name == ".git" ||
			(name == "Extensions" && under(p, browserProfile)) {
			return ""
		}
		prefix := strings.Join(parts[:i+1], "/")
		if inInstalled(prefix) {
			return ""
		}
		if secretName(name) {
			return prefix
		}
	}
	return ""
}

func inInstalled(p string) bool {
	for _, d := range notScanned {
		if under(p, d) {
			return true
		}
	}
	return false
}

func secretName(name string) bool {
	return secretNames[name] || historyRE.MatchString(name) || envRE.MatchString(name) ||
		sshKeyRE.MatchString(name) || keyExtRE.MatchString(name) || wordRE.MatchString(name)
}

// IsSecret reports whether p is a secret as Scan lists them (not a file
// inside one).
func IsSecret(p string) bool {
	return p != "" && SecretOf(p) == p
}

// under reports whether p is dir or inside it.
func under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}
