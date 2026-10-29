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
// stores (environments.json, volumes.json, accounts.json, networks.json,
// images.json) sit at the top of Data; the folders below hold the rest.
type Layout struct {
	Data   string
	Secret string // 0700: session key, proxy tokens, VPN credentials, TLS storage
	State  string // 0700: instance ID, recovery/network journals, VPN scratch, mounted CLI builds
	Cache  string // disposable: Selkies web clients extracted from images
	Home   string // home/<name>: each environment's /workspace
	Volume string // volume/<name>: each named volume's data

	host string // where Data is on the engine host
}

// DataDir is where the supervisor's container mounts the data folder.
const DataDir = "/data"

// NewLayout is the layout of the data folder data, which the engine host
// has at the same path until WithHost says otherwise.
func NewLayout(data string) Layout {
	return Layout{
		Data:   data,
		Secret: filepath.Join(data, "secret"),
		State:  filepath.Join(data, "state"),
		Cache:  filepath.Join(data, "cache"),
		Home:   filepath.Join(data, "home"),
		Volume: filepath.Join(data, "volume"),
		host:   filepath.Clean(data),
	}
}

// WithHost returns a copy whose data folder is host on the engine host.
func (l Layout) WithHost(host string) Layout {
	l.host = filepath.Clean(host)
	return l
}

// Host is where Data is on the engine host.
func (l Layout) Host() string { return l.host }

// ToHost is where a path in the data folder is on the engine host, to show
// it to people, who know the host's folder. The engine's own mounts are
// translated by engine.WithDataFolder. A path outside Data is returned as
// it is.
func (l Layout) ToHost(p string) string {
	rel, err := filepath.Rel(l.Data, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return p
	}
	return filepath.Join(l.host, rel)
}

// HomeOf is the environment's workspace folder.
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

// ErrLegacyLayout: the data folder holds state from before environment
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
