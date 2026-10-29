// Package storage owns storage remotes (curated rclone connections whose
// credentials live in secret/), restic backup repositories on them, backup
// policies and their schedule, and every rclone and restic call, which run in
// a short-lived helper container (see Runner).
package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrInUse    = errors.New("in use")
)

// RemoteType is one of the curated rclone backends.
type RemoteType string

const (
	S3     RemoteType = "s3"
	B2     RemoteType = "b2"
	SFTP   RemoteType = "sftp"
	WebDAV RemoteType = "webdav"
)

// S3Providers are the S3-compatible services offered, as rclone names them.
var S3Providers = []string{"AWS", "Cloudflare", "Minio", "Wasabi", "Other"}

// WebDAVVendors are the WebDAV servers offered, as rclone names them.
var WebDAVVendors = []string{"nextcloud", "owncloud", "other"}

// RemoteMount is where a helper container finds a remote's configuration
// folder, secret/remotes/<name>/. rclone.conf names files in it by this path.
const RemoteMount = "/secret/remote"

// Remote is a storage remote's public, secret-free configuration: an rclone
// connection. Only the fields of its type are set. Root is the path on the
// remote that users of it (backup repositories now, sync and mounts later)
// address folders below: for S3 and B2 a bucket and optional folder.
type Remote struct {
	Name    string     `json:"name"`
	Type    RemoteType `json:"type"`
	Created time.Time  `json:"created"`
	Root    string     `json:"root,omitempty"`
	// S3
	Provider    string `json:"provider,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Region      string `json:"region,omitempty"`
	AccessKeyID string `json:"accessKeyId,omitempty"`
	// B2
	KeyID string `json:"keyId,omitempty"`
	// SFTP; User also for WebDAV
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user,omitempty"`
	HostKey string `json:"hostKey,omitempty"` // a known_hosts line pinning the server's key
	// WebDAV
	URL    string `json:"url,omitempty"`
	Vendor string `json:"vendor,omitempty"`
}

// Secrets are a remote's write-only credentials. On an update a nil field
// keeps the stored value.
type Secrets struct {
	SecretAccessKey *string `json:"secretAccessKey,omitempty"` // S3
	ApplicationKey  *string `json:"applicationKey,omitempty"`  // B2
	Password        *string `json:"password,omitempty"`        // SFTP, WebDAV
	PrivateKey      *string `json:"privateKey,omitempty"`      // SFTP, PEM
}

func (Secrets) MarshalJSON() ([]byte, error) { return nil, errors.New("remote secrets are write-only") }

// StoredSecrets are a remote's saved credentials, kept in
// secret/remotes/<name>/secrets.json.
type StoredSecrets struct {
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	ApplicationKey  string `json:"applicationKey,omitempty"`
	Password        string `json:"password,omitempty"`
	PrivateKey      string `json:"privateKey,omitempty"`
}

// stored is base with the secrets s supplies.
func (s Secrets) stored(base StoredSecrets) StoredSecrets {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&base.SecretAccessKey, s.SecretAccessKey)
	set(&base.ApplicationKey, s.ApplicationKey)
	set(&base.Password, s.Password)
	set(&base.PrivateKey, s.PrivateKey)
	return base
}

// names lists which secrets are set, by their JSON names.
func (s StoredSecrets) names() []string {
	var out []string
	for name, v := range map[string]string{"secretAccessKey": s.SecretAccessKey, "applicationKey": s.ApplicationKey, "password": s.Password, "privateKey": s.PrivateKey} {
		if v != "" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Validate checks r with the secrets it would have: sec on top of nothing
// when creating, or on top of the stored ones (assumed complete) when not.
func (r Remote) Validate(sec Secrets, creating bool) error {
	if err := computers.ValidName(r.Name); err != nil {
		return fmt.Errorf("invalid remote name: %w", err)
	}
	for field, v := range map[string]string{"root": r.Root, "provider": r.Provider, "endpoint": r.Endpoint, "region": r.Region, "access key ID": r.AccessKeyID, "key ID": r.KeyID, "host": r.Host, "user": r.User, "host key": r.HostKey, "URL": r.URL, "vendor": r.Vendor} {
		if strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
			return fmt.Errorf("the %s can't contain line breaks", field)
		}
	}
	for field, v := range map[string]*string{"secret access key": sec.SecretAccessKey, "application key": sec.ApplicationKey, "password": sec.Password} {
		if v != nil && strings.ContainsAny(*v, "\r\n\x00") {
			return fmt.Errorf("the %s can't contain line breaks", field)
		}
	}
	if r.Root != "" && (strings.Contains(r.Root, "\\") || slices.Contains(strings.Split(r.Root, "/"), "..")) {
		return errors.New("the root path can't contain .. or backslashes")
	}
	has := func(p *string) bool { return !creating || p != nil && *p != "" }
	only := func(fields ...string) error {
		set := map[string]bool{"provider": r.Provider != "", "endpoint": r.Endpoint != "", "region": r.Region != "", "accessKeyId": r.AccessKeyID != "",
			"keyId": r.KeyID != "", "host": r.Host != "", "port": r.Port != 0, "user": r.User != "", "hostKey": r.HostKey != "", "url": r.URL != "", "vendor": r.Vendor != ""}
		for f, on := range set {
			if on && !slices.Contains(fields, f) {
				return fmt.Errorf("a %s remote has no %s", r.Type, f)
			}
		}
		return nil
	}
	bucket := func() error {
		if r.Root == "" || strings.HasPrefix(r.Root, "/") {
			return errors.New("the root path starts with the bucket, like my-bucket/backups")
		}
		return nil
	}
	switch r.Type {
	case S3:
		if err := only("provider", "endpoint", "region", "accessKeyId"); err != nil {
			return err
		}
		if !slices.Contains(S3Providers, r.Provider) {
			return fmt.Errorf("unknown S3 provider %q", r.Provider)
		}
		if r.Provider != "AWS" && r.Endpoint == "" {
			return errors.New("an endpoint is needed for providers other than AWS")
		}
		if r.Endpoint != "" {
			if u, err := url.Parse(r.Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return errors.New("the endpoint is an http or https URL")
			}
		}
		if r.AccessKeyID == "" || !has(sec.SecretAccessKey) {
			return errors.New("an access key ID and secret access key are needed")
		}
		return bucket()
	case B2:
		if err := only("keyId"); err != nil {
			return err
		}
		if r.KeyID == "" || !has(sec.ApplicationKey) {
			return errors.New("a key ID and application key are needed")
		}
		return bucket()
	case SFTP:
		if err := only("host", "port", "user", "hostKey"); err != nil {
			return err
		}
		if r.Host == "" || r.User == "" {
			return errors.New("a host and user are needed")
		}
		if r.Port < 0 || r.Port > 65535 {
			return errors.New("the port is 1 to 65535")
		}
		if creating && (sec.Password == nil || *sec.Password == "") && (sec.PrivateKey == nil || *sec.PrivateKey == "") {
			return errors.New("a password or a private key is needed")
		}
		return nil
	case WebDAV:
		if err := only("url", "vendor", "user"); err != nil {
			return err
		}
		if u, err := url.Parse(r.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("the URL is an http or https URL")
		}
		if r.Vendor != "" && !slices.Contains(WebDAVVendors, r.Vendor) {
			return fmt.Errorf("unknown WebDAV vendor %q", r.Vendor)
		}
		return nil
	default:
		return fmt.Errorf("unknown remote type %q", r.Type)
	}
}

// RcloneConfig is r's rclone configuration: one section named after it.
// Passwords rclone expects obscured are obscured; files it refers to are in
// RemoteMount.
func (r Remote) RcloneConfig(sec StoredSecrets) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", r.Name)
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	obscured := func(k, v string) error {
		if v == "" {
			return nil
		}
		ob, err := Obscure(v)
		if err != nil {
			return err
		}
		kv(k, ob)
		return nil
	}
	switch r.Type {
	case S3:
		kv("type", "s3")
		kv("provider", r.Provider)
		kv("endpoint", r.Endpoint)
		kv("region", r.Region)
		kv("access_key_id", r.AccessKeyID)
		kv("secret_access_key", sec.SecretAccessKey)
		if r.Provider == "Minio" || r.Provider == "Other" {
			kv("force_path_style", "true")
		}
	case B2:
		kv("type", "b2")
		kv("account", r.KeyID)
		kv("key", sec.ApplicationKey)
	case SFTP:
		kv("type", "sftp")
		kv("host", r.Host)
		port := r.Port
		if port == 0 {
			port = 22
		}
		kv("port", strconv.Itoa(port))
		kv("user", r.User)
		if err := obscured("pass", sec.Password); err != nil {
			return "", err
		}
		// rclone reads a one-line PEM with literal \n for line breaks.
		kv("key_pem", strings.ReplaceAll(strings.TrimRight(sec.PrivateKey, "\n"), "\n", `\n`))
		if r.HostKey != "" {
			kv("known_hosts_file", RemoteMount+"/known_hosts")
		}
		// Plain SFTP only: no shell commands on the server.
		kv("shell_type", "none")
	case WebDAV:
		kv("type", "webdav")
		kv("url", r.URL)
		kv("vendor", r.Vendor)
		kv("user", r.User)
		if err := obscured("pass", sec.Password); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown remote type %q", r.Type)
	}
	return b.String(), nil
}

// WriteConfigDir writes r's rclone.conf, and its known_hosts when it pins a
// host key, into dir (made owner-only).
func WriteConfigDir(dir string, r Remote, sec StoredSecrets) error {
	conf, err := r.RcloneConfig(sec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if err := store.WriteFile(filepath.Join(dir, "rclone.conf"), []byte(conf)); err != nil {
		return err
	}
	known := filepath.Join(dir, "known_hosts")
	if r.HostKey == "" {
		if err := os.Remove(known); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return store.WriteFile(known, []byte(r.HostKey+"\n"))
}

const remotesVersion = 1

type remotesFile struct {
	Version int      `json:"version"`
	Remotes []Remote `json:"remotes"`
}

// Remotes is storage-remotes.json (named so as not to be confused with the
// deskyard CLI's remotes.json of servers) and each remote's
// secret/remotes/<name>/ folder: secrets.json and the generated rclone.conf.
type Remotes struct {
	path, secretDir string

	mu      sync.Mutex
	remotes map[string]Remote
}

// LoadRemotes reads path; a missing file is an empty store. Any invalid
// entry fails the whole load.
func LoadRemotes(path, secretDir string) (*Remotes, error) {
	secretDir, err := filepath.Abs(filepath.Join(secretDir, "remotes"))
	if err != nil {
		return nil, err
	}
	rs := &Remotes{path: path, secretDir: secretDir, remotes: map[string]Remote{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rs, nil
	}
	if err != nil {
		return nil, err
	}
	var f remotesFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != remotesVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", path, f.Version, remotesVersion)
	}
	for _, r := range f.Remotes {
		if err := r.Validate(Secrets{}, false); err != nil {
			return nil, fmt.Errorf("%s: remote %q: %w", path, r.Name, err)
		}
		if _, dup := rs.remotes[r.Name]; dup {
			return nil, fmt.Errorf("%s: remote %q is defined twice", path, r.Name)
		}
		rs.remotes[r.Name] = r
	}
	return rs, nil
}

// List returns every remote, sorted by name.
func (rs *Remotes) List() []Remote {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]Remote, 0, len(rs.remotes))
	for _, r := range rs.remotes {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Remote) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (rs *Remotes) Get(name string) (Remote, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.remotes[name]
	return r, ok
}

// ConfigDir is the absolute host path of the remote's secret folder, which
// helpers mount at RemoteMount.
func (rs *Remotes) ConfigDir(name string) string { return filepath.Join(rs.secretDir, name) }

// SecretsSet lists which of name's secrets are stored, by their JSON names.
func (rs *Remotes) SecretsSet(name string) []string {
	sec, _ := rs.secrets(name)
	return sec.names()
}

func (rs *Remotes) secrets(name string) (StoredSecrets, error) {
	var sec StoredSecrets
	err := store.ReadJSON(filepath.Join(rs.ConfigDir(name), "secrets.json"), &sec)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return sec, err
}

// Add validates and saves a new remote with its secrets.
func (rs *Remotes) Add(r Remote, sec Secrets) error {
	if err := r.Validate(sec, true); err != nil {
		return err
	}
	if r.Created.IsZero() {
		r.Created = time.Now().UTC()
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.remotes[r.Name]; ok {
		return fmt.Errorf("remote %q: %w", r.Name, ErrExists)
	}
	if err := rs.writeSecrets(r, sec.stored(StoredSecrets{})); err != nil {
		_ = os.RemoveAll(rs.ConfigDir(r.Name))
		return err
	}
	next := maps(rs.remotes)
	next[r.Name] = r
	if err := rs.save(next); err != nil {
		_ = os.RemoveAll(rs.ConfigDir(r.Name))
		return err
	}
	return nil
}

// Update replaces name's settings with r, keeping stored secrets that sec
// leaves out. The name, type and creation time can't change.
func (rs *Remotes) Update(name string, r Remote, sec Secrets) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	cur, ok := rs.remotes[name]
	if !ok {
		return fmt.Errorf("remote %q: %w", name, ErrNotFound)
	}
	if r.Name != name || r.Type != cur.Type {
		return errors.New("a remote's name and type can't change")
	}
	r.Created = cur.Created
	if err := r.Validate(sec, false); err != nil {
		return err
	}
	stored, err := rs.secrets(name)
	if err != nil {
		return err
	}
	merged := sec.stored(stored)
	if r.Type == SFTP && merged.Password == "" && merged.PrivateKey == "" {
		return errors.New("a password or a private key is needed")
	}
	if err := rs.writeSecrets(r, merged); err != nil {
		return err
	}
	next := maps(rs.remotes)
	next[name] = r
	return rs.save(next)
}

// Delete forgets name and its local secrets. Nothing on the remote changes.
func (rs *Remotes) Delete(name string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.remotes[name]; !ok {
		return fmt.Errorf("remote %q: %w", name, ErrNotFound)
	}
	next := maps(rs.remotes)
	delete(next, name)
	if err := rs.save(next); err != nil {
		return err
	}
	return os.RemoveAll(rs.ConfigDir(name))
}

// StoredSecrets returns name's saved secrets, for testing a changed remote.
func (rs *Remotes) StoredSecrets(name string) (StoredSecrets, error) { return rs.secrets(name) }

func (rs *Remotes) writeSecrets(r Remote, sec StoredSecrets) error {
	if err := os.MkdirAll(rs.secretDir, 0o700); err != nil {
		return err
	}
	dir := rs.ConfigDir(r.Name)
	if err := WriteConfigDir(dir, r, sec); err != nil {
		return err
	}
	return store.WriteJSON(filepath.Join(dir, "secrets.json"), sec)
}

func (rs *Remotes) save(next map[string]Remote) error {
	out := make([]Remote, 0, len(next))
	for _, r := range next {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Remote) int { return strings.Compare(a.Name, b.Name) })
	if err := store.WriteJSON(rs.path, remotesFile{Version: remotesVersion, Remotes: out}); err != nil {
		return err
	}
	rs.remotes = next
	return nil
}

func maps[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// joinPath joins a remote's root and a folder below it, as rclone paths.
func joinPath(root, folder string) string {
	switch {
	case root == "":
		return folder
	case folder == "":
		return root
	}
	return path.Join(root, folder)
}
