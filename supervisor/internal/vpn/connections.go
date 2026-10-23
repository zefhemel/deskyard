// Package vpn owns saved outbound VPN configuration and local exception policy.
package vpn

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

var (
	ErrNotFound   = errors.New("VPN connection not found")
	ErrConflict   = errors.New("VPN connection conflict")
	ErrValidation = errors.New("invalid VPN configuration")
)

// ValidationError identifies the rejected field without including its value.
type ValidationError struct{ Field, Reason string }

func (e *ValidationError) Error() string { return "invalid VPN " + e.Field + ": " + e.Reason }
func (e *ValidationError) Unwrap() error { return ErrValidation }
func invalid(field, reason string) error { return &ValidationError{field, reason} }

// Connection is the public, secret-free saved configuration.
type Connection struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Provider              string `json:"provider"`
	Country               string `json:"country"`
	Revision              uint64 `json:"revision"`
	CredentialsConfigured bool   `json:"credentialsConfigured"`
}

// Input is write-only. Nil credential fields on Update retain their saved value.
// Name, provider and country describe the complete candidate configuration.
type Input struct {
	Name     string  `json:"name"`
	Provider string  `json:"provider"`
	Country  string  `json:"country"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

func (Input) MarshalJSON() ([]byte, error) { return nil, errors.New("VPN input is write-only") }

// CredentialFiles contains engine-host paths for read-only secret mounts. The
// supervisor's state directory must be mounted at the same host path in Docker.
// Paths are never part of public JSON responses.
type CredentialFiles struct {
	Username string `json:"-"`
	Password string `json:"-"`
}

type connectionRecord struct {
	Connection
	CredentialRef string `json:"credentialRef"`
}
type connectionFile struct {
	Connections []connectionRecord `json:"connections"`
}

// Store keeps metadata under stateDir/vpn and secrets in owner-only files. The
// caller passes the supervisor's private .deskyard directory, not its workspace.
// One supervisor owns a state directory; the mutex serializes its operations.
type Store struct {
	mu  sync.Mutex
	dir string
}

func NewStore(stateDir string) (*Store, error) {
	dir, err := filepath.Abs(filepath.Join(stateDir, "vpn"))
	if err != nil {
		return nil, err
	}
	for _, p := range []string{dir, filepath.Join(dir, "secrets")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(p, 0o700); err != nil {
			return nil, err
		}
	}
	s := &Store{dir: dir}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() ([]connectionRecord, error) {
	var f connectionFile
	if err := store.ReadJSON(filepath.Join(s.dir, "connections.json"), &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, r := range f.Connections {
		// Validate references before they can be used to construct secret paths.
		if !validID(r.ID) || !validID(r.CredentialRef) || r.Revision == 0 || ids[r.ID] || names[strings.ToLower(r.Name)] {
			return nil, errors.New("invalid VPN connection metadata")
		}
		if _, err := normalizeInput(Input{Name: r.Name, Provider: r.Provider, Country: r.Country}, false); err != nil {
			return nil, errors.New("invalid VPN connection metadata")
		}
		ids[r.ID], names[strings.ToLower(r.Name)] = true, true
	}
	return f.Connections, nil
}
func (s *Store) save(rs []connectionRecord) error {
	return store.WriteJSON(filepath.Join(s.dir, "connections.json"), connectionFile{rs})
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func find(rs []connectionRecord, id string) int {
	for i := range rs {
		if rs[i].ID == id {
			return i
		}
	}
	return -1
}
func duplicate(rs []connectionRecord, name, id string) bool {
	for _, r := range rs {
		if r.ID != id && strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

func (s *Store) List() ([]Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Connection)
	}
	slices.SortFunc(out, func(a, b Connection) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}
func (s *Store) Get(id string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	i := find(rs, id)
	if i < 0 {
		return Connection{}, ErrNotFound
	}
	return rs[i].Connection, nil
}
func (s *Store) Create(in Input) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := normalizeInput(in, true)
	if err != nil {
		return Connection{}, err
	}
	rs, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	if duplicate(rs, in.Name, "") {
		return Connection{}, ErrConflict
	}
	id, err := newID()
	if err != nil {
		return Connection{}, err
	}
	ref, err := s.writeCredentials(id, *in.Username, *in.Password)
	if err != nil {
		return Connection{}, err
	}
	r := connectionRecord{Connection: Connection{ID: id, Name: in.Name, Provider: in.Provider, Country: in.Country, Revision: 1, CredentialsConfigured: true}, CredentialRef: ref}
	if err = s.save(append(rs, r)); err != nil {
		os.RemoveAll(filepath.Join(s.dir, "secrets", id))
		return Connection{}, err
	}
	return r.Connection, nil
}
func (s *Store) Update(id string, in Input) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := normalizeInput(in, false)
	if err != nil {
		return Connection{}, err
	}
	rs, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	i := find(rs, id)
	if i < 0 {
		return Connection{}, ErrNotFound
	}
	if duplicate(rs, in.Name, id) {
		return Connection{}, ErrConflict
	}
	previous := rs[i]
	r := previous
	r.Name, r.Provider, r.Country = in.Name, in.Provider, in.Country
	r.Revision++
	changed := in.Username != nil || in.Password != nil
	if changed {
		files, err := s.credentialFiles(id, previous.CredentialRef)
		if err != nil {
			return Connection{}, err
		}
		username, password := in.Username, in.Password
		if username == nil {
			b, err := os.ReadFile(files.Username)
			if err != nil {
				return Connection{}, errors.New("VPN credentials unavailable")
			}
			v := string(b)
			username = &v
		}
		if password == nil {
			b, err := os.ReadFile(files.Password)
			if err != nil {
				return Connection{}, errors.New("VPN credentials unavailable")
			}
			v := string(b)
			password = &v
		}
		r.CredentialRef, err = s.writeCredentials(id, *username, *password)
		if err != nil {
			return Connection{}, err
		}
	}
	rs[i] = r
	if err = s.save(rs); err != nil {
		if changed {
			os.RemoveAll(filepath.Join(s.dir, "secrets", id, r.CredentialRef))
		}
		return Connection{}, err
	}
	// Preserve old immutable files: a running gateway may still mount them. Delete
	// removes every version for this connection after its users have been removed.
	return r.Connection, nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return err
	}
	i := find(rs, id)
	if i < 0 {
		return ErrNotFound
	}
	if err = s.save(slices.Delete(rs, i, i+1)); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, "secrets", id))
}

// SecretFiles resolves private file paths for internal runtime configuration.
// Missing credentials fail closed; public DTOs never contain these paths.
func (s *Store) SecretFiles(id string) (CredentialFiles, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return CredentialFiles{}, err
	}
	i := find(rs, id)
	if i < 0 {
		return CredentialFiles{}, ErrNotFound
	}
	return s.credentialFiles(id, rs[i].CredentialRef)
}
func (s *Store) credentialFiles(id, ref string) (CredentialFiles, error) {
	files := CredentialFiles{filepath.Join(s.dir, "secrets", id, ref, "username"), filepath.Join(s.dir, "secrets", id, ref, "password")}
	for _, p := range []string{files.Username, files.Password} {
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Mode().Perm() != 0o600 {
			return CredentialFiles{}, errors.New("VPN credentials unavailable or insecure")
		}
	}
	return files, nil
}
func (s *Store) writeCredentials(id, username, password string) (string, error) {
	ref, err := newID()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.dir, "secrets", id, ref)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", err
	}
	if err = os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	for name, value := range map[string]string{"username": username, "password": password} {
		if err := store.WriteFile(filepath.Join(dir, name), []byte(value)); err != nil {
			os.RemoveAll(dir)
			return "", errors.New("cannot store VPN credentials")
		}
	}
	return ref, nil
}

// Countries returns the supported guided OpenVPN country choices. A copy keeps
// callers from changing the store's validation policy. Availability is verified
// by a connection test, not by this static country-name validation.
func Countries() []string { return slices.Clone(countries) }

var countries = []string{"Albania", "Argentina", "Australia", "Austria", "Belgium", "Bosnia and Herzegovina", "Brazil", "Bulgaria", "Canada", "Chile", "Costa Rica", "Croatia", "Cyprus", "Czech Republic", "Denmark", "Estonia", "Finland", "France", "Georgia", "Germany", "Greece", "Hong Kong", "Hungary", "Iceland", "India", "Indonesia", "Ireland", "Israel", "Italy", "Japan", "Latvia", "Lithuania", "Luxembourg", "Malaysia", "Mexico", "Moldova", "Netherlands", "New Zealand", "North Macedonia", "Norway", "Poland", "Portugal", "Romania", "Serbia", "Singapore", "Slovakia", "Slovenia", "South Africa", "South Korea", "Spain", "Sweden", "Switzerland", "Taiwan", "Thailand", "Turkey", "Ukraine", "United Arab Emirates", "United Kingdom", "United States", "Vietnam"}

func normalizeInput(in Input, create bool) (Input, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	if in.Name == "" || len(in.Name) > 100 || strings.ContainsAny(in.Name, "\x00\r\n") {
		return Input{}, invalid("name", "must be a display name of at most 100 bytes")
	}
	if in.Provider != "nordvpn" {
		return Input{}, invalid("provider", "only NordVPN is supported")
	}
	country := ""
	for _, c := range countries {
		if strings.EqualFold(c, strings.TrimSpace(in.Country)) {
			country = c
			break
		}
	}
	if country == "" {
		return Input{}, invalid("country", "choose a supported country")
	}
	in.Country = country
	for field, value := range map[string]*string{"username": in.Username, "password": in.Password} {
		if value == nil {
			if create {
				return Input{}, invalid(field, "service credentials are required")
			}
			continue
		}
		if strings.TrimSpace(*value) == "" || len(*value) > 4096 || strings.ContainsAny(*value, "\x00\r\n") {
			return Input{}, invalid(field, "must be nonempty single-line service credentials")
		}
	}
	return in, nil
}

// Guard against accidentally broadening public JSON with a secret-bearing type.
var _ json.Marshaler = Input{}
