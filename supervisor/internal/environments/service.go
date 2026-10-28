// Package environments creates and runs environments. Each is defined in
// environments.json (Definitions), the only authority for its configuration,
// and realised as a disposable container labelled as this supervisor's, with
// its own network and home folder. Private recovery intent protects container
// replacements.
package environments

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
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
	DataDir string // the supervisor's data folder (see config.Layout)
	Subnet  string // DESKYARD_SUBNET
	SelfID  string // the supervisor's own container; empty in Go tests, which have none
	// DataHost is where the data folder lives on the engine host (see
	// config.Layout.WithHost); empty in Go tests, which have no host folder.
	DataHost string
	// DesktopPort is the port the proxy dials on a desktop. Zero means 8080;
	// only tests set it, to reach a fake desktop served on a local port.
	DesktopPort int
}

type Service struct {
	Networking *Networking
	Eng        engine.Engine
	Cfg        Config
	Layout     config.Layout // from Cfg.DataDir
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
	layout := config.NewLayout(cfg.DataDir).WithHost(cfg.DataHost)
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

func (s *Service) image(ctx context.Context, ref string) engine.Image {
	s.mu.Lock()
	img, ok := s.imgInfo[ref]
	s.mu.Unlock()
	if ok {
		return img
	}
	img, err := s.Eng.ImageInspect(ctx, ref)
	if err != nil {
		return engine.Image{}
	}
	s.mu.Lock()
	s.imgInfo[ref] = img
	s.mu.Unlock()
	return img
}

// ForgetImage drops cached image information (after a pull).
func (s *Service) ForgetImage(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.imgInfo, ref)
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

// ImagesInUse returns the IDs of the images that environment definitions pin
// (stopped environments without a container included) and that containers
// on the engine run from, of any supervisor and in any state, parked
// replacements included.
func (s *Service) ImagesInUse(ctx context.Context) (map[string]bool, error) {
	inUse := map[string]bool{}
	for _, def := range s.Defs.List() {
		if def.Image.ID != "" {
			inUse[def.Image.ID] = true
		}
	}
	all, err := s.Eng.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		inUse[c.ImageID] = true
	}
	return inUse, nil
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

// FolderExists reports whether a workspace folder of that name is already there.
func (s *Service) FolderExists(name string) bool {
	st, err := os.Stat(s.Layout.HomeOf(name))
	return err == nil && st.IsDir()
}

// missingHome is the error of a defined environment without its home folder.
func (s *Service) missingHome(name string) error {
	return fmt.Errorf("%s's home folder %s is missing", name, s.Layout.ToHost(s.Layout.HomeOf(name)))
}

// missingVolume is the error of the first of grants, name's, whose volume
// isn't in volumes.json or whose local folder is missing; nil when there is
// none. A host volume's folder is on the engine host, which the supervisor
// may not see: checkHostSources and the strict mount check that.
func (s *Service) missingVolume(name string, grants []VolumeGrant) error {
	for _, g := range grants {
		v, ok := s.Vols.Get(g.Name)
		if !ok {
			return fmt.Errorf("%s uses volume %s, which doesn't exist", name, g.Name)
		}
		if v.Kind == VolumeHost {
			continue
		}
		folder := s.Layout.VolumeOf(g.Name)
		if st, err := os.Stat(folder); err != nil || !st.IsDir() {
			return fmt.Errorf("%s uses volume %s, whose folder %s is missing", name, g.Name, s.Layout.ToHost(folder))
		}
	}
	return nil
}

// missingFolder is name's missing home folder, or else the first volume
// of grants missing as missingVolume has it; nil when there is none.
func (s *Service) missingFolder(name string, grants []VolumeGrant) error {
	if !s.FolderExists(name) {
		return s.missingHome(name)
	}
	return s.missingVolume(name, grants)
}

// checkFolders records a missing home or granted volume folder as name's
// error, and clears that error once the folder is back. It is kept apart from
// the error of a failed change, which shows again once the folder is back.
// Only creation makes a home, and only CreateVolume a volume folder.
func (s *Service) checkFolders(name string) error {
	def, defined := s.Defs.Get(name)
	err := s.missingFolder(name, def.Volumes)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil || !defined {
		delete(s.folderErrs, name)
	} else {
		s.folderErrs[name] = err.Error()
	}
	return err
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) tokenPath(name string) string {
	return filepath.Join(s.Layout.Secret, "proxy", name)
}

// proxyToken is the token the proxy sends to name's Caddy, kept in
// secret/proxy/<name> and made on first use.
func (s *Service) proxyToken(name string) (string, error) {
	b, err := os.ReadFile(s.tokenPath(name))
	if err == nil && len(bytes.TrimSpace(b)) > 0 {
		return string(bytes.TrimSpace(b)), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	token := randomHex(32)
	if err := store.WriteFile(s.tokenPath(name), []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
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

// EnsureImage pulls ref when it isn't present and checks the image contract.
func (s *Service) EnsureImage(ctx context.Context, ref string, progress func(string, float64)) (engine.Image, error) {
	img, err := s.Eng.ImageInspect(ctx, ref)
	if errors.Is(err, engine.ErrNotFound) {
		progress("Pulling "+ref, 0)
		err = s.Eng.Pull(ctx, ref, func(p engine.PullProgress) {
			if p.Total > 0 {
				progress(fmt.Sprintf("Pulling image, %d of %d MB", p.Current>>20, p.Total>>20), float64(p.Current)/float64(p.Total))
			}
		})
		if err != nil {
			return engine.Image{}, fmt.Errorf("pulling %s: %w", ref, err)
		}
		s.ForgetImage(ref)
		img, err = s.Eng.ImageInspect(ctx, ref)
	}
	if err != nil {
		return engine.Image{}, err
	}
	if img.Labels[LabelContract] != Contract {
		return engine.Image{}, fmt.Errorf("%s isn't a Deskyard desktop image (label %s=%s missing)", ref, LabelContract, Contract)
	}
	if s.ImageUsed != nil {
		s.ImageUsed(ref, img.ID)
	}
	return img, nil
}

// withSettings is def changed to set, with image the ID set.Image resolved to.
// The revision is bumped when the container is affected.
func withSettings(def Definition, set Settings, image string) Definition {
	next := def
	next.Image = ImageRef{Ref: set.Image, ID: image}
	next.Limits = Limits{MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs}
	next.Network = Network{Kind: NetworkIsolated}
	if set.VPNConnectionID != "" {
		next.Network = Network{Kind: NetworkVPN, Connection: set.VPNConnectionID, LANExceptions: set.LANExceptions}
	}
	next.Volumes = set.Volumes
	next.DockerAccess = set.DockerAccess
	next = next.clone()
	if ContainerAffecting(def, next) {
		next.Revision++
	}
	return next
}

// pinned is the image a definition's container is built from.
func pinned(def Definition) string {
	if def.Image.ID != "" {
		return def.Image.ID
	}
	return def.Image.Ref
}

// Create defines a new environment and starts it. progress reports steps
// (message, fraction or -1). The definition is saved before the engine is
// touched; on failure nothing is left behind but a folder that was already
// there.
func (s *Service) Create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Environment, error) {
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
	def := withSettings(Definition{Name: name, Created: time.Now().UTC(), MemoryReclaim: true, Power: PowerRunning}, set, img.ID)
	def.Revision = 1
	if err := s.Defs.Put(def); err != nil {
		_ = s.forget(name)
		removeFolder()
		return Environment{}, err
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

// WaitReady waits until the environment's desktop answers through its Caddy.
func (s *Service) WaitReady(ctx context.Context, name string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		addr, token, err := s.Target(ctx, name)
		if err == nil {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
			req.Header.Set("X-Deskyard-Token", token)
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		} else if errors.Is(err, ErrStopped) {
			if c, _ := s.Get(ctx, name); c.Container.State == "exited" {
				return fmt.Errorf("%s stopped while starting (see its logs: docker logs %s)", name, containerName(name))
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s didn't come up within %s", name, limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// createNetwork gives name's network the first free subnet of the pool.
// Choosing and creating are one step for this supervisor (subnetMu), so
// environments created at once don't choose the same subnet; one that another
// supervisor on the engine takes meanwhile is chosen again.
func (s *Service) createNetwork(ctx context.Context, name string) error {
	pool, err := netipParse(s.Cfg.Subnet)
	if err != nil {
		return err
	}
	s.subnetMu.Lock()
	defer s.subnetMu.Unlock()
	for attempt := 0; ; attempt++ {
		nets, err := s.Eng.Networks(ctx, nil)
		if err != nil {
			return err
		}
		var used []string
		for _, n := range nets {
			if n.Subnet != "" {
				used = append(used, n.Subnet)
			}
		}
		subnet, err := freeSubnet(pool, used)
		if err != nil {
			return err
		}
		err = s.Eng.CreateNetwork(ctx, containerName(name), subnet.String(), s.ownLabels(name))
		if !errors.Is(err, engine.ErrSubnetTaken) || attempt == 4 {
			return err
		}
	}
}

// network checks name's network: engine.ErrNotFound when there is none,
// ErrConflict when one of its name isn't this supervisor's.
func (s *Service) network(ctx context.Context, name string) error {
	nets, err := s.Eng.Networks(ctx, nil)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.Name == containerName(name) {
			if !s.owns(n.Labels, name) {
				return fmt.Errorf("network %s: %w", n.Name, ErrConflict)
			}
			return nil
		}
	}
	return engine.ErrNotFound
}

// ensureNetwork creates name's network unless it is there.
func (s *Service) ensureNetwork(ctx context.Context, name string) error {
	if err := s.network(ctx, name); !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	return s.createNetwork(ctx, name)
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
	token, err := s.proxyToken(name)
	if err != nil {
		return err
	}
	labels := s.ownLabels(name)
	labels[LabelRevision] = strconv.FormatUint(revision, 10)
	spec := engine.ContainerSpec{
		Name: containerName(name), Image: imageID, Labels: labels,
		Env: map[string]string{TokenEnv: token},
		// Strict, so the engine never makes a missing home on start.
		Mounts:  []engine.Mount{{Source: s.Layout.HomeOf(name), Target: "/workspace", MustExist: true}},
		Network: containerName(name), Aliases: []string{containerName(name)},
		MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs, ShmBytes: shmBytes,
		Flatpak: true, Restart: "unless-stopped", NoNewPrivileges: len(set.Volumes) > 0,
	}
	img, err := s.Eng.ImageInspect(ctx, imageID)
	if err != nil {
		return fmt.Errorf("inspect desktop FUSE support: %w", err)
	}
	if img.Labels["deskyard.fuse"] == "userns-v1" {
		spec.Devices = append(spec.Devices, "/dev/fuse")
	}
	if set.DockerAccess {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: dockerSocket, Target: dockerSocket, MustExist: true})
		spec.Env["DESKYARD_DOCKER_ACCESS"] = "1"
	}
	// Strict too: a missing volume folder is an error, never made empty.
	for _, g := range set.Volumes {
		v, ok := s.Vols.Get(g.Name)
		if !ok {
			return fmt.Errorf("volume %s doesn't exist", g.Name)
		}
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: s.VolumeFolder(v).Path, Target: GrantPath(g), ReadOnly: g.ReadOnly, MustExist: true})
	}
	if runtimeID != "" {
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

// removeNetwork removes an environment's network. The supervisor joined it to
// reach the environment, and has to leave it first. A network of its name that
// isn't this supervisor's is left alone (ErrConflict).
func (s *Service) removeNetwork(ctx context.Context, name string) error {
	if err := s.network(ctx, name); err != nil {
		return err
	}
	if s.Cfg.SelfID != "" {
		_ = s.Eng.DisconnectNetwork(ctx, containerName(name), s.Cfg.SelfID)
	}
	return s.Eng.RemoveNetwork(ctx, containerName(name))
}

// FolderSize adds up the sizes of the files in an environment's folder. complete
// is false when some directories couldn't be read (the supervisor is UID 1000,
// so only root-owned files, as an environment with Docker access can leave,
// are out of reach).
func (s *Service) FolderSize(name string) (bytes int64, complete bool) {
	return treeSize(s.Layout.HomeOf(name))
}

// VolumeSize is FolderSize for the folder of the volume name. A host
// volume's folder is on the engine host, which the supervisor can't see:
// its size is 0 and incomplete.
func (s *Service) VolumeSize(name string) (bytes int64, complete bool) {
	v, ok := s.Vols.Get(name)
	if !ok {
		return 0, false
	}
	folder := s.VolumeFolder(v)
	if folder.OnEngineHost {
		return 0, false
	}
	return treeSize(folder.Path)
}

func treeSize(dir string) (bytes int64, complete bool) {
	complete = true
	// Through an os.Root: an environment's links never lead the walk outside.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, !errors.Is(err, fs.ErrPermission)
	}
	defer root.Close()
	_ = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			complete = false
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				bytes += info.Size()
			}
		}
		return nil
	})
	return bytes, complete
}

// Target is where the proxy reaches a running environment's port 8080, and the
// token it must send.
func (s *Service) Target(ctx context.Context, name string) (addr, token string, err error) {
	c, err := s.Get(ctx, name)
	if err != nil {
		return "", "", err
	}
	if !c.Container.Running() {
		return "", "", ErrStopped
	}
	token = c.Container.Env[TokenEnv]
	if c.Settings.VPNConnectionID != "" {
		if s.Networking == nil {
			return "", "", errVPNUnavailable
		}
		g, ok := s.Networking.target(name)
		if !ok {
			return "", "", errors.New("VPN gateway unavailable")
		}
		c.Container = g
	}
	port := "8080"
	if s.Cfg.DesktopPort != 0 {
		port = strconv.Itoa(s.Cfg.DesktopPort)
	}
	if ip := c.Container.IP[containerName(name)]; ip != "" {
		return net.JoinHostPort(ip, port), token, nil
	}
	return net.JoinHostPort(containerName(name), port), token, nil
}

func isAlreadyConnected(err error) bool {
	return err != nil && (containsFold(err.Error(), "already exists") || containsFold(err.Error(), "already attached"))
}

// IsEnvironment rejects runtime roles even if a foreign container copied environment labels.
func IsEnvironment(labels map[string]string) bool {
	return labels[LabelManaged] == "true" && labels[vpn.LabelRole] == "" && labels["deskyard.role"] != "vpn"
}
