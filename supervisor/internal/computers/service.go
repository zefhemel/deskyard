// Package computers creates and runs computers: containers labelled as the
// supervisor's, each with its own network and workspace folder. The engine is
// the only list of computers; nothing about them is stored elsewhere.
package computers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	LabelContract = "deskyard.contract"
	TokenEnv      = "WORKSPACE_PROXY_TOKEN"
	Contract      = "1"

	stopTimeout = 30 * time.Second
	shmBytes    = 1 << 30
)

var (
	ErrNotFound = errors.New("no such computer")
	ErrExists   = errors.New("a computer with that name already exists")
	ErrBusy     = errors.New("the computer is busy with another operation")
	ErrStopped  = errors.New("the computer is stopped")
)

type Settings struct {
	Image       string
	MemoryBytes int64 // 0: no limit
	NanoCPUs    int64 // 0: no limit
}

// Computer is what the supervisor knows about one computer, read from its container.
type Computer struct {
	ID, Name    string
	State       string // running, stopped, plus transient creating/upgrading/removing/starting
	Image       string
	ImageID     string
	ImageLabels map[string]string
	StartedAt   time.Time
	Folder      string
	Settings    Settings
	Container   engine.Container
}

func (c Computer) Running() bool { return c.State == "running" }

type Config struct {
	DataDir     string
	Subnet      string // DESKYARD_SUBNET
	InContainer bool
	SelfID      string // the supervisor's own container, when InContainer
}

type Service struct {
	Eng engine.Engine
	Cfg Config
	Log *slog.Logger

	mu      sync.Mutex
	busy    map[string]string // name → transient state
	imgInfo map[string]engine.Image
}

func New(e engine.Engine, cfg Config, log *slog.Logger) *Service {
	return &Service{Eng: e, Cfg: cfg, Log: log, busy: map[string]string{}, imgInfo: map[string]engine.Image{}}
}

func containerName(name string) string { return "deskyard-" + name }

// Begin marks a computer busy for a long operation; Done clears it.
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

// Busy returns the transient states of computers in the middle of a long operation.
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

func (s *Service) fromContainer(ctx context.Context, c engine.Container) Computer {
	name := c.Labels[LabelName]
	comp := Computer{
		ID: c.Labels[LabelID], Name: name, Image: c.Image, ImageID: c.ImageID,
		StartedAt: c.StartedAt, Folder: filepath.Join(s.Cfg.DataDir, name), Container: c,
		Settings: Settings{Image: c.Image, MemoryBytes: c.MemoryBytes, NanoCPUs: c.NanoCPUs},
	}
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

func (s *Service) List(ctx context.Context) ([]Computer, error) {
	cs, err := s.Eng.List(ctx, map[string]string{LabelManaged: "true"})
	if err != nil {
		return nil, err
	}
	out := make([]Computer, 0, len(cs))
	seen := map[string]bool{}
	for _, c := range cs {
		comp := s.fromContainer(ctx, c)
		seen[comp.Name] = true
		out = append(out, comp)
	}
	// Computers being created don't have a container yet.
	for name, state := range s.Busy() {
		if !seen[name] && state == "creating" {
			out = append(out, Computer{Name: name, State: state, Folder: filepath.Join(s.Cfg.DataDir, name)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) Get(ctx context.Context, name string) (Computer, error) {
	if ValidName(name) != nil {
		return Computer{}, ErrNotFound
	}
	c, err := s.Eng.Inspect(ctx, containerName(name))
	if errors.Is(err, engine.ErrNotFound) || (err == nil && c.Labels[LabelManaged] != "true") {
		if st, ok := s.Busy()[name]; ok && st == "creating" {
			return Computer{Name: name, State: st, Folder: filepath.Join(s.Cfg.DataDir, name)}, nil
		}
		return Computer{}, ErrNotFound
	}
	if err != nil {
		return Computer{}, err
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
		return engine.Image{}, fmt.Errorf("%s isn't a Deskyard computer image (label %s=%s missing)", ref, LabelContract, Contract)
	}
	return img, nil
}

// Create makes a new computer and starts it. progress reports steps (message,
// fraction or -1). On failure nothing is left behind but a folder that was
// already there.
func (s *Service) Create(ctx context.Context, name string, set Settings, progress func(string, float64)) (Computer, error) {
	if err := ValidName(name); err != nil {
		return Computer{}, err
	}
	if _, err := s.Eng.Inspect(ctx, containerName(name)); err == nil {
		return Computer{}, ErrExists
	}
	if _, err := s.EnsureImage(ctx, set.Image, progress); err != nil {
		return Computer{}, err
	}
	folder := filepath.Join(s.Cfg.DataDir, name)
	createdFolder := false
	if !s.FolderExists(name) {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return Computer{}, err
		}
		createdFolder = true
	}
	id := randomHex(16)
	cleanup := func() {
		bg := context.WithoutCancel(ctx)
		_ = s.Eng.Remove(bg, containerName(name))
		_ = s.Eng.RemoveNetwork(bg, containerName(name))
		if createdFolder {
			_ = os.Remove(folder) // only if still empty
		}
	}
	progress("Creating the computer", -1)
	if err := s.createNetwork(ctx, name, id); err != nil {
		cleanup()
		return Computer{}, err
	}
	if err := s.createContainer(ctx, name, id, randomHex(32), set); err != nil {
		cleanup()
		return Computer{}, err
	}
	progress("Starting", -1)
	if err := s.Eng.Start(ctx, containerName(name)); err != nil {
		cleanup()
		return Computer{}, err
	}
	progress("Starting the desktop", -1)
	if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
		return Computer{}, err
	}
	return s.Get(ctx, name)
}

// WaitReady waits until the computer's desktop answers through its Caddy.
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

func (s *Service) createContainer(ctx context.Context, name, id, token string, set Settings) error {
	labels := map[string]string{LabelManaged: "true", LabelName: name, LabelID: id}
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
	if !s.Cfg.InContainer {
		zero := 0
		spec.PublishLocal = &zero
	}
	if _, err := s.Eng.Create(ctx, spec); err != nil {
		return err
	}
	if s.Cfg.InContainer {
		if err := s.Eng.ConnectNetwork(ctx, containerName(name), s.Cfg.SelfID, nil); err != nil {
			return fmt.Errorf("connecting the supervisor to %s: %w", containerName(name), err)
		}
	}
	return nil
}

// Recreate replaces the container with one on new settings (image, limits),
// keeping its ID, token, network and folder. A running computer is started again.
func (s *Service) Recreate(ctx context.Context, name string, set Settings, progress func(string, float64)) (Computer, error) {
	c, err := s.Get(ctx, name)
	if err != nil {
		return Computer{}, err
	}
	if _, err := s.EnsureImage(ctx, set.Image, progress); err != nil {
		return Computer{}, err
	}
	wasRunning := c.Container.Running()
	token := c.Container.Env[TokenEnv]
	progress("Stopping", -1)
	if wasRunning {
		if err := s.Eng.Stop(ctx, c.Container.ID, stopTimeout); err != nil {
			return Computer{}, err
		}
	}
	progress("Recreating", -1)
	if err := s.Eng.Remove(ctx, c.Container.ID); err != nil {
		return Computer{}, err
	}
	if err := s.createContainer(ctx, name, c.ID, token, set); err != nil {
		// Try to bring the old one back so the computer isn't lost.
		if err2 := s.createContainer(context.WithoutCancel(ctx), name, c.ID, token, c.Settings); err2 != nil {
			return Computer{}, errors.Join(err, err2)
		}
		return Computer{}, err
	}
	if wasRunning {
		progress("Starting", -1)
		if err := s.Eng.Start(ctx, containerName(name)); err != nil {
			return Computer{}, err
		}
		if err := s.WaitReady(ctx, name, 2*time.Minute); err != nil {
			return Computer{}, err
		}
	}
	return s.Get(ctx, name)
}

func (s *Service) Start(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
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
	if err := s.Eng.RemoveNetwork(ctx, containerName(name)); err != nil && !errors.Is(err, engine.ErrNotFound) {
		s.Log.Warn("removing network", "computer", name, "err", err)
	}
	if deleteFiles {
		return s.deleteFolder(ctx, name)
	}
	return nil
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

// Target is where the proxy reaches a running computer's port 8080, and the
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
// every computer's network (e.g. after the supervisor's container was recreated).
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
