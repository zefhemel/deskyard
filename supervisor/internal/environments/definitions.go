package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// definitionsVersion is the format version of environments.json.
const definitionsVersion = 1

// Network kinds an environment's definition selects.
const (
	NetworkIsolated = "isolated" // its own bridge network
	NetworkVPN      = "vpn"      // all traffic through a saved VPN connection, fail closed
)

// Power is an environment's desired power state, separate from what its
// container is observed doing.
type Power string

const (
	PowerRunning Power = "running"
	PowerStopped Power = "stopped"
)

// ImageRef is the image a definition is built from: Ref is the logical
// reference checked for updates, ID the image that built the current revision.
type ImageRef struct {
	Ref string `json:"ref"`
	ID  string `json:"id"`
}

type Limits struct {
	MemoryBytes int64 `json:"memoryBytes"` // 0: no limit
	NanoCPUs    int64 `json:"nanoCPUs"`    // 0: no limit
}

// VolumeGrant gives an environment a named volume, mounted at Path (by
// default /share/<name>). The definition is the only record of who has which
// volume.
type VolumeGrant struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}

type Network struct {
	Kind          string          `json:"kind"`
	Connection    string          `json:"connection,omitempty"` // a saved VPN connection's ID, for kind vpn
	LANExceptions []vpn.Exception `json:"lanExceptions,omitempty"`
}

// Definition is an environment's desired configuration, kept in
// environments.json. It is the only authority: containers are built from it
// and their labels are never read back as configuration.
type Definition struct {
	Name     string        `json:"name"`
	Created  time.Time     `json:"created"`
	Revision uint64        `json:"revision"` // bumped by container-affecting changes only
	Image    ImageRef      `json:"image"`
	Limits   Limits        `json:"limits"`
	Network  Network       `json:"network"`
	Volumes  []VolumeGrant `json:"volumes"`
	// DockerAccess is as in Settings.
	DockerAccess  bool `json:"dockerAccess"`
	MemoryReclaim bool `json:"memoryReclaim"`
	// WebDAV serves the home and local volumes at /<name>/dav/.
	WebDAV bool  `json:"webdav"`
	Power  Power `json:"power"`
}

// Settings returns the definition's container settings, for the create code.
// Image is the logical reference.
func (d Definition) Settings() Settings {
	return cloneSettings(Settings{
		Image:           d.Image.Ref,
		MemoryBytes:     d.Limits.MemoryBytes,
		NanoCPUs:        d.Limits.NanoCPUs,
		VPNConnectionID: d.Network.Connection,
		LANExceptions:   d.Network.LANExceptions,
		Volumes:         d.Volumes,
		DockerAccess:    d.DockerAccess,
	})
}

// ContainerAffecting reports whether a and b differ in anything their
// container is built from: image, limits, network, volume grants,
// Docker access. Memory reclaim and power are applied without a new
// container.
func ContainerAffecting(a, b Definition) bool {
	return a.Image != b.Image || a.Limits != b.Limits || !sameNetwork(a.Network, b.Network) ||
		!slices.Equal(withGrantPaths(a.Volumes), withGrantPaths(b.Volumes)) ||
		a.DockerAccess != b.DockerAccess
}

func sameNetwork(a, b Network) bool {
	return a.Kind == b.Kind && a.Connection == b.Connection &&
		slices.EqualFunc(a.LANExceptions, b.LANExceptions, func(x, y vpn.Exception) bool {
			return x.Destination == y.Destination && x.Protocol == y.Protocol && slices.Equal(x.Ports, y.Ports)
		})
}

func cloneSettings(s Settings) Settings {
	s.Volumes = slices.Clone(s.Volumes)
	s.LANExceptions = slices.Clone(s.LANExceptions)
	for i := range s.LANExceptions {
		s.LANExceptions[i].Ports = slices.Clone(s.LANExceptions[i].Ports)
	}
	return s
}

func (d Definition) clone() Definition {
	s := cloneSettings(Settings{Volumes: d.Volumes, LANExceptions: d.Network.LANExceptions})
	d.Volumes, d.Network.LANExceptions = withGrantPaths(s.Volumes), s.LANExceptions
	return d
}

func (d Definition) validate() error {
	if err := ValidName(d.Name); err != nil {
		return err
	}
	switch {
	case d.Created.IsZero():
		return errors.New("no creation time")
	case d.Revision == 0:
		return errors.New("revision must be at least 1")
	case d.Image.Ref == "":
		return errors.New("no image")
	case d.Limits.MemoryBytes < 0 || d.Limits.NanoCPUs < 0:
		return errors.New("negative limits")
	case d.Power != PowerRunning && d.Power != PowerStopped:
		return fmt.Errorf("unknown power %q", d.Power)
	}
	switch d.Network.Kind {
	case NetworkIsolated:
		if d.Network.Connection != "" || len(d.Network.LANExceptions) > 0 {
			return errors.New("an isolated network has no VPN connection or LAN exceptions")
		}
	case NetworkVPN:
		if d.Network.Connection == "" {
			return errors.New("a VPN network needs a connection")
		}
		if _, err := vpn.NormalizeExceptions(d.Network.LANExceptions); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown network kind %q", d.Network.Kind)
	}
	return ValidateVolumeGrants(d.Volumes)
}

type definitionsFile struct {
	Version      int          `json:"version"`
	Environments []Definition `json:"environments"`
}

// loadedDefinition is a Definition as read from environments.json. Files
// written before camera support was removed carry a "camera" field, which is
// accepted and dropped: the next save leaves it out. Files from before host
// volumes carry "mounts", host folders, which load only when empty: they are
// not migrated.
type loadedDefinition struct {
	Definition
	Camera json.RawMessage `json:"camera"`
	Mounts json.RawMessage `json:"mounts"`
}

// hasHostFolders reports whether a loaded "mounts" field lists any host folder.
func (l loadedDefinition) hasHostFolders() bool {
	if len(l.Mounts) == 0 {
		return false
	}
	var mounts []json.RawMessage
	if err := json.Unmarshal(l.Mounts, &mounts); err != nil {
		return true
	}
	return len(mounts) > 0
}

// Definitions is the environments.json store. Every change is written
// atomically before it replaces the in-memory copy, so a failed write leaves
// both as they were.
type Definitions struct {
	path string

	mu   sync.Mutex
	defs map[string]Definition // never mutated; replaced on every change
	// checkGrants, when set, refuses grants of volumes that don't exist. Put
	// and Update call it with mu held, before saving, with only the grants
	// the change adds, so an unrelated edit of a definition whose volume went
	// missing still saves. Volume deletion holds mu (Hold) while it checks
	// for grants, so the two can't race.
	checkGrants func(grants []VolumeGrant) error
}

// LoadDefinitions reads path; a missing file is an empty store. Any invalid
// entry fails the whole load rather than dropping an environment.
func LoadDefinitions(path string) (*Definitions, error) {
	d := &Definitions{path: path, defs: map[string]Definition{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Version      int                `json:"version"`
		Environments []loadedDefinition `json:"environments"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != definitionsVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", path, f.Version, definitionsVersion)
	}
	for _, loaded := range f.Environments {
		def := loaded.Definition
		if IsReserved(def.Name) {
			// A name an upgrade newly reserved (such as "volumes"): stop with
			// the remedy rather than a bare validation error.
			return nil, fmt.Errorf("%s: environment %q: the name is now reserved by the supervisor; delete that environment with the previous version, or remove its entry from this file, before upgrading", path, def.Name)
		}
		if loaded.hasHostFolders() {
			return nil, fmt.Errorf("%s: environment %q has host folders, which this version replaced with host volumes; create a host volume for each folder and grant it instead, after removing the folders with the previous version or deleting the \"mounts\" entries from this file", path, def.Name)
		}
		if err := def.validate(); err != nil {
			return nil, fmt.Errorf("%s: environment %q: %w", path, def.Name, err)
		}
		if _, dup := d.defs[def.Name]; dup {
			return nil, fmt.Errorf("%s: environment %q is defined twice", path, def.Name)
		}
		d.defs[def.Name] = def.clone()
	}
	return d, nil
}

func (d *Definitions) Get(name string) (Definition, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	def, ok := d.defs[name]
	if !ok {
		return Definition{}, false
	}
	return def.clone(), true
}

// List returns every definition, sorted by name.
func (d *Definitions) List() []Definition {
	d.mu.Lock()
	defer d.mu.Unlock()
	return sortedDefinitions(d.defs)
}

// Put validates and saves def, adding or replacing the definition of that
// name. It does not bump the revision; callers decide that with
// ContainerAffecting.
func (d *Definitions) Put(def Definition) error {
	if err := def.validate(); err != nil {
		return fmt.Errorf("invalid environment definition: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkNewGrants(d.defs[def.Name].Volumes, def.Volumes); err != nil {
		return err
	}
	next := make(map[string]Definition, len(d.defs)+1)
	for name, v := range d.defs {
		next[name] = v
	}
	next[def.Name] = def.clone()
	return d.save(next)
}

// Update changes the definition of name with change and saves it, atomically
// with respect to other changes. It returns the saved definition; on an error
// from change, validation or the write, nothing changes.
func (d *Definitions) Update(name string, change func(*Definition) error) (Definition, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur, ok := d.defs[name]
	if !ok {
		return Definition{}, ErrNotFound
	}
	def := cur.clone()
	if err := change(&def); err != nil {
		return Definition{}, err
	}
	if def.Name != name {
		return Definition{}, errors.New("an environment's name can't change")
	}
	if err := def.validate(); err != nil {
		return Definition{}, fmt.Errorf("invalid environment definition: %w", err)
	}
	if err := d.checkNewGrants(cur.Volumes, def.Volumes); err != nil {
		return Definition{}, err
	}
	next := make(map[string]Definition, len(d.defs))
	for n, v := range d.defs {
		next[n] = v
	}
	next[name] = def.clone()
	if err := d.save(next); err != nil {
		return Definition{}, err
	}
	return def.clone(), nil
}

// checkNewGrants passes the grants in next that aren't in cur to
// checkGrants; d.mu is held.
func (d *Definitions) checkNewGrants(cur, next []VolumeGrant) error {
	if d.checkGrants == nil {
		return nil
	}
	var added []VolumeGrant
	for _, g := range next {
		if !slices.ContainsFunc(cur, func(c VolumeGrant) bool { return c.Name == g.Name }) {
			added = append(added, g)
		}
	}
	if len(added) == 0 {
		return nil
	}
	return d.checkGrants(added)
}

// Hold runs fn with every definition, sorted by name, while no definition
// can change.
func (d *Definitions) Hold(fn func(defs []Definition) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fn(sortedDefinitions(d.defs))
}

// Delete removes the definition of name, or returns ErrNotFound.
func (d *Definitions) Delete(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.defs[name]; !ok {
		return ErrNotFound
	}
	next := make(map[string]Definition, len(d.defs))
	for n, v := range d.defs {
		if n != name {
			next[n] = v
		}
	}
	return d.save(next)
}

// syncDefinitionsDir syncs the directory of environments.json; a variable so
// tests can observe it.
var syncDefinitionsDir = syncDirectory

// save writes next and adopts it; d.mu is held. The directory is synced too:
// engine work follows a save, and must not outlive it across a power loss.
// Once the file is renamed into place next is adopted, even when that sync
// fails, so memory matches the file.
func (d *Definitions) save(next map[string]Definition) error {
	if err := store.WriteJSON(d.path, definitionsFile{Version: definitionsVersion, Environments: sortedDefinitions(next)}); err != nil {
		return err
	}
	d.defs = next
	return syncDefinitionsDir(filepath.Dir(d.path))
}

func sortedDefinitions(defs map[string]Definition) []Definition {
	out := make([]Definition, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.clone())
	}
	slices.SortFunc(out, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	return out
}
