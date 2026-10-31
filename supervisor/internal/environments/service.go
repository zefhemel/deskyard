// Package environments creates and runs environments. Each is defined in
// environments.json (Definitions), the only authority for its configuration,
// and realised as a disposable container labelled as this supervisor's, with
// its own network and home folder. Private recovery intent protects container
// replacements.
package environments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/desktopcli"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// Labels on this supervisor's containers and networks. They identify and
// match; they are never read back as configuration.
const (
	LabelManaged  = "deskyard.managed"
	LabelName     = "deskyard.name"
	LabelRevision = "deskyard.revision" // the definition revision a container was built from
	LabelInstance = "deskyard.instance" // the supervisor instance that owns it
	LabelContract = "deskyard.contract" // on images
	TokenEnv      = "WORKSPACE_PROXY_TOKEN"
	Contract      = "3" // 3: a Selkies desktop (Xvfb, Selkies behind Caddy)

	stopTimeout = 30 * time.Second
	shmBytes    = 1 << 30
)

var (
	ErrNotFound = errors.New("no such environment")
	ErrExists   = errors.New("an environment with that name already exists")
	ErrBusy     = errors.New("the environment is busy with another operation")
	ErrStopped  = errors.New("the environment is stopped")
	ErrConflict = errors.New("a container or network that is not this environment's has its name")

	errVPNUnavailable = errors.New("VPN networking unavailable")
)

type Settings struct {
	Services        []vpn.ServiceRule
	Image           string
	MemoryBytes     int64 // 0: no limit
	NanoCPUs        int64 // 0: no limit
	VPNConnectionID string
	LANExceptions   []vpn.Exception
	Volumes         []VolumeGrant // each mounted at GrantPath
	DockerAccess    bool
}

// Environment is an environment's definition joined with what its container,
// when it has one, is doing.
type Environment struct {
	// ID is the name, the environment's identity. Grants, tokens and events
	// still key on it under this field.
	ID, Name    string
	State       string // running, stopped, plus transient creating/upgrading/removing/starting
	Image       string // the definition's image reference
	ImageID     string // the container's image, or the pinned one without a container
	ImageLabels map[string]string
	StartedAt   time.Time
	Created     time.Time
	Folder      string
	Settings    Settings
	// MemoryReclaim lets the supervisor reclaim memory while it is idle.
	MemoryReclaim bool
	// WebDAV serves the environment's files at /<name>/dav/.
	WebDAV bool
	// Revision is the definition's; AppliedRevision the one its container was
	// built from (0 without a container). Revision ahead of AppliedRevision is
	// a change that is applied on the next start.
	Revision, AppliedRevision uint64
	Power                     Power  // desired; State is observed
	Error                     string // why the last change could not be applied
	Conflict                  string // why a container with its name isn't matched to it
	VPN                       VPNStatus
	Container                 engine.Container // zero when it has no container
}

func (c Environment) Running() bool { return c.State == "running" }

type Config struct {
	// Layout is the supervisor's data folder, with where the engine host
	// has it (config.Layout.WithHost).
	Layout config.Layout
	Subnet string // DESKYARD_SUBNET
	// SelfID is the supervisor's own container, which joins each desktop's
	// network to reach it. Empty when the caller isn't in a container on
	// the engine, as for Go tests on the engine's host, which reach
	// desktops by address.
	SelfID string
	// DesktopPort is the port the proxy dials on a desktop: DesktopPort, or
	// a test's fake desktop.
	DesktopPort int
}

// DesktopPort is the port desktops serve on.
const DesktopPort = 8080

type Service struct {
	Networking *Networking
	Eng        engine.Engine
	Cfg        Config
	Layout     config.Layout // Cfg.Layout
	Defs       *Definitions  // environments.json
	Vols       *Volumes      // volumes.json
	// Instance is this supervisor's ID (config.InstanceID). Only containers
	// and networks labelled with it are considered.
	Instance string
	Log      *slog.Logger
	// ImageUsed, when set, hears which image ID a reference resolved to
	// whenever an environment is created or recreated from it.
	ImageUsed func(ref, id string)
	// ConflictsChanged, when set, hears that a reconcile pass found other
	// conflicts than the one before.
	ConflictsChanged func()
	// Repinned, when set, hears the pinned images a reconcile pass found
	// gone and pulled again, each recorded as a new revision.
	Repinned func([]Repin)
	// NameClaimed, when set, hears under ClaimName's reservation that name
	// is claimed for a new environment, to forget what a removal of an
	// earlier one of the name failed to purge (its grants).
	NameClaimed func(name string) error
	// Removed, when set, hears that a removal deleted name's definition,
	// whether from Remove or from the poll or recovery finishing a VPN
	// removal, and even when deleting its files then failed. Its error is
	// the removal's.
	Removed func(name string) error

	mu         sync.Mutex
	subnetMu   sync.Mutex // choosing and creating a network's subnet
	reserved   map[Item]reservation
	errs       map[string]string // name → why its last change failed
	folderErrs map[string]string // name → its missing home or volume folder, shown over errs
	failed     map[string]bool   // names without a container whose build failed
	conflicts  []Conflict        // found by the latest reconcile pass
	repins     []Repin           // since the latest reconcile pass
	imgInfo    map[string]engine.Image
	// volMu serialises creating and deleting volumes, folders included. Lock
	// order: volMu, then Defs.mu, then Vols.mu.
	volMu sync.Mutex

	helpersOnce sync.Once
	helpers     *helper.Runner
}

// New makes a service with no definitions or volumes yet (saved to
// environments.json and volumes.json in the data folder once there are) and
// no instance ID. Open loads them.
func New(e engine.Engine, cfg Config, log *slog.Logger) *Service {
	layout := cfg.Layout
	s := &Service{Eng: e, Cfg: cfg, Layout: layout, Log: log,
		Defs:     &Definitions{path: definitionsPath(layout), defs: map[string]Definition{}},
		Vols:     &Volumes{path: volumesPath(layout), layout: layout, vols: map[string]Volume{}},
		reserved: map[Item]reservation{}, errs: map[string]string{}, folderErrs: map[string]string{},
		failed: map[string]bool{}, imgInfo: map[string]engine.Image{}}
	s.Defs.checkGrants = s.checkGrants
	return s
}

// Open makes the service of supervisor instance, with the definitions and
// volumes saved in the data folder.
func Open(e engine.Engine, cfg Config, instance string, log *slog.Logger) (*Service, error) {
	s := New(e, cfg, log)
	defs, err := LoadDefinitions(definitionsPath(s.Layout))
	if err != nil {
		return nil, err
	}
	vols, err := LoadVolumes(volumesPath(s.Layout), s.Layout)
	if err != nil {
		return nil, err
	}
	defs.checkGrants = s.checkGrants
	s.Defs, s.Vols, s.Instance = defs, vols, instance
	return s, nil
}

func definitionsPath(l config.Layout) string { return filepath.Join(l.Data, "environments.json") }

func containerName(name string) string { return "deskyard-" + name }

// owns reports whether labels mark this supervisor's container or network of
// environment name.
func (s *Service) owns(labels map[string]string, name string) bool {
	return IsEnvironment(labels) && labels[LabelName] == name && labels[LabelInstance] == s.Instance
}

func (s *Service) ownLabels(name string) map[string]string {
	return map[string]string{LabelManaged: "true", LabelName: name, LabelInstance: s.Instance}
}

// ownFilter selects this supervisor's containers and networks from the engine.
func (s *Service) ownFilter() map[string]string {
	f := map[string]string{LabelManaged: "true"}
	if s.Instance != "" {
		f[LabelInstance] = s.Instance
	}
	return f
}

// setError records why a change to name failed; "" clears it, and that its
// build failed.
func (s *Service) setError(name, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg == "" {
		delete(s.errs, name)
		delete(s.failed, name)
	} else {
		s.errs[name] = msg
	}
}

// buildFailed records why name's container could not be built: reconcile
// leaves it for a start or edit, which clears it.
func (s *Service) buildFailed(name string, err error) {
	s.setError(name, err.Error())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[name] = true
}

func (s *Service) hasFailedBuild(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed[name]
}

// clearError clears name's error when it is msg.
func (s *Service) clearError(name, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errs[name] == msg {
		delete(s.errs, name)
	}
}

// errorOf is name's error: a missing home or volume folder over why its last
// change failed, which is kept for when the folder is back.
func (s *Service) errorOf(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg := s.folderErrs[name]; msg != "" {
		return msg
	}
	return s.errs[name]
}

// fromDefinition joins def with its container c (zero when it has none).
func (s *Service) fromDefinition(ctx context.Context, def Definition, c engine.Container) Environment {
	env := Environment{
		ID: def.Name, Name: def.Name, State: "stopped", Image: def.Image.Ref, ImageID: def.Image.ID,
		Created: def.Created, Folder: s.Layout.HomeOf(def.Name), Settings: def.Settings(),
		MemoryReclaim: def.MemoryReclaim, WebDAV: def.WebDAV, Revision: def.Revision, Power: def.Power,
		Error: s.errorOf(def.Name), Conflict: s.duplicate(def.Name), Container: c,
	}
	if c.ID != "" {
		env.ImageID, env.StartedAt = c.ImageID, c.StartedAt
		env.AppliedRevision, _ = strconv.ParseUint(c.Labels[LabelRevision], 10, 64)
		if c.Running() {
			env.State = "running"
		}
	}
	if env.ImageID != "" {
		env.ImageLabels = s.image(ctx, env.ImageID).Labels
	}
	if b := s.Busy()[def.Name]; b != "" {
		env.State = b
	}
	if s.Networking != nil {
		env = s.Networking.decorate(env)
	} else {
		env.VPN.State = "disabled"
	}
	return env
}

// creating is the placeholder of an environment whose definition isn't saved yet.
func (s *Service) creating(name string) Environment {
	return Environment{ID: name, Name: name, State: "creating", Folder: s.Layout.HomeOf(name)}
}

// List returns every defined environment, with or without a container, and
// those being created.
func (s *Service) List(ctx context.Context) ([]Environment, error) {
	cs, err := s.Eng.List(ctx, s.ownFilter())
	if err != nil {
		return nil, err
	}
	containers := map[string]engine.Container{}
	conflicts := map[string]string{}
	for _, c := range cs {
		name := c.Labels[LabelName]
		if !s.owns(c.Labels, name) || c.Name != containerName(name) {
			continue
		}
		if _, dup := containers[name]; dup {
			conflicts[name] = "several containers claim this environment"
			continue
		}
		containers[name] = c
	}
	defs := s.Defs.List()
	out := make([]Environment, 0, len(defs))
	seen := map[string]bool{}
	for _, def := range defs {
		c, ok := containers[def.Name]
		conflict := conflicts[def.Name]
		if !ok {
			// As Get: a container of its name that isn't this supervisor's.
			if _, conflict, err = s.container(ctx, def.Name); err != nil {
				return nil, err
			}
		}
		env := s.fromDefinition(ctx, def, c)
		if conflict != "" {
			env.Conflict = conflict
		}
		seen[def.Name] = true
		out = append(out, env)
	}
	// Environments being created may not be defined yet.
	for name, state := range s.Busy() {
		if !seen[name] && state == "creating" {
			out = append(out, s.creating(name))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns a defined environment (or one being created).
func (s *Service) Get(ctx context.Context, name string) (Environment, error) {
	if ValidName(name) != nil {
		return Environment{}, ErrNotFound
	}
	def, ok := s.Defs.Get(name)
	if !ok {
		if st, ok := s.Busy()[name]; ok && st == "creating" {
			return s.creating(name), nil
		}
		return Environment{}, ErrNotFound
	}
	c, conflict, err := s.container(ctx, name)
	if err != nil {
		return Environment{}, err
	}
	env := s.fromDefinition(ctx, def, c)
	if conflict != "" {
		env.Conflict = conflict
	}
	return env, nil
}

// container is name's container, or a zero one when it has none. A container
// with its name that isn't this supervisor's is reported as a conflict.
func (s *Service) container(ctx context.Context, name string) (c engine.Container, conflict string, err error) {
	c, err = s.Eng.Inspect(ctx, containerName(name))
	if errors.Is(err, engine.ErrNotFound) {
		return engine.Container{}, "", nil
	}
	if err != nil {
		return engine.Container{}, "", err
	}
	if !s.owns(c.Labels, name) {
		return engine.Container{}, fmt.Sprintf("the container %s isn't this environment's", containerName(name)), nil
	}
	return c, "", nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// forget deletes name's definition and proxy token.
func (s *Service) forget(name string) error {
	if err := s.Defs.Delete(name); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.setError(name, "")
	s.mu.Lock()
	delete(s.folderErrs, name)
	s.mu.Unlock()
	if err := os.Remove(s.tokenPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// withSettings is def changed to set, with image the ID set.Image resolved to.
// The revision is bumped when the container is affected.
func withSettings(def Definition, set Settings, image string) Definition {
	next := def
	next.Image = ImageRef{Ref: set.Image, ID: image}
	next.Limits = Limits{MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs}
	next.Network = Network{Kind: NetworkIsolated}
	if set.VPNConnectionID != "" {
		next.Network = Network{Kind: NetworkVPN, Connection: set.VPNConnectionID, LANExceptions: set.LANExceptions, Services: set.Services}
	}
	next.Volumes = set.Volumes
	next.DockerAccess = set.DockerAccess
	next = next.clone()
	if ContainerAffecting(def, next) {
		next.Revision++
	}
	return next
}

// Create defines a new environment and starts it. progress reports steps
// (message, fraction or -1). The definition is saved before the engine is
// touched; on failure nothing is left behind but a folder that was already
// there.
func (s *Service) Create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Environment, error) {
	return s.create(ctx, name, set, PowerRunning, progress)
}

// CreateStopped is Create of an environment that stays stopped: only its
// definition is saved, and its container is built when it is started. It
// has no VPN connection, whose coordinator always starts what it creates.
func (s *Service) CreateStopped(ctx context.Context, name string, set Settings, progress func(string, float64)) (Environment, error) {
	if set.VPNConnectionID != "" {
		return Environment{}, errors.New("an environment with a VPN connection is created running")
	}
	return s.create(ctx, name, set, PowerStopped, progress)
}

// create is Create, or CreateStopped with power stopped.
func (s *Service) create(ctx context.Context, name string, set Settings, power Power, progress func(string, float64)) (Environment, error) {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return Environment{}, err
		}
		defer unlock()
	}
	viaVPN := set.VPNConnectionID != ""
	if viaVPN && s.Networking == nil {
		return Environment{}, errVPNUnavailable
	}
	if err := ValidName(name); err != nil {
		return Environment{}, err
	}
	if _, ok := s.Defs.Get(name); ok {
		return Environment{}, ErrExists
	}
	// A removal still being finished would take the new environment with it.
	if s.Networking != nil {
		if _, ok := s.Networking.record(name); ok {
			return Environment{}, ErrExists
		}
	}
	if _, err := s.Eng.Inspect(ctx, containerName(name)); err == nil {
		return Environment{}, ErrExists
	}
	// A stray container of this instance claiming the name would be a
	// duplicate of the new one, blocking it.
	claims, err := s.claims(ctx)
	if err != nil {
		return Environment{}, err
	}
	if cs := claims[name]; len(cs) > 0 {
		names := make([]string, 0, len(cs))
		for _, c := range cs {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		return Environment{}, fmt.Errorf("%w: %s claims environment %s; remove it first", ErrConflict, strings.Join(names, ", "), name)
	}
	if err := ValidateVolumeGrants(set.Volumes); err != nil {
		return Environment{}, err
	}
	// The home is made below.
	if err := s.preflight(ctx, name, set.Volumes, set.DockerAccess, grantFolders, progress); err != nil {
		return Environment{}, err
	}
	img, err := s.EnsureImage(ctx, set.Image, progress)
	if err != nil {
		return Environment{}, err
	}
	// The home is there before the definition is saved: only creation
	// makes one, so a definition saved without it could never start.
	folder := s.Layout.HomeOf(name)
	createdFolder := false
	if !s.FolderExists(name) {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return Environment{}, err
		}
		createdFolder = true
	}
	removeFolder := func() {
		if createdFolder {
			_ = os.Remove(folder) // only if still empty
		}
	}
	def := withSettings(Definition{Name: name, Created: time.Now().UTC(), MemoryReclaim: true, Power: power}, set, img.ID)
	def.Revision = 1
	if err := s.Defs.Put(def); err != nil {
		_ = s.forget(name)
		removeFolder()
		return Environment{}, err
	}
	if power == PowerStopped {
		return s.Get(ctx, name)
	}
	if viaVPN {
		env, err := s.Networking.create(ctx, name, progress)
		// A transition the networking journal has taken on is recovered
		// from it; anything earlier leaves nothing. No transition of the
		// name existed before this call.
		if _, journaled := s.Networking.record(name); err != nil && !journaled {
			_ = s.forget(name)
			removeFolder()
		}
		return env, err
	}
	// Only what this call made is removed: a container or network of the
	// name may belong to another supervisor.
	madeNetwork, madeContainer := false, false
	cleanup := func() {
		bg := context.WithoutCancel(ctx)
		if madeContainer {
			_ = s.Eng.Remove(bg, containerName(name))
		}
		if madeNetwork {
			_ = s.removeNetwork(bg, name)
		}
		_ = s.forget(name)
		removeFolder()
	}
	progress("Creating the environment", -1)
	if err := s.createNetwork(ctx, name); err != nil {
		cleanup()
		return Environment{}, err
	}
	madeNetwork = true
	if err := s.realise(ctx, def, ""); err != nil {
		cleanup()
		return Environment{}, err
	}
	madeContainer = true
	progress("Starting", -1)
	if err := s.Eng.Start(ctx, containerName(name)); err != nil {
		cleanup()
		return Environment{}, err
	}
	progress("Starting the environment", -1)
	if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
		cleanup()
		return Environment{}, err
	}
	return s.Get(ctx, name)
}

// realise creates the container of def, not started.
func (s *Service) realise(ctx context.Context, def Definition, runtimeID string) error {
	return s.createContainer(ctx, def.Name, def.Revision, def.Settings(), pinned(def), runtimeID)
}

// createContainer creates name's container with set, labelled as built from
// revision, from imageID; with runtimeID, in that VPN runtime's namespace.
func (s *Service) createContainer(ctx context.Context, name string, revision uint64, set Settings, imageID, runtimeID string) error {
	if set.VPNConnectionID != "" && runtimeID == "" {
		return errors.New("VPN selection requires a prepared network namespace")
	}
	if err := ValidateVolumeGrants(set.Volumes); err != nil {
		return err
	}
	mounts, missing := s.Mounts(name, set.Volumes)
	if len(missing) > 0 {
		return fmt.Errorf("volume %s doesn't exist", missing[0])
	}
	token, err := s.proxyToken(name)
	if err != nil {
		return err
	}
	labels := s.ownLabels(name)
	labels[LabelRevision] = strconv.FormatUint(revision, 10)
	spec := engine.ContainerSpec{
		Name: containerName(name), Image: imageID, Labels: labels,
		Env:     map[string]string{TokenEnv: token},
		Mounts:  engineMounts(mounts),
		Network: containerName(name), Aliases: []string{containerName(name)},
		MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs, ShmBytes: shmBytes,
		Flatpak: true, Restart: "unless-stopped", NoNewPrivileges: len(set.Volumes) > 0,
	}
	img, err := s.Eng.ImageInspect(ctx, imageID)
	if err != nil {
		return fmt.Errorf("inspect desktop capabilities: %w", err)
	}
	if capability := img.Labels[desktopcli.Label]; capability != "" {
		if capability != desktopcli.Capability {
			return fmt.Errorf("unsupported desktop CLI capability %q", capability)
		}
		dir, err := desktopcli.Directory(s.Layout.State, img.Arch)
		if err != nil {
			return err
		}
		binary := filepath.Join(dir, "deskyard")
		st, err := os.Stat(binary)
		if err != nil {
			return fmt.Errorf("desktop CLI unavailable (restart the supervisor to republish it): %w", err)
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("desktop CLI %s is not an executable file", binary)
		}
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: dir, Target: desktopcli.Target, ReadOnly: true, MustExist: true})
	}
	if img.Labels[labelFUSE] == fuseUserNS {
		spec.Devices = append(spec.Devices, "/dev/fuse")
	}
	if set.DockerAccess {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: dockerSocket, OnEngineHost: true, Target: dockerSocket, MustExist: true})
		spec.Env["DESKYARD_DOCKER_ACCESS"] = "1"
	}
	if runtimeID != "" {
		if mounts, ok := s.Networking.runtime.(interface {
			DesktopMounts(string, string) ([]engine.Mount, error)
		}); ok {
			extra, e := mounts.DesktopMounts(name, set.VPNConnectionID)
			if e != nil {
				return e
			}
			spec.Mounts = append(spec.Mounts, extra...)
		}
		spec.NetworkContainer = runtimeID
		spec.Network = ""
		spec.Aliases = nil
		spec.Restart = "no"
		spec.Labels[vpn.LabelConnection] = set.VPNConnectionID
		if r, ok := s.Networking.record(name); ok {
			spec.Labels[vpn.LabelRevision] = strconv.FormatUint(r.ConnectionRevision, 10)
		}
	}
	if _, err := s.Eng.Create(ctx, spec); err != nil {
		return err
	}
	if s.Cfg.SelfID != "" {
		// After a recreate the supervisor is still on the network.
		if self, err := s.Eng.Inspect(ctx, s.Cfg.SelfID); err == nil && self.IP[containerName(name)] != "" {
			return nil
		}
		if err := s.Eng.ConnectNetwork(ctx, containerName(name), s.Cfg.SelfID, nil); err != nil {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer done()
			_ = s.Eng.Remove(cleanup, containerName(name))
			return fmt.Errorf("connecting the supervisor to %s: %w", containerName(name), err)
		}
	}
	return nil
}

// SetMemoryReclaim saves whether the supervisor may reclaim name's memory
// while it is idle. Its container is not affected, so the revision stays.
func (s *Service) SetMemoryReclaim(name string, on bool) error {
	_, err := s.Defs.Update(name, func(d *Definition) error {
		d.MemoryReclaim = on
		return nil
	})
	return err
}

// SetWebDAV saves whether name's files are served over WebDAV. Its
// container is not affected, so the revision stays.
func (s *Service) SetWebDAV(name string, on bool) error {
	_, err := s.Defs.Update(name, func(d *Definition) error {
		d.WebDAV = on
		return nil
	})
	return err
}

func (s *Service) setPower(name string, p Power) error {
	_, err := s.Defs.Update(name, func(d *Definition) error {
		d.Power = p
		return nil
	})
	return err
}

// Start starts an environment, saving that it should run. Without a
// container, or with one built from an older revision, its container is
// created from the definition first.
// The caller reserves the environment with Begin, as for Recreate.
func (s *Service) Start(ctx context.Context, name string) error {
	return s.power(ctx, name, "start")
}

// Restart is Start, restarting a running container.
func (s *Service) Restart(ctx context.Context, name string) error {
	return s.power(ctx, name, "restart")
}

// power is Start or Restart by action; "resume" (the reconcile pass) only
// starts an existing container, leaving a definition ahead of it for a start,
// restart or edit to apply. A missing home or volume folder refuses all of
// them.
func (s *Service) power(ctx context.Context, name, action string) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if err := s.blocked(name); err != nil {
		return err
	}
	// A start that is refused leaves the saved power as it was.
	viaVPN := s.Networking != nil && s.Networking.selected(name)
	var c Environment
	if viaVPN {
		if err := s.Networking.startable(name); err != nil {
			return err
		}
	} else {
		var err error
		if c, err = s.Get(ctx, name); err != nil {
			return err
		}
		// A VPN environment never starts without its coordinator.
		if c.Settings.VPNConnectionID != "" {
			return errVPNUnavailable
		}
		if c.Conflict != "" {
			return fmt.Errorf("%w: %s", ErrConflict, c.Conflict)
		}
	}
	def, ok := s.Defs.Get(name)
	if !ok {
		return ErrNotFound
	}
	if err := s.preflight(ctx, name, def.Volumes, def.DockerAccess, savedFolders, nil); err != nil {
		return err
	}
	if err := s.setPower(name, PowerRunning); err != nil {
		return err
	}
	if viaVPN {
		return s.Networking.power(ctx, name, action)
	}
	if c.Container.ID == "" {
		if err := s.build(ctx, name); err != nil {
			return err
		}
		return s.Eng.Start(ctx, containerName(name))
	}
	if c.AppliedRevision != c.Revision && action != "resume" {
		if err := s.applyDefinition(ctx, c, func(string, float64) {}); err != nil {
			return err
		}
		if d, err := s.Get(ctx, name); err == nil && d.Container.Running() {
			return nil
		}
		return s.Eng.Start(ctx, containerName(name))
	}
	var err error
	if action == "restart" {
		err = s.Eng.Restart(ctx, containerName(name), stopTimeout)
	} else {
		err = s.Eng.Start(ctx, containerName(name))
	}
	if err != nil {
		return err
	}
	// A container that didn't become ready after a network change is
	// what this start retries; an error of a failed change stays.
	s.clearError(name, errNotReady)
	return nil
}

// build creates the container of an environment that has none, from its
// definition. A pinned image gone from the engine is first replaced, as a
// new revision. A missing home or volume folder is an error: only creation
// makes a home, and only CreateVolume a volume folder.
func (s *Service) build(ctx context.Context, name string) error {
	if _, ok := s.Defs.Get(name); !ok {
		return ErrNotFound
	}
	if err := s.checkFolders(name); err != nil {
		return err
	}
	def, err := s.repin(ctx, name)
	if err != nil {
		s.buildFailed(name, err)
		return err
	}
	if err = s.ensureNetwork(ctx, name); err != nil {
		s.buildFailed(name, err)
		return err
	}
	if err := s.realise(ctx, def, ""); err != nil {
		s.buildFailed(name, err)
		return err
	}
	s.setError(name, "")
	return nil
}

// Stop stops an environment and saves that it should stay stopped. Its
// container is kept. Like Start, it requires the caller's reservation.
func (s *Service) Stop(ctx context.Context, name string) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if err := s.blocked(name); err != nil {
		return err
	}
	if err := s.setPower(name, PowerStopped); err != nil {
		return err
	}
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.power(ctx, name, "stop")
	}
	c, _, err := s.container(ctx, name)
	if err != nil || c.ID == "" {
		return err
	}
	return s.Eng.Stop(ctx, c.ID, stopTimeout)
}

// Remove deletes the definition, the container and its network; the
// workspace folder is deleted only with deleteFiles.
func (s *Service) Remove(ctx context.Context, name string, deleteFiles bool) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if _, ok := s.Defs.Get(name); !ok {
		return ErrNotFound
	}
	if err := s.blocked(name); err != nil {
		return err
	}
	// Its files are held for the removal: no backup or restore starts on
	// them meanwhile, and none that runs is removed under.
	release, err := s.HoldFiles(Item{KindEnvironment, name}, "removing", true)
	if err != nil {
		return err
	}
	defer release()
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.remove(ctx, name, deleteFiles)
	}
	c, _, err := s.container(ctx, name)
	if err != nil {
		return err
	}
	// Saved stopped first: a removal that fails or is interrupted before
	// the definition is deleted must not have it built and started again.
	if err := s.setPower(name, PowerStopped); err != nil {
		return err
	}
	if c.ID != "" {
		if err := s.Eng.Remove(ctx, c.ID); err != nil && !errors.Is(err, engine.ErrNotFound) {
			return err
		}
	}
	if err := s.removeNetwork(ctx, name); errors.Is(err, ErrConflict) {
		s.Log.Warn("leaving a network that isn't this environment's", "environment", name, "err", err)
	} else if err != nil && !errors.Is(err, engine.ErrNotFound) {
		s.Log.Warn("removing network", "environment", name, "err", err)
	}
	if err := s.forget(name); err != nil {
		return err
	}
	var folderErr error
	if deleteFiles {
		folderErr = s.removeTree(ctx, Folder{Path: s.Layout.Home}, name)
	}
	return errors.Join(folderErr, s.removed(name))
}

// removed tells Removed that name's definition was deleted by a removal.
func (s *Service) removed(name string) error {
	if s.Removed == nil {
		return nil
	}
	return s.Removed(name)
}

func isAlreadyConnected(err error) bool {
	return err != nil && (containsFold(err.Error(), "already exists") || containsFold(err.Error(), "already attached"))
}

// IsEnvironment rejects runtime roles even if a foreign container copied environment labels.
func IsEnvironment(labels map[string]string) bool {
	return labels[LabelManaged] == "true" && labels[vpn.LabelRole] == "" && labels["deskyard.role"] != "vpn"
}
