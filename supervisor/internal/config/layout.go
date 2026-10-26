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
// stores (environments.json, volumes.json, accounts.json, tokens.json,
// networks.json, images.json) sit at the top of Data; the folders below hold
// the rest.
type Layout struct {
	Data   string
	Secret string // 0700: session key, proxy tokens, VPN credentials, TLS storage
	State  string // 0700: instance ID, recovery and network journals, VPN scratch
	Cache  string // disposable: Selkies web clients extracted from images
	Home   string // home/<name>: each environment's /workspace
	Volume string // volume/<name>: each named volume's data
}

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
		return fmt.Errorf("%w: remove %s, or choose another DESKYARD_DATA_DIR (old state is not migrated)", ErrLegacyLayout, old)
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
