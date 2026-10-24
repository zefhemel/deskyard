// Package desktops creates and runs desktops: containers labelled as the
// supervisor's, each with its own network and workspace folder. Containers are
// the active records; private recovery intent protects container replacements.
package desktops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

const (
	LabelManaged  = "deskyard.managed"
	LabelName     = "deskyard.name"
	LabelID       = "deskyard.id"
	LabelMemory   = "deskyard.setting.memory"
	LabelCPUs     = "deskyard.setting.cpus"
	LabelCreated  = "deskyard.created"
	LabelImage    = "deskyard.setting.image"
	LabelContract = "deskyard.contract"
	TokenEnv      = "WORKSPACE_PROXY_TOKEN"
	Contract      = "3" // 3: a Selkies desktop (Xvfb, Selkies behind Caddy)

	stopTimeout = 30 * time.Second
	shmBytes    = 1 << 30
)

var (
	ErrNotFound = errors.New("no such desktop")
	ErrExists   = errors.New("a desktop with that name already exists")
	ErrBusy     = errors.New("the desktop is busy with another operation")
	ErrStopped  = errors.New("the desktop is stopped")
)

type Settings struct {
	Image       string
	MemoryBytes int64 // 0: no limit
	NanoCPUs    int64 // 0: no limit
	// Camera is the host's virtual camera (a v4l2loopback /dev/videoN) the
	// desktop has to itself, which viewers' webcams feed; "" for none.
	Camera          string
	VPNConnectionID string
	LANExceptions   []vpn.Exception
	Mounts          []HostMount
}

// Desktop is what the supervisor knows about one desktop, read from its container.
type Desktop struct {
	ID, Name    string
	State       string // running, stopped, plus transient creating/upgrading/removing/starting
	Image       string
	ImageID     string
	ImageLabels map[string]string
	StartedAt   time.Time
	Created     time.Time // zero for desktops made before the label existed
	Folder      string
	Settings    Settings
	VPN         VPNStatus
	Container   engine.Container
}

func (c Desktop) Running() bool { return c.State == "running" }

type Config struct {
	DataDir     string
	Subnet      string // DESKYARD_SUBNET
	InContainer bool
	SelfID      string // the supervisor's own container, when InContainer
	SysDir      string // the host's /sys, for its virtual cameras ("" is /sys)
	// LocalEngine: the engine runs on this machine (a unix socket), so the
	// host's cameras are the ones in SysDir. Elsewhere there are none to give.
	LocalEngine bool
}

type Service struct {
	Networking *Networking
	Eng        engine.Engine
	Cfg        Config
	Log        *slog.Logger
	// ImageUsed, when set, hears which image ID a reference resolved to
	// whenever a desktop is created or recreated from it.
	ImageUsed func(ref, id string)

	mu      sync.Mutex
	busy    map[string]string // name → transient state
	imgInfo map[string]engine.Image
	camMu   sync.Mutex // held while a camera is checked and given
}

func New(e engine.Engine, cfg Config, log *slog.Logger) *Service {
	return &Service{Eng: e, Cfg: cfg, Log: log, busy: map[string]string{}, imgInfo: map[string]engine.Image{}}
}

func containerName(name string) string { return "deskyard-" + name }

// Begin marks a desktop busy for an operation; Done clears it. An empty state
// reserves the slot without overriding the engine's live state.
func (s *Service) Begin(name, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.busy[name]; ok {
		return ErrBusy
	}
	s.busy[name] = state
	return nil
}

func (s *Service) Done(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.busy, name)
}

// Busy returns the transient states of desktops in the middle of a long operation.
func (s *Service) Busy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.busy))
	for k, v := range s.busy {
		out[k] = v
	}
	return out
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

func (s *Service) fromContainer(ctx context.Context, c engine.Container) Desktop {
	name := c.Labels[LabelName]
	image := c.Labels[LabelImage]
	if image == "" {
		image = c.Image
	}
	comp := Desktop{
		ID: c.Labels[LabelID], Name: name, Image: image, ImageID: c.ImageID,
		StartedAt: c.StartedAt, Folder: filepath.Join(s.Cfg.DataDir, name), Container: c,
		Settings: Settings{Image: image, MemoryBytes: c.MemoryBytes, NanoCPUs: c.NanoCPUs, Camera: cameraOf(c.Devices), VPNConnectionID: c.Labels[vpn.LabelConnection]},
	}
	comp.Settings.Mounts, _ = hostMountsFromLabels(c.Labels)
	comp.Created, _ = time.Parse(time.RFC3339, c.Labels[LabelCreated])
	comp.ImageLabels = s.image(ctx, c.ImageID).Labels
	switch {
	case c.Running():
		comp.State = "running"
	default:
		comp.State = "stopped"
	}
	if b := s.Busy()[name]; b != "" {
		comp.State = b
	}
	if s.Networking != nil {
		comp = s.Networking.decorate(comp)
	} else {
		comp.VPN.State = "disabled"
	}
	return comp
}

func (s *Service) List(ctx context.Context) ([]Desktop, error) {
	cs, err := s.Eng.List(ctx, map[string]string{LabelManaged: "true"})
	if err != nil {
		return nil, err
	}
	out := make([]Desktop, 0, len(cs))
	seen := map[string]bool{}
	for _, c := range cs {
		if !IsDesktop(c.Labels) {
			continue
		}
		comp := s.fromContainer(ctx, c)
		seen[comp.Name] = true
		out = append(out, comp)
	}
	if s.Networking != nil {
		for _, r := range s.Networking.all() {
			if !seen[r.Name] {
				out = append(out, s.Networking.synthetic(r))
				seen[r.Name] = true
			}
		}
	}
	// Desktops being created don't have a container yet.
	for name, state := range s.Busy() {
		if !seen[name] && state == "creating" {
			out = append(out, Desktop{Name: name, State: state, Folder: filepath.Join(s.Cfg.DataDir, name)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) Get(ctx context.Context, name string) (Desktop, error) {
	if ValidName(name) != nil {
		return Desktop{}, ErrNotFound
	}
	c, err := s.Eng.Inspect(ctx, containerName(name))
	if errors.Is(err, engine.ErrNotFound) || (err == nil && !IsDesktop(c.Labels)) {
		if s.Networking != nil {
			if r, ok := s.Networking.record(name); ok {
				return s.Networking.synthetic(r), nil
			}
		}
		if st, ok := s.Busy()[name]; ok && st == "creating" {
			return Desktop{Name: name, State: st, Folder: filepath.Join(s.Cfg.DataDir, name)}, nil
		}
		return Desktop{}, ErrNotFound
	}
	if err != nil {
		return Desktop{}, err
	}
	return s.fromContainer(ctx, c), nil
}

// FolderExists reports whether a workspace folder of that name is already there.
func (s *Service) FolderExists(name string) bool {
	st, err := os.Stat(filepath.Join(s.Cfg.DataDir, name))
	return err == nil && st.IsDir()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// Create makes a new desktop and starts it. progress reports steps (message,
// fraction or -1). On failure nothing is left behind but a folder that was
// already there.
func (s *Service) Create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return Desktop{}, err
		}
		defer unlock()
	}
	if set.VPNConnectionID != "" || len(set.LANExceptions) > 0 {
		if s.Networking == nil {
			return Desktop{}, errors.New("VPN networking unavailable")
		}
		return s.Networking.create(ctx, name, set, progress)
	}
	if err := ValidName(name); err != nil {
		return Desktop{}, err
	}
	if _, err := s.Eng.Inspect(ctx, containerName(name)); err == nil {
		return Desktop{}, ErrExists
	}
	if len(set.Mounts) > 0 {
		progress("Checking host folders", -1)
	}
	if err := s.checkHostMountSources(ctx, set.Mounts); err != nil {
		return Desktop{}, err
	}
	if _, err := s.EnsureImage(ctx, set.Image, progress); err != nil {
		return Desktop{}, err
	}
	folder := filepath.Join(s.Cfg.DataDir, name)
	createdFolder := false
	if !s.FolderExists(name) {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return Desktop{}, err
		}
		createdFolder = true
	}
	id := randomHex(16)
	cleanup := func() {
		bg := context.WithoutCancel(ctx)
		_ = s.Eng.Remove(bg, containerName(name))
		_ = s.removeNetwork(bg, name)
		if createdFolder {
			_ = os.Remove(folder) // only if still empty
		}
	}
	progress("Creating the desktop", -1)
	if err := s.createNetwork(ctx, name, id); err != nil {
		cleanup()
		return Desktop{}, err
	}
	if err := s.createContainer(ctx, name, id, randomHex(32), time.Now(), set); err != nil {
		cleanup()
		return Desktop{}, err
	}
	progress("Starting", -1)
	if err := s.Eng.Start(ctx, containerName(name)); err != nil {
		cleanup()
		return Desktop{}, err
	}
	progress("Starting the desktop", -1)
	if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
		return Desktop{}, err
	}
	return s.Get(ctx, name)
}

// WaitReady waits until the desktop's desktop answers through its Caddy.
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

func (s *Service) createNetwork(ctx context.Context, name, id string) error {
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
	pool, err := netipParse(s.Cfg.Subnet)
	if err != nil {
		return err
	}
	subnet, err := freeSubnet(pool, used)
	if err != nil {
		return err
	}
	return s.Eng.CreateNetwork(ctx, containerName(name), subnet.String(), map[string]string{
		LabelManaged: "true", LabelName: name, LabelID: id,
	})
}

func (s *Service) createContainer(ctx context.Context, name, id, token string, created time.Time, set Settings) error {
	return s.createPinnedContainer(ctx, name, id, token, created, set, set.Image)
}
func (s *Service) createContainerIn(ctx context.Context, name, id, token string, created time.Time, set Settings, runtimeID string) error {
	return s.createPinnedContainerIn(ctx, name, id, token, created, set, set.Image, runtimeID)
}
func (s *Service) createPinnedContainer(ctx context.Context, name, id, token string, created time.Time, set Settings, imageID string) error {
	return s.createPinnedContainerIn(ctx, name, id, token, created, set, imageID, "")
}
func (s *Service) createPinnedContainerIn(ctx context.Context, name, id, token string, created time.Time, set Settings, imageID, runtimeID string) error {
	if set.VPNConnectionID != "" && runtimeID == "" {
		return errors.New("VPN selection requires a prepared network namespace")
	}
	if err := ValidateHostMounts(set.Mounts); err != nil {
		return err
	}
	labels := map[string]string{LabelManaged: "true", LabelName: name, LabelID: id, LabelImage: set.Image}
	if len(set.Mounts) > 0 {
		raw, err := json.Marshal(set.Mounts)
		if err != nil {
			return err
		}
		labels[LabelHostMounts] = string(raw)
	}
	if !created.IsZero() {
		labels[LabelCreated] = created.UTC().Format(time.RFC3339)
	}
	if set.MemoryBytes > 0 {
		labels[LabelMemory] = strconv.FormatInt(set.MemoryBytes, 10)
	}
	if set.NanoCPUs > 0 {
		labels[LabelCPUs] = strconv.FormatInt(set.NanoCPUs, 10)
	}
	spec := engine.ContainerSpec{
		Name: containerName(name), Image: imageID, Labels: labels,
		Env:     map[string]string{TokenEnv: token},
		Mounts:  []engine.Mount{{Source: filepath.Join(s.Cfg.DataDir, name), Target: "/workspace"}},
		Network: containerName(name), Aliases: []string{containerName(name)},
		MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs, ShmBytes: shmBytes,
		Flatpak: true, Restart: "unless-stopped", NoNewPrivileges: len(set.Mounts) > 0,
	}
	for _, m := range set.Mounts {
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly, MustExist: true})
	}
	if set.Camera != "" {
		s.camMu.Lock()
		defer s.camMu.Unlock()
		if err := s.cameraFree(ctx, name, set.Camera); err != nil {
			return err
		}
		spec.Devices = []string{set.Camera}
	}
	if runtimeID != "" {
		spec.NetworkContainer = runtimeID
		spec.Network = ""
		spec.Aliases = nil
		spec.Restart = "no"
		spec.Labels[vpn.LabelConnection] = set.VPNConnectionID
		if r, ok := s.Networking.record(name); ok {
			spec.Labels[vpn.LabelRevision] = strconv.FormatUint(r.Revision, 10)
		}
	}
	if !s.Cfg.InContainer && runtimeID == "" {
		zero := 0
		spec.PublishLocal = &zero
	}
	if _, err := s.Eng.Create(ctx, spec); err != nil {
		return err
	}
	if s.Cfg.InContainer {
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

// Start starts a stopped desktop. One whose camera has gone from the host
// (the module unloaded, a reboot without it) can't start with it, so it is
// recreated without the camera, which is logged, and started.
// The caller reserves the desktop with Begin, as for Recreate.
func (s *Service) Start(ctx context.Context, name string) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.power(ctx, name, "start")
	}
	c, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	if c.Settings.VPNConnectionID != "" {
		return errors.New("VPN recovery record is missing; administrator repair required")
	}
	err = s.Eng.Start(ctx, containerName(name))
	if err == nil || c.Settings.Camera == "" || !s.cameraGone(c.Settings.Camera) {
		return err
	}
	return s.withoutCameraReserved(ctx, c, true)
}

// cameraGone reports whether a desktop's camera has gone from the host.
// Only a local engine's host can be looked at; elsewhere none is given.
func (s *Service) cameraGone(camera string) bool {
	return s.Cfg.LocalEngine && !slices.Contains(s.HostCameras(), camera)
}

// withoutCamera recreates a desktop whose camera is gone, without it, and
// logs it; start starts it afterwards. It marks the desktop busy, and isn't
// cut short by the caller's deadline: a recreate stopped halfway loses the
// container.
func (s *Service) withoutCamera(ctx context.Context, c Desktop, start bool) error {
	release, err := s.Reserve(c.Name, "starting")
	if err != nil {
		return err
	}
	defer release()
	return s.withoutCameraReserved(ctx, c, start)
}

// withoutCameraReserved is also used by power requests that already hold the
// desktop reservation. Startup reconciliation uses the reserving wrapper above.
func (s *Service) withoutCameraReserved(ctx context.Context, c Desktop, start bool) error {
	ctx = context.WithoutCancel(ctx)
	s.Log.Warn("camera gone from the host; recreating the desktop without it", "desktop", c.Name, "camera", c.Settings.Camera)
	set := c.Settings
	set.Camera = ""
	if _, err := s.recreateDirect(ctx, c.Name, set, func(string, float64) {}); err != nil {
		return err
	}
	if !start {
		return nil
	}
	if d, err := s.Get(ctx, c.Name); err == nil && d.Container.Running() {
		return nil
	}
	return s.Eng.Start(ctx, containerName(c.Name))
}

// Stop and Restart, like Start, require the caller's Begin/Done reservation.
func (s *Service) Stop(ctx context.Context, name string) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.power(ctx, name, "stop")
	}
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	return s.Eng.Stop(ctx, containerName(name), stopTimeout)
}

func (s *Service) Restart(ctx context.Context, name string) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.power(ctx, name, "restart")
	}
	c, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	if c.Settings.VPNConnectionID != "" {
		return errors.New("VPN recovery record is missing; administrator repair required")
	}
	err = s.Eng.Restart(ctx, containerName(name), stopTimeout)
	if err == nil || c.Settings.Camera == "" || !s.cameraGone(c.Settings.Camera) {
		return err
	}
	return s.withoutCameraReserved(ctx, c, true)
}

// Remove stops and removes the container and its network; the workspace
// folder is deleted only with deleteFiles.
func (s *Service) Remove(ctx context.Context, name string, deleteFiles bool) error {
	if s.Networking != nil {
		unlock, err := s.Networking.lock(ctx, name)
		if err != nil {
			return err
		}
		defer unlock()
	}
	if s.Networking != nil && s.Networking.selected(name) {
		return s.Networking.remove(ctx, name, deleteFiles)
	}
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	if err := s.Eng.Remove(ctx, containerName(name)); err != nil && !errors.Is(err, engine.ErrNotFound) {
		return err
	}
	if err := s.removeNetwork(ctx, name); err != nil && !errors.Is(err, engine.ErrNotFound) {
		s.Log.Warn("removing network", "desktop", name, "err", err)
	}
	if deleteFiles {
		return s.deleteFolder(ctx, name)
	}
	return nil
}

// removeNetwork removes a desktop's network. In container mode the supervisor
// joined it to reach the desktop, and has to leave it first.
func (s *Service) removeNetwork(ctx context.Context, name string) error {
	if s.Cfg.InContainer {
		_ = s.Eng.DisconnectNetwork(ctx, containerName(name), s.Cfg.SelfID)
	}
	return s.Eng.RemoveNetwork(ctx, containerName(name))
}

// FolderSize adds up the sizes of the files in a desktop's folder. complete
// is false when some directories couldn't be read (files belong to UID 1000).
func (s *Service) FolderSize(name string) (bytes int64, complete bool) {
	complete = true
	// Through an os.Root: a desktop's links never lead the walk outside.
	root, err := os.OpenRoot(filepath.Join(s.Cfg.DataDir, name))
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

// deleteFolder removes a workspace folder. Its files belong to UID 1000 (and
// Flatpak leaves read-only directories), so the removal runs in a helper
// container of alpine, as root.
func (s *Service) deleteFolder(ctx context.Context, name string) error {
	folder := filepath.Join(s.Cfg.DataDir, name)
	if err := os.RemoveAll(folder); err == nil {
		return nil
	}
	if err := s.ensureHelper(ctx); err != nil {
		return err
	}
	hname := "deskyard-remove-" + randomHex(4)
	_, err := s.Eng.Create(ctx, engine.ContainerSpec{
		Name: hname, Image: helperImage, Cmd: []string{"rm", "-rf", "/data/" + name},
		Mounts: []engine.Mount{{Source: s.Cfg.DataDir, Target: "/data"}},
	})
	if err != nil {
		return err
	}
	defer func() { _ = s.Eng.Remove(context.WithoutCancel(ctx), hname) }()
	if err := s.Eng.Start(ctx, hname); err != nil {
		return err
	}
	for i := 0; i < 600; i++ {
		c, err := s.Eng.Inspect(ctx, hname)
		if err != nil || !c.Running() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(folder); err == nil {
		return fmt.Errorf("could not delete %s", folder)
	}
	return nil
}

// helperImage runs as root over desktop folders whose files the supervisor
// itself can't read or delete (they belong to UID 1000).
const helperImage = "alpine:3.22"

func (s *Service) ensureHelper(ctx context.Context) error {
	if _, err := s.Eng.ImageInspect(ctx, helperImage); errors.Is(err, engine.ErrNotFound) {
		return s.Eng.Pull(ctx, helperImage, nil)
	}
	return nil
}

// FolderTar streams a desktop's folder as a tar archive (names relative to
// the folder, starting with "./"), read in a helper container of alpine as
// root, for when the supervisor can't read the files itself. The folder is
// mounted read-only, and the helper has no network. Close removes the helper, and reports a failed or
// incomplete read.
//
// With paths (relative to the folder, as "./a/b" or "a/b"), only those go in
// the archive, in that order. Names with a newline can't be asked for.
//
// A file with several names in the archive has its content once, at the
// first; the others are hard links (tar.TypeLink) to it.
func (s *Service) FolderTar(ctx context.Context, name string, paths ...string) (io.ReadCloser, error) {
	var list strings.Builder
	for _, p := range paths {
		if strings.Contains(p, "\n") {
			return nil, fmt.Errorf("can't read %q: a newline in its name", p)
		}
		list.WriteString("./" + strings.TrimPrefix(p, "./") + "\n")
	}
	folder := filepath.Join(s.Cfg.DataDir, name)
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s has no folder", name)
	}
	if err := s.ensureHelper(ctx); err != nil {
		return nil, err
	}
	hname := "deskyard-read-" + randomHex(4)
	if _, err := s.Eng.Create(ctx, engine.ContainerSpec{
		Name: hname, Image: helperImage, Cmd: []string{"sleep", "86400"}, Network: "none",
		Mounts: []engine.Mount{{Source: folder, Target: "/home-src", ReadOnly: true}},
	}); err != nil {
		return nil, err
	}
	remove := func() { _ = s.Eng.Remove(context.WithoutCancel(ctx), hname) }
	if err := s.Eng.Start(ctx, hname); err != nil {
		remove()
		return nil, err
	}
	spec := engine.ExecSpec{Cmd: []string{"tar", "-C", "/home-src", "-cf", "-", "."}}
	if len(paths) > 0 {
		spec = engine.ExecSpec{Cmd: []string{"tar", "-C", "/home-src", "-cf", "-", "-T", "-"}, Stdin: true}
	}
	p, err := s.Eng.Exec(ctx, hname, spec)
	if err != nil {
		remove()
		return nil, err
	}
	if len(paths) > 0 {
		go func() {
			_, _ = io.WriteString(p.Stdin, list.String())
			_ = p.Stdin.Close()
		}()
	}
	// Drain stderr while stdout is read (the streams share one connection).
	var errOut bytes.Buffer
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		if p.Stderr != nil {
			_, _ = io.Copy(&limitWriter{&errOut, 4096}, p.Stderr)
		}
	}()
	return &helperRead{Reader: p.Stdout, close: func() error {
		// Whatever wasn't read yet: the archive's end, or the rest after an error.
		_, _ = io.Copy(io.Discard, p.Stdout)
		<-errDone
		code, err := p.Wait()
		p.Close()
		remove()
		if err == nil && code != 0 {
			err = fmt.Errorf("reading %s failed: %s", name, strings.TrimSpace(errOut.String()))
		}
		return err
	}}, nil
}

type helperRead struct {
	io.Reader
	close func() error
	once  sync.Once
	err   error
}

func (h *helperRead) Close() error {
	h.once.Do(func() { h.err = h.close() })
	return h.err
}

// limitWriter keeps the first n bytes and drops the rest.
type limitWriter struct {
	b *bytes.Buffer
	n int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if room := w.n - w.b.Len(); room > 0 {
		w.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// Target is where the proxy reaches a running desktop's port 8080, and the
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
		g, ok := s.Networking.target(name)
		if !ok {
			return "", "", errors.New("VPN gateway unavailable")
		}
		c.Container = g
	}
	if s.Cfg.InContainer {
		if ip := c.Container.IP[containerName(name)]; ip != "" {
			return net.JoinHostPort(ip, "8080"), token, nil
		}
		return containerName(name) + ":8080", token, nil
	}
	if c.Container.LocalPort == 0 {
		return "", "", fmt.Errorf("%s has no published port", name)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Container.LocalPort)), token, nil
}

// Reconcile runs at startup. In a container, it reconnects the supervisor to
// every desktop's network (e.g. after the supervisor's container was
// recreated). And a desktop whose camera has gone from the host (a reboot
// without the module) is recreated without it, which is logged: started if
// the engine had tried to start it and failed, left stopped if a user had
// stopped it.
func (s *Service) Reconcile(ctx context.Context) error {
	var errs []error
	if s.Cfg.InContainer {
		nets, err := s.Eng.Networks(ctx, map[string]string{LabelManaged: "true"})
		if err != nil {
			errs = append(errs, err)
		}
		for _, n := range nets {
			if err := s.Eng.ConnectNetwork(ctx, n.Name, s.Cfg.SelfID, nil); err != nil && !isAlreadyConnected(err) {
				s.Log.Warn("reconnecting to network", "network", n.Name, "err", err)
			}
		}
	}
	if s.Networking != nil {
		if err := s.Networking.Reconcile(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.recoverReplacements(ctx); err != nil {
		errs = append(errs, err)
	}
	list, err := s.List(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range list {
		if c.Settings.VPNConnectionID != "" && (s.Networking == nil || !s.Networking.selected(c.Name)) {
			_ = s.Eng.UpdateRestartPolicy(ctx, c.Container.ID, "no")
			_ = s.Eng.Stop(ctx, c.Container.ID, stopTimeout)
			errs = append(errs, errors.New("VPN recovery record missing; administrator repair required"))
			continue
		}
		if s.Networking != nil && s.Networking.selected(c.Name) {
			continue
		}
		if c.Settings.Camera == "" || !s.cameraGone(c.Settings.Camera) {
			continue
		}
		failed := !c.Container.Running() && c.Container.Error != ""
		if err := s.withoutCamera(ctx, c, failed); err != nil {
			s.Log.Warn("recreating a desktop without its gone camera", "desktop", c.Name, "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func isAlreadyConnected(err error) bool {
	return err != nil && (containsFold(err.Error(), "already exists") || containsFold(err.Error(), "already attached"))
}

// IsDesktop rejects runtime roles even if a foreign container copied desktop labels.
func IsDesktop(labels map[string]string) bool {
	return labels[LabelManaged] == "true" && labels[vpn.LabelRole] == "" && labels["deskyard.role"] != "vpn"
}
