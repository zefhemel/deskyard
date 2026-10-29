package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// Layout is where the supervisor keeps things in its data folder. The JSON
// stores (environments.json, volumes.json, accounts.json, links.json,
// credentials.json, networks.json, images.json) sit at the top of Data; the
// folders below hold the rest.
type Layout struct {
	Data   string
	Secret string // 0700: session key, proxy tokens, VPN credentials, TLS storage
	State  string // 0700: instance ID, recovery and network journals, VPN scratch
	Cache  string // disposable: Selkies web clients extracted from images
	Home   string // home/<name>: each computer's /workspace
	Volume string // volume/<name>: each named volume's data

	host string // where Data lives on the engine host; "" when unknown
}

// DataDir is where the supervisor's container mounts the data folder.
const DataDir = "/data"

func NewLayout(data string) Layout {
	return Layout{
		Data:   data,
		Secret: filepath.Join(data, "secret"),
		State:  filepath.Join(data, "state"),
		Cache:  filepath.Join(data, "cache"),
		Home:   filepath.Join(data, "home"),
		Volume: filepath.Join(data, "volume"),
	}
}

// WithHost returns a copy that knows the host folder mounted at Data, so
// ToHost and FromHost can translate between the two.
func (l Layout) WithHost(host string) Layout {
	l.host = ""
	if host != "" {
		l.host = filepath.Clean(host)
	}
	return l
}

// Host is the host folder WithHost set: where Data lives on the engine host,
// or "" when unknown.
func (l Layout) Host() string { return l.host }

// entry is the first path element of p below Data, and whether p is Data
// itself or below it at all.
func (l Layout) entry(p string) (first string, in bool) {
	p = filepath.Clean(p)
	if p == l.Data {
		return "", true
	}
	rest, ok := strings.CutPrefix(p, l.Data+"/")
	if !ok {
		return "", false
	}
	first, _, _ = strings.Cut(rest, "/")
	return first, true
}

// Owns reports whether p is Data itself or one of the layout's own entries
// (home, volume, secret, state, cache or a top-level JSON store) and so
// lives in the data folder's mount. Any other path under Data is not the
// layout's, and is not translated.
func (l Layout) Owns(p string) bool {
	first, in := l.entry(p)
	if !in {
		return false
	}
	switch first {
	case "", "home", "volume", "secret", "state", "cache":
		return true
	}
	// A top-level *.json store, not a deeper file.
	return strings.HasSuffix(first, ".json") && filepath.Clean(p) == filepath.Join(l.Data, first)
}

// ToHost maps a path inside the supervisor's container to the engine host's
// path, for bind mounts the engine resolves on the host. Paths the layout
// does not own, and every path when the host folder is unknown, are
// returned unchanged.
func (l Layout) ToHost(p string) string {
	if l.host == "" || !l.Owns(p) {
		return p
	}
	return filepath.Join(l.host, strings.TrimPrefix(filepath.Clean(p), l.Data))
}

// FromHost is the inverse of ToHost, for paths under the host folder. Only a
// path that maps to one of the layout's entries is translated: a host
// volume elsewhere under the host folder keeps its host path.
func (l Layout) FromHost(p string) string {
	if l.host == "" {
		return p
	}
	c := filepath.Clean(p)
	if c != l.host && !strings.HasPrefix(c, l.host+"/") {
		return p
	}
	if in := filepath.Join(l.Data, strings.TrimPrefix(c, l.host)); l.Owns(in) {
		return in
	}
	return p
}

// HomeOf is the computer's workspace folder.
func (l Layout) HomeOf(name string) string { return filepath.Join(l.Home, name) }

// VolumeOf is the named volume's data folder.
func (l Layout) VolumeOf(name string) string { return filepath.Join(l.Volume, name) }

// Prepare creates the layout's folders. secret/ and state/ are made
// owner-only even when they already exist with wider modes.
func (l Layout) Prepare() error {
	for _, dir := range []string{l.Secret, l.State} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	for _, dir := range []string{l.Cache, l.Home, l.Volume} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ErrLegacyLayout: the data folder holds state from before computer
// definitions, which this supervisor neither migrates nor deletes.
var ErrLegacyLayout = errors.New("the data folder has state from an older Deskyard")

// CheckLegacy refuses a data folder with the old hidden .deskyard/ state
// folder. It creates nothing.
func CheckLegacy(data string) error {
	old := filepath.Join(data, ".deskyard")
	if _, err := os.Lstat(old); err == nil {
		return fmt.Errorf("%w: remove %s, or mount another folder at /data (old state is not migrated)", ErrLegacyLayout, old)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// InstanceID is this supervisor's identity on the engine, kept in
// state/instance and created on first use: 32 hex characters.
func InstanceID(l Layout) (string, error) {
	path := filepath.Join(l.State, "instance")
	b, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if _, decodeErr := hex.DecodeString(id); len(id) != 32 || decodeErr != nil {
			return "", fmt.Errorf("%s is not a valid instance ID", path)
		}
		return id, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	if err := store.WriteFile(path, []byte(id+"\n")); err != nil {
		return "", err
	}
	return id, nil
}
