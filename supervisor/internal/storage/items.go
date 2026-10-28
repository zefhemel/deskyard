package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// kind is what backups do differently for each kind of item: where its
// files are, here and in its snapshots, and how a restore makes a new one.
// A snapshot of an item holds it below Root, /deskyard/<kind>/<name>.
type kind interface {
	// exists reports whether the item name is here.
	exists(env Environments, name string) bool
	// folder is the folder holding the files of the item name, which exists.
	folder(env Environments, name string) (environments.Folder, error)
	// parent is the folder holding a new item's folder, named after it.
	parent(env Environments) environments.Folder
	// validNew checks name as a new item's.
	validNew(name string) error
	// files is where, below root, its snapshots hold the item's files.
	files(root string) string
	// sources are the mounts that put the files of the item name at root in
	// a helper, read-only, and what to clean up after.
	sources(s *Service, name, root string) ([]engine.Mount, func(), error)
	// users are the environments that have the item's files open.
	users(env Environments, name string) []string
	// restoreAs prepares a restore of snap of from as the new item name:
	// what it holds until the restore ends (release), and create, which
	// makes the item of its restored folder.
	restoreAs(ctx context.Context, s *Service, repo Repo, snap Snapshot, from Item, name string, req RestoreRequest) (create func(context.Context, func(string, float64)) (RestoreResult, error), release func(), err error)
	// environment is the environment that work on the item name shows on.
	environment(name string) string
}

var kinds = map[string]kind{KindEnvironment: environmentKind{}, KindVolume: volumeKind{}}

func (i Item) kind() kind { return kinds[i.Kind] }

// Root is where the item is in its snapshots.
func (i Item) Root() string { return path.Join("/deskyard", i.Kind, i.Name) }

// files is where the item's files are in its snapshots.
func (i Item) files() string { return i.kind().files(i.Root()) }

// Environment is the environment that work on the item shows on, if any.
func (i Item) Environment() string { return i.kind().environment(i.Name) }

// environmentKind: an environment's home, with its definition beside it in
// snapshots.
type environmentKind struct{}

func (environmentKind) exists(env Environments, name string) bool {
	_, ok := env.Definition(name)
	return ok
}

func (environmentKind) folder(env Environments, name string) (environments.Folder, error) {
	if _, ok := env.Definition(name); !ok {
		return environments.Folder{}, fmt.Errorf("environment %s: %w", name, ErrNotFound)
	}
	return environments.Folder{Path: env.Layout().HomeOf(name)}, nil
}

func (environmentKind) parent(env Environments) environments.Folder {
	return environments.Folder{Path: env.Layout().Home}
}

func (environmentKind) validNew(name string) error { return environments.ValidNewName(name) }

func (environmentKind) files(root string) string { return path.Join(root, "home") }

func (environmentKind) sources(s *Service, name, root string) ([]engine.Mount, func(), error) {
	def, ok := s.Env.Definition(name)
	if !ok {
		return nil, func() {}, fmt.Errorf("environment %s doesn't exist", name)
	}
	b, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return nil, func() {}, err
	}
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	file := filepath.Join(s.layout.State, "backups", "definition-"+hex.EncodeToString(raw)+".json")
	if err := store.WriteFile(file, append(b, '\n')); err != nil {
		return nil, func() {}, err
	}
	return []engine.Mount{
		{Source: s.Env.Layout().HomeOf(name), Target: path.Join(root, "home"), ReadOnly: true, MustExist: true},
		{Source: file, Target: path.Join(root, "definition.json"), ReadOnly: true, MustExist: true},
	}, func() { os.Remove(file) }, nil
}

func (environmentKind) users(_ Environments, name string) []string { return []string{name} }

// restoreAs claims the name, as the create API does, so that reconcile and
// other operations leave it alone while its home is restored, and what a
// removal of the name failed to purge doesn't carry over. The new
// environment takes over what it can of the snapshot's definition, and is
// created stopped: the restored desktop never runs before it is asked to.
func (environmentKind) restoreAs(ctx context.Context, s *Service, repo Repo, snap Snapshot, from Item, name string, req RestoreRequest) (func(context.Context, func(string, float64)) (RestoreResult, error), func(), error) {
	release, err := s.Env.ClaimName(name)
	if errors.Is(err, environments.ErrExists) {
		return nil, nil, fmt.Errorf("environment %q: %w", name, ErrExists)
	} else if err != nil {
		return nil, nil, fmt.Errorf("%s is busy: %w", name, err)
	}
	var buf bytes.Buffer
	var def environments.Definition
	if err := s.Tool.Dump(ctx, repo, snap.ID, path.Join(from.Root(), "definition.json"), false, &buf); err != nil {
		release()
		return nil, nil, err
	}
	if err := json.Unmarshal(buf.Bytes(), &def); err != nil {
		release()
		return nil, nil, fmt.Errorf("reading the saved settings: %w", err)
	}
	return func(ctx context.Context, progress func(string, float64)) (RestoreResult, error) {
		set, dropped := s.settingsFrom(def, req.NewImage)
		if _, err := s.Env.CreateStopped(ctx, name, set, progress); err != nil {
			return RestoreResult{}, err
		}
		return RestoreResult{Created: name, Dropped: dropped}, nil
	}, release, nil
}

func (environmentKind) environment(name string) string { return name }

// volumeKind: a volume's folder, local or on the engine host.
type volumeKind struct{}

func (volumeKind) exists(env Environments, name string) bool {
	_, ok := env.Volume(name)
	return ok
}

func (volumeKind) folder(env Environments, name string) (environments.Folder, error) {
	v, ok := env.Volume(name)
	if !ok {
		return environments.Folder{}, fmt.Errorf("volume %s: %w", name, ErrNotFound)
	}
	return env.VolumeFolder(v), nil
}

func (volumeKind) parent(env Environments) environments.Folder {
	return environments.Folder{Path: env.Layout().Volume}
}

func (volumeKind) validNew(name string) error { return environments.ValidName(name) }

func (volumeKind) files(root string) string { return root }

func (volumeKind) sources(s *Service, name, root string) ([]engine.Mount, func(), error) {
	v, ok := s.Env.Volume(name)
	if !ok {
		return nil, func() {}, fmt.Errorf("volume %s doesn't exist", name)
	}
	return []engine.Mount{{Source: s.Env.VolumeFolder(v).Path, Target: root, ReadOnly: true, MustExist: true}}, func() {}, nil
}

func (volumeKind) users(env Environments, name string) []string {
	var out []string
	for _, u := range env.VolumeUsers(name) {
		out = append(out, u.Environment)
	}
	return out
}

// restoreAs makes a local volume of the restored folder, which it adopts.
func (volumeKind) restoreAs(_ context.Context, s *Service, _ Repo, _ Snapshot, _ Item, name string, _ RestoreRequest) (func(context.Context, func(string, float64)) (RestoreResult, error), func(), error) {
	return func(ctx context.Context, _ func(string, float64)) (RestoreResult, error) {
		if _, _, err := s.Env.CreateHeldVolume(ctx, name); err != nil {
			return RestoreResult{}, err
		}
		return RestoreResult{Created: name}, nil
	}, func() {}, nil
}

func (volumeKind) environment(string) string { return "" }
