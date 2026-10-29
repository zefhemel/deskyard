package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
)

// fakeEnv is the computers service as backups see it: definitions,
// volumes and a lifecycle log.
type fakeEnv struct {
	mu       sync.Mutex
	layout   config.Layout
	defs     map[string]computers.Definition
	vols     map[string]computers.Volume
	running  map[string]bool
	reserved map[string]bool
	log      []string
	created  map[string]computers.Settings
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	return &fakeEnv{layout: layout, defs: map[string]computers.Definition{}, vols: map[string]computers.Volume{},
		running: map[string]bool{}, reserved: map[string]bool{}, created: map[string]computers.Settings{}}
}

func (e *fakeEnv) addComputer(t *testing.T, name string, running bool, set computers.Settings) {
	t.Helper()
	e.defs[name] = computers.Definition{Name: name, Image: computers.ImageRef{Ref: set.Image}, Limits: computers.Limits{MemoryBytes: set.MemoryBytes},
		Network: computers.Network{Kind: computers.NetworkIsolated, Connection: set.VPNConnectionID}, Volumes: set.Volumes, DockerAccess: set.DockerAccess, Revision: 1}
	if set.VPNConnectionID != "" {
		d := e.defs[name]
		d.Network.Kind = computers.NetworkVPN
		e.defs[name] = d
	}
	e.running[name] = running
	if err := os.MkdirAll(e.layout.HomeOf(name), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e *fakeEnv) addVolume(t *testing.T, name string) {
	t.Helper()
	e.vols[name] = computers.Volume{Name: name, Kind: computers.VolumeLocal, Created: time.Now()}
	if err := os.MkdirAll(e.layout.VolumeOf(name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// addHostVolume registers a volume on a host folder, made at path.
func (e *fakeEnv) addHostVolume(t *testing.T, name, path string) {
	t.Helper()
	e.vols[name] = computers.Volume{Name: name, Kind: computers.VolumeHost, HostPath: path, Created: time.Now()}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func (e *fakeEnv) record(s string) { e.mu.Lock(); e.log = append(e.log, s); e.mu.Unlock() }

func (e *fakeEnv) events() string { e.mu.Lock(); defer e.mu.Unlock(); return strings.Join(e.log, ",") }

func (e *fakeEnv) Layout() config.Layout { return e.layout }
func (e *fakeEnv) Definition(name string) (computers.Definition, bool) {
	d, ok := e.defs[name]
	return d, ok
}
func (e *fakeEnv) Volume(name string) (computers.Volume, bool) {
	v, ok := e.vols[name]
	return v, ok
}
func (e *fakeEnv) VolumeFolder(v computers.Volume) string {
	if v.Kind == computers.VolumeHost {
		return v.HostPath
	}
	return e.layout.VolumeOf(v.Name)
}
func (e *fakeEnv) VolumeUsers(name string) []computers.VolumeUser {
	var out []computers.VolumeUser
	for _, d := range e.defs {
		for _, g := range d.Volumes {
			if g.Name == name {
				out = append(out, computers.VolumeUser{Computer: d.Name, ReadOnly: g.ReadOnly})
			}
		}
	}
	return out
}
func (e *fakeEnv) Get(_ context.Context, name string) (computers.Computer, error) {
	if _, ok := e.defs[name]; !ok {
		return computers.Computer{}, computers.ErrNotFound
	}
	state := "stopped"
	if e.running[name] {
		state = "running"
	}
	return computers.Computer{Name: name, State: state}, nil
}
func (e *fakeEnv) Reserve(name, state string) (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.reserved[name] {
		return nil, computers.ErrBusy
	}
	e.reserved[name] = true
	e.log = append(e.log, "reserve "+name)
	return func() { e.mu.Lock(); delete(e.reserved, name); e.mu.Unlock() }, nil
}
func (e *fakeEnv) Stop(_ context.Context, name string) error {
	e.record("stop " + name)
	e.running[name] = false
	return nil
}
func (e *fakeEnv) Start(_ context.Context, name string) error {
	e.record("start " + name)
	e.running[name] = true
	return nil
}
func (e *fakeEnv) Create(_ context.Context, name string, set computers.Settings, _ func(string, float64)) (computers.Computer, error) {
	if _, err := os.Stat(e.layout.HomeOf(name)); err != nil {
		return computers.Computer{}, fmt.Errorf("no home: %w", err)
	}
	e.record("create " + name)
	e.created[name] = set
	e.defs[name] = computers.Definition{Name: name}
	e.running[name] = true
	return computers.Computer{Name: name}, nil
}
func (e *fakeEnv) CreateVolume(_ context.Context, name, hostPath string) (computers.Volume, bool, error) {
	e.record("create volume " + name)
	v := computers.Volume{Name: name, Kind: computers.VolumeLocal}
	e.vols[name] = v
	return v, true, nil
}

// testService is a storage service on a fake runner and computers, with
// one SFTP remote "nas" and repository "home" on it.
func testService(t *testing.T, f *fakeRunner) (*Service, *fakeEnv) {
	t.Helper()
	env := newFakeEnv(t)
	s, err := Open(env.layout, "0123456789abcdef0123456789abcdef", f, env, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	s.Loc = time.UTC
	v := validRemotes()[SFTP]
	if err := s.Remotes.Add(v.r, v.sec); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.AddRepository(Repository{Name: "home", Remote: "nas", Folder: DefaultFolder, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.keyDir("home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.keyDir("home")+"/restic-password", []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return s, env
}

// resticReply answers restic commands like a working repository would.
func resticReply(c Call) (string, Result, error) {
	switch c.Args[1] {
	case "backup":
		return `{"message_type":"summary","snapshot_id":"snap1"}` + "\n", Result{}, nil
	case "snapshots":
		return "[]", Result{}, nil
	}
	return "", Result{}, nil
}

func argsOf(f *fakeRunner) []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c.Args[:2], " "))
	}
	return out
}
