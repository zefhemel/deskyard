// Package recipes describes custom desktop images (a base plus mise tools,
// Flatpak remotes and apps, build commands and an optional starting home),
// turns them into Dockerfiles, and stores them.
package recipes

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// HomeFromImage means: the home template is copied out of the current image
// when the recipe is rebuilt.
const HomeFromImage = "image"

// Recipe is the JSON form kept in .deskyard/recipes.json and in the image's
// deskyard.recipe label.
type Recipe struct {
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Base     string    `json:"base"` // catalog ref
	Mise     []string  `json:"mise,omitempty"`
	Remotes  []Remote  `json:"remotes,omitempty"`
	Flatpaks []Flatpak `json:"flatpaks,omitempty"`
	Commands string    `json:"commands,omitempty"`
	HomeFrom string    `json:"homeFrom,omitempty"` // "" or "image"
	Secrets  bool      `json:"secrets,omitempty"`
}

// Remote is an extra Flatpak remote: a .flatpakrepo file URL, or a repository
// URL with its base64-encoded GPG key.
type Remote struct {
	Name     string `json:"name"`
	RepoFile string `json:"repoFile,omitempty"`
	URL      string `json:"url,omitempty"`
	GPGKey   string `json:"gpgKey,omitempty"`
}

// Flatpak is an app: an ID from a remote (Flathub when Remote is empty), or a
// .flatpakref URL.
type Flatpak struct {
	ID     string `json:"id,omitempty"`
	Remote string `json:"remote,omitempty"`
	Ref    string `json:"ref,omitempty"`
}

var (
	// Same rule as desktop names (internal/desktops).
	slugRE       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)
	miseRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9:@/._+-]*$`)
	remoteNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	flatpakIDRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(\.[A-Za-z0-9][A-Za-z0-9_-]*)+$`)
	// What may appear in a URL that lands in a shell word: no quotes,
	// backslashes, whitespace or backticks.
	urlCharsRE = regexp.MustCompile(`^[A-Za-z0-9._~:/?#@%&=+,-]+$`)
)

const defaultRemote = "flathub"

// Validate checks every rule that keeps the generated Dockerfile safe. Always
// call it before Dockerfile.
func (r Recipe) Validate() error {
	if !slugRE.MatchString(r.Slug) {
		return fmt.Errorf("slug %q: use lowercase letters, digits and dashes, 2-32 characters, starting with a letter", r.Slug)
	}
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 100 || strings.ContainsAny(r.Name, "\x00\r\n") {
		return errors.New("name: required, one line, at most 100 characters")
	}
	if strings.TrimSpace(r.Base) == "" {
		return errors.New("base: required")
	}
	for _, m := range r.Mise {
		if !miseRE.MatchString(m) {
			return fmt.Errorf("mise tool %q: use lowercase letters, digits and : @ / . _ + -", m)
		}
	}
	if r.HomeFrom != "" && r.HomeFrom != HomeFromImage {
		return fmt.Errorf("homeFrom %q: must be empty or %q", r.HomeFrom, HomeFromImage)
	}
	seen := map[string]bool{}
	for _, rm := range r.Remotes {
		if err := rm.validate(); err != nil {
			return err
		}
		if seen[rm.Name] {
			return fmt.Errorf("remote %q is listed twice", rm.Name)
		}
		seen[rm.Name] = true
	}
	for _, f := range r.Flatpaks {
		if err := f.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (rm Remote) validate() error {
	if !remoteNameRE.MatchString(rm.Name) {
		return fmt.Errorf("remote name %q: use letters, digits and . _ -", rm.Name)
	}
	if strings.EqualFold(rm.Name, defaultRemote) {
		return fmt.Errorf("remote name %q is taken by Flathub", rm.Name)
	}
	switch {
	case rm.RepoFile != "" && (rm.URL != "" || rm.GPGKey != ""):
		return fmt.Errorf("remote %q: give a .flatpakrepo URL, or a URL with a GPG key, not both", rm.Name)
	case rm.RepoFile != "":
		return checkURL("remote "+rm.Name+" repo file", rm.RepoFile)
	case rm.URL != "":
		if err := checkURL("remote "+rm.Name+" URL", rm.URL); err != nil {
			return err
		}
		if cleanKey(rm.GPGKey) == "" {
			return fmt.Errorf("remote %q: a repository URL needs its GPG key", rm.Name)
		}
		if _, err := base64.StdEncoding.DecodeString(cleanKey(rm.GPGKey)); err != nil {
			return fmt.Errorf("remote %q: the GPG key must be base64", rm.Name)
		}
		return nil
	default:
		return fmt.Errorf("remote %q: needs a .flatpakrepo URL, or a URL with a GPG key", rm.Name)
	}
}

func (f Flatpak) validate() error {
	switch {
	case f.Ref != "" && (f.ID != "" || f.Remote != ""):
		return errors.New("flatpak: give an ID (with its remote) or a .flatpakref URL, not both")
	case f.Ref != "":
		return checkURL("flatpak ref", f.Ref)
	case f.ID == "":
		return errors.New("flatpak: needs an ID or a .flatpakref URL")
	}
	if !flatpakIDRE.MatchString(f.ID) {
		return fmt.Errorf("flatpak ID %q: expected reverse-DNS, like org.gimp.GIMP", f.ID)
	}
	if f.Remote != "" && !remoteNameRE.MatchString(f.Remote) {
		return fmt.Errorf("flatpak %s: remote name %q: use letters, digits and . _ -", f.ID, f.Remote)
	}
	return nil
}

func checkURL(what, s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || !urlCharsRE.MatchString(s) {
		return fmt.Errorf("%s %q: must be an https: URL", what, s)
	}
	return nil
}

// cleanKey drops whitespace, so a pasted key may span lines.
func cleanKey(s string) string {
	return strings.Join(strings.Fields(s), "")
}
