package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Server is one saved server: a supervisor the CLI signed in to, with the
// client credential it got and the computer commands use by default.
type Server struct {
	Name       string `json:"-"`
	URL        string `json:"url"` // supervisor base URL, no trailing slash
	Username   string `json:"username"`
	Credential string `json:"credential"`         // dyc_…
	Computer   string `json:"computer,omitempty"` // default computer; may be empty
}

// Config is the content of servers.json.
type Config struct {
	Default string             `json:"default,omitempty"`
	Servers map[string]*Server `json:"servers"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidName reports whether name can be used as a server name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// DefaultPath is $XDG_CONFIG_HOME/deskyard/servers.json, falling
// back to ~/.config/deskyard/servers.json.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("can't find the home directory to keep servers in: %v (set XDG_CONFIG_HOME)", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "deskyard", "servers.json"), nil
}

// Load reads the config at path; a missing file is an empty config.
func Load(path string) (*Config, error) {
	c := &Config{Servers: map[string]*Server{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v): fix or delete it", path, err)
	}
	if c.Servers == nil {
		c.Servers = map[string]*Server{}
	}
	for name, s := range c.Servers {
		if s == nil {
			delete(c.Servers, name)
			continue
		}
		s.Name = name
	}
	return c, nil
}

// Save writes the config atomically with mode 0600.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".servers-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Sorted returns the servers sorted by name.
func (c *Config) Sorted() []*Server {
	out := make([]*Server, 0, len(c.Servers))
	for _, s := range c.Servers {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Add stores s under s.Name, replacing any server with that name. The first
// server becomes the default.
func (c *Config) Add(s *Server) {
	c.Servers[s.Name] = s
	if c.Default == "" || c.Servers[c.Default] == nil {
		c.Default = s.Name
	}
}

// Remove deletes a server; it reports whether it existed.
func (c *Config) Remove(name string) bool {
	if _, ok := c.Servers[name]; !ok {
		return false
	}
	delete(c.Servers, name)
	if c.Default == name {
		c.Default = ""
	}
	return true
}

// CredentialID is the id part of a client credential dyc_<id>_<secret>, or
// "" when cred isn't one.
func CredentialID(cred string) string {
	rest, ok := strings.CutPrefix(cred, "dyc_")
	if !ok {
		return ""
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return ""
	}
	return id
}

// MaskCredential shows a credential's ID part only: dyc_0a1b2c3d_… .
func MaskCredential(cred string) string {
	if id := CredentialID(cred); id != "" {
		return "dyc_" + id + "_…"
	}
	return "…"
}

// DefaultName is the server name `deskyard login` picks for a supervisor
// base URL: its host without the port, or "" when that isn't a usable name.
func DefaultName(base string) string {
	u, err := url.Parse(base)
	if err != nil || !ValidName(u.Hostname()) {
		return ""
	}
	return u.Hostname()
}

// UnmarshalJSON reads a server entry, taking the default computer from the
// legacy "environment" key of older servers.json files when "computer" is
// absent. Saving writes only "computer". Removable once every install has
// started on this version; see the Computer terminology feature page,
// "Compatibility code".
func (s *Server) UnmarshalJSON(data []byte) error {
	type plain Server
	var aux struct {
		plain
		LegacyEnvironment string `json:"environment"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*s = Server(aux.plain)
	if s.Computer == "" {
		s.Computer = aux.LegacyEnvironment
	}
	return nil
}
