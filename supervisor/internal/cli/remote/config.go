package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Remote is one saved remote: a local alias for a desktop reached through
// a supervisor's agent API with a token.
type Remote struct {
	Name    string `json:"-"`
	URL     string `json:"url"`     // as given to `remote add`
	Base    string `json:"base"`    // supervisor base URL
	Desktop string `json:"desktop"` // the desktop's name on the supervisor
	Token   string `json:"token"`
}

// Config is the content of remotes.json.
type Config struct {
	Default string             `json:"default,omitempty"`
	Remotes map[string]*Remote `json:"remotes"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidName reports whether name can be used as a remote name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// DefaultPath is $XDG_CONFIG_HOME/deskyard/remotes.json, falling
// back to ~/.config/deskyard/remotes.json.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("can't find the home directory to keep remotes in: %v (set XDG_CONFIG_HOME)", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "deskyard", "remotes.json"), nil
}

// Load reads the config at path; a missing file is an empty config.
func Load(path string) (*Config, error) {
	c := &Config{Remotes: map[string]*Remote{}}
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
	if c.Remotes == nil {
		c.Remotes = map[string]*Remote{}
	}
	for name, r := range c.Remotes {
		if r == nil {
			delete(c.Remotes, name)
			continue
		}
		r.Name = name
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
	tmp, err := os.CreateTemp(dir, ".remotes-*.json")
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

// Sorted returns the remotes sorted by name.
func (c *Config) Sorted() []*Remote {
	out := make([]*Remote, 0, len(c.Remotes))
	for _, r := range c.Remotes {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Add stores r under r.Name, replacing any remote with that name. The first
// remote becomes the default.
func (c *Config) Add(r *Remote) {
	c.Remotes[r.Name] = r
	if c.Default == "" || c.Remotes[c.Default] == nil {
		c.Default = r.Name
	}
}

// Remove deletes a remote; it reports whether it existed.
func (c *Config) Remove(name string) bool {
	if _, ok := c.Remotes[name]; !ok {
		return false
	}
	delete(c.Remotes, name)
	if c.Default == name {
		c.Default = ""
	}
	return true
}

// MaskToken shows a token's ID part only: dyt_abc123_… .
func MaskToken(tok string) string {
	if len(tok) > 4 && tok[:4] == "dyt_" {
		rest := tok[4:]
		for i := 0; i < len(rest); i++ {
			if rest[i] == '_' {
				return tok[:4+i+1] + "…"
			}
		}
	}
	if len(tok) > 8 {
		return tok[:8] + "…"
	}
	return "…"
}
