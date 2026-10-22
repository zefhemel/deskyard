// Package desktops creates and runs desktops: containers labelled as the
// supervisor's, each with its own network and workspace folder. The engine is
// the only list of desktops; nothing about them is stored elsewhere.
package desktops

import (
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
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const (
	LabelManaged  = "deskyard.managed"
	LabelName     = "deskyard.name"
	LabelID       = "deskyard.id"
	LabelMemory   = "deskyard.setting.memory"
	LabelCPUs     = "deskyard.setting.cpus"
	LabelCreated  = "deskyard.created"
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
	Camera string
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
	Eng engine.Engine
	Cfg Config
	Log *slog.Logger

	mu      sync.Mutex
	busy    map[string]string // name → transient state
	imgInfo map[string]engine.Image
	camMu   sync.Mutex // held while a camera is checked and given
}

func New(e engine.Engine, cfg Config, log *slog.Logger) *Service {
	return &Service{Eng: e, Cfg: cfg, Log: log, busy: map[string]string{}, imgInfo: map[string]engine.Image{}}
}

func containerName(name string) string { return "deskyard-" + name }

// Begin marks a desktop busy for a long operation; Done clears it.
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
	comp := Desktop{
		ID: c.Labels[LabelID], Name: name, Image: c.Image, ImageID: c.ImageID,
		StartedAt: c.StartedAt, Folder: filepath.Join(s.Cfg.DataDir, name), Container: c,
		Settings: Settings{Image: c.Image, MemoryBytes: c.MemoryBytes, NanoCPUs: c.NanoCPUs, Camera: cameraOf(c.Devices)},
	}
	comp.Created, _ = time.Parse(time.RFC3339, c.Labels[LabelCreated])
	comp.ImageLabels = s.image(ctx, c.ImageID).Labels
	switch {
	case c.Running():
		comp.State = "running"
	default:
		comp.State = "stopped"
	}
	if b, ok := s.Busy()[name]; ok {
		comp.State = b
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
		comp := s.fromContainer(ctx, c)
		seen[comp.Name] = true
		out = append(out, comp)
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
	if errors.Is(err, engine.ErrNotFound) || (err == nil && c.Labels[LabelManaged] != "true") {
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
	return img, nil
}

// Create makes a new desktop and starts it. progress reports steps (message,
// fraction or -1). On failure nothing is left behind but a folder that was
// already there.
func (s *Service) Create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	if err := ValidName(name); err != nil {
		return Desktop{}, err
	}
	if _, err := s.Eng.Inspect(ctx, containerName(name)); err == nil {
		return Desktop{}, ErrExists
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
	labels := map[string]string{LabelManaged: "true", LabelName: name, LabelID: id}
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
		Name: containerName(name), Image: set.Image, Labels: labels,
		Env:     map[string]string{TokenEnv: token},
		Mounts:  []engine.Mount{{Source: filepath.Join(s.Cfg.DataDir, name), Target: "/workspace"}},
		Network: containerName(name), Aliases: []string{containerName(name)},
		MemoryBytes: set.MemoryBytes, NanoCPUs: set.NanoCPUs, ShmBytes: shmBytes,
		Flatpak: true, Restart: "unless-stopped",
	}
	if set.Camera != "" {
		s.camMu.Lock()
		defer s.camMu.Unlock()
		if err := s.cameraFree(ctx, name, set.Camera); err != nil {
			return err
		}
		spec.Devices = []string{set.Camera}
	}
	if !s.Cfg.InContainer {
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
			_ = s.Eng.Remove(context.WithoutCancel(ctx), containerName(name))
			return fmt.Errorf("connecting the supervisor to %s: %w", containerName(name), err)
		}
	}
	return nil
}

// Recreate replaces the container with one on new settings (image, limits),
// keeping its ID, token, network and folder. A running desktop is started again.
func (s *Service) Recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Desktop, error) {
	c, err := s.Get(ctx, name)
	if err != nil {
		return Desktop{}, err
	}
	if _, err := s.EnsureImage(ctx, set.Image, progress); err != nil {
		return Desktop{}, err
	}
	wasRunning := c.Container.Running()
	token := c.Container.Env[TokenEnv]
	progress("Stopping", -1)
	if wasRunning {
		if err := s.Eng.Stop(ctx, c.Container.ID, stopTimeout); err != nil {
			return Desktop{}, err
		}
	}
	progress("Recreating", -1)
	if err := s.Eng.Remove(ctx, c.Container.ID); err != nil {
		return Desktop{}, err
	}
	if err := s.createContainer(ctx, name, c.ID, token, c.Created, set); err != nil {
		// Try to bring the old one back so the desktop isn't lost.
		if err2 := s.createContainer(context.WithoutCancel(ctx), name, c.ID, token, c.Created, c.Settings); err2 != nil {
			return Desktop{}, errors.Join(err, err2)
		}
		return Desktop{}, err
	}
	if wasRunning {
		progress("Starting", -1)
		if err := s.Eng.Start(ctx, containerName(name)); err != nil {
			return Desktop{}, err
		}
		if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
			return Desktop{}, err
		}
	}
	return s.Get(ctx, name)
}

// Start starts a stopped desktop. One whose camera has gone from the host
// (the module unloaded, a reboot without it) can't start with it, so it is
// recreated without the camera, which is logged, and started.
func (s *Service) Start(ctx context.Context, name string) error {
	c, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	err = s.Eng.Start(ctx, containerName(name))
	if err == nil || c.Settings.Camera == "" || slices.Contains(s.HostCameras(), c.Settings.Camera) {
		return err
	}
	s.Log.Warn("camera gone from the host; recreating the desktop without it", "desktop", name, "camera", c.Settings.Camera, "err", err)
	set := c.Settings
	set.Camera = ""
	if _, err := s.Recreate(ctx, name, set, func(string, float64) {}); err != nil {
		return err
	}
	return s.Eng.Start(ctx, containerName(name))
}

func (s *Service) Stop(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	return s.Eng.Stop(ctx, containerName(name), stopTimeout)
}

func (s *Service) Restart(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	return s.Eng.Restart(ctx, containerName(name), stopTimeout)
}

// Remove stops and removes the container and its network; the workspace
// folder is deleted only with deleteFiles.
func (s *Service) Remove(ctx context.Context, name string, deleteFiles bool) error {
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
	folder := filepath.Join(s.Cfg.DataDir, name)
	if _, err := os.Stat(folder); err != nil {
		return 0, true
	}
	_ = filepath.WalkDir(folder, func(_ string, d fs.DirEntry, err error) error {
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
	const helper = "alpine:3.22"
	if _, err := s.Eng.ImageInspect(ctx, helper); errors.Is(err, engine.ErrNotFound) {
		if err := s.Eng.Pull(ctx, helper, nil); err != nil {
			return err
		}
	}
	hname := "deskyard-remove-" + randomHex(4)
	_, err := s.Eng.Create(ctx, engine.ContainerSpec{
		Name: hname, Image: helper, Cmd: []string{"rm", "-rf", "/data/" + name},
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

// Reconcile runs at startup: in a container, it reconnects the supervisor to
// every desktop's network (e.g. after the supervisor's container was recreated).
func (s *Service) Reconcile(ctx context.Context) error {
	if !s.Cfg.InContainer {
		return nil
	}
	nets, err := s.Eng.Networks(ctx, map[string]string{LabelManaged: "true"})
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := s.Eng.ConnectNetwork(ctx, n.Name, s.Cfg.SelfID, nil); err != nil && !isAlreadyConnected(err) {
			s.Log.Warn("reconnecting to network", "network", n.Name, "err", err)
		}
	}
	return nil
}

func isAlreadyConnected(err error) bool {
	return err != nil && (containsFold(err.Error(), "already exists") || containsFold(err.Error(), "already attached"))
}
