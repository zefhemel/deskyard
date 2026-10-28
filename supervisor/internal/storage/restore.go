package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// RestoreRequest is a restore of a snapshot: here (replace the item's
// files), new (a new environment or volume named NewName) or folder (Paths,
// relative to the item, into a new "Restored …" folder inside the item).
type RestoreRequest struct {
	Repository  string   `json:"repository"` // default: the item's policy's
	Snapshot    string   `json:"snapshot"`
	Mode        string   `json:"mode"`
	NewName     string   `json:"newName,omitempty"`
	NewImage    string   `json:"newImage,omitempty"`
	BackupFirst bool     `json:"backupFirst,omitempty"`
	Paths       []string `json:"paths,omitempty"`
}

// RestoreResult says what a restore made: a new item's name and the
// settings it couldn't take over, or the folder files were restored into.
type RestoreResult struct {
	Created string   `json:"created,omitempty"`
	Dropped []string `json:"dropped,omitempty"`
	Folder  string   `json:"folder,omitempty"`
}

// cleanSnapshotPath checks a path relative to an item's root, as the browser
// sends it, and returns it clean ("" for the root).
func cleanSnapshotPath(p string) (string, error) {
	if strings.ContainsAny(p, "\x00\r\n") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("invalid path %q", p)
	}
	clean := path.Clean("/" + p)
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid path %q", p)
		}
	}
	return strings.TrimPrefix(clean, "/"), nil
}

// repoFor is the repository a request names, or else the item's policy's.
func (s *Service) repoFor(it Item, name string) (string, error) {
	if name != "" {
		return name, nil
	}
	if p, ok := s.Store.Policy(it); ok {
		return p.Repository, nil
	}
	return "", fmt.Errorf("say which repository %s's snapshot is in", it)
}

// itemFolder is the folder holding the item's files.
func (s *Service) itemFolder(it Item) (environments.Folder, error) {
	return it.kind().folder(s.Env, it.Name)
}

// Restore restores a snapshot of it as req says.
func (s *Service) Restore(ctx context.Context, it Item, req RestoreRequest, progress func(string, float64)) (RestoreResult, error) {
	if progress == nil {
		progress = func(string, float64) {}
	}
	repoName, err := s.repoFor(it, req.Repository)
	if err != nil {
		return RestoreResult{}, err
	}
	repo, _, err := s.repo(repoName)
	if err != nil {
		return RestoreResult{}, err
	}
	snap, err := s.snapshotOf(ctx, repoName, it, req.Snapshot)
	if err != nil {
		return RestoreResult{}, err
	}
	switch req.Mode {
	case "here":
		return RestoreResult{}, s.restoreHere(ctx, it, repo, snap, req.BackupFirst, progress)
	case "new":
		return s.restoreNew(ctx, it, repo, snap, req, progress)
	case "folder":
		return s.restoreFolder(ctx, it, repo, snap, req.Paths, progress)
	}
	return RestoreResult{}, fmt.Errorf("unknown restore mode %q", req.Mode)
}

func (s *Service) restoreHere(ctx context.Context, it Item, repo Repo, snap Snapshot, backupFirst bool, progress func(string, float64)) error {
	folder, err := s.itemFolder(it)
	if err != nil {
		return err
	}
	// Its files are replaced: no environment is granted them or starts with
	// them until the restore is done.
	release, err := s.hold(it, "restoring", true)
	if err != nil {
		return err
	}
	defer release()
	if backupFirst {
		if err := s.backUp(ctx, it, ReasonPreRestore, progress); err != nil {
			return fmt.Errorf("backing up the current files first: %w", err)
		}
	}
	// The environments that have the files open: stopped, and started again
	// afterwards when they were running.
	envs := it.kind().users(s.Env, it.Name)
	// After any backup that holds the lock, so nothing sits stopped waiting.
	s.opMu.Lock()
	defer s.opMu.Unlock()
	done, err := s.useRemote(repo.Remote)
	if err != nil {
		return err
	}
	defer done()
	// Every one is reserved before any is stopped, so a busy one stops none.
	for _, name := range envs {
		release, err := s.Env.Reserve(name, "restoring")
		if err != nil {
			return fmt.Errorf("%s is busy: %w", name, err)
		}
		defer release()
	}
	var restart []string
	// The files are let go of first: environments using them don't start
	// while they are held.
	startAgain := func() {
		release()
		for _, name := range restart {
			_ = s.Env.Start(context.WithoutCancel(ctx), name)
		}
	}
	for _, name := range envs {
		c, err := s.Env.Get(ctx, name)
		if err == nil && c.Running() {
			progress("Stopping "+name, -1)
			if err = s.Env.Stop(ctx, name); err == nil {
				restart = append(restart, name)
				continue
			}
		}
		if err != nil {
			startAgain() // nothing is restored yet
			return fmt.Errorf("stopping %s: %w", name, err)
		}
	}
	progress("Restoring", 0)
	if err := s.Tool.Restore(ctx, repo, snap.ID, it.files(), engine.Mount{Source: folder.Path, MustExist: true}, nil, true, func(frac float64, msg string) {
		progress("Restoring: "+msg, frac)
	}); err != nil {
		if len(restart) > 0 {
			return fmt.Errorf("%w; %s left stopped, as its files may be half restored", err, strings.Join(restart, ", "))
		}
		return err
	}
	release()
	for _, name := range restart {
		progress("Starting "+name, -1)
		if err := s.Env.Start(ctx, name); err != nil {
			return fmt.Errorf("the files are restored, but starting %s failed: %w", name, err)
		}
	}
	return nil
}

func (s *Service) restoreNew(ctx context.Context, it Item, repo Repo, snap Snapshot, req RestoreRequest, progress func(string, float64)) (RestoreResult, error) {
	name := req.NewName
	k := it.kind()
	if err := k.validNew(name); err != nil {
		return RestoreResult{}, err
	}
	if k.exists(s.Env, name) {
		return RestoreResult{}, fmt.Errorf("%s %q: %w", it.Kind, name, ErrExists)
	}
	parent := k.parent(s.Env)
	folder := filepath.Join(parent.Path, name)
	if _, err := os.Lstat(folder); err == nil {
		return RestoreResult{}, fmt.Errorf("%w: %s is in the way; remove it or choose another name", ErrExists, folder)
	}
	release, err := s.hold(Item{it.Kind, name}, "restoring", true)
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	create, releaseNew, err := k.restoreAs(ctx, s, repo, snap, it, name, req)
	if err != nil {
		return RestoreResult{}, err
	}
	defer releaseNew()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	done, err := s.useRemote(repo.Remote)
	if err != nil {
		return RestoreResult{}, err
	}
	defer done()
	if err := os.Mkdir(folder, 0o755); err != nil {
		return RestoreResult{}, err
	}
	removeFolder := func() { _ = s.Env.RemoveTree(context.WithoutCancel(ctx), parent, name) }
	progress("Restoring", 0)
	if err := s.Tool.Restore(ctx, repo, snap.ID, it.files(), engine.Mount{Source: folder, MustExist: true}, nil, false, func(frac float64, msg string) {
		progress("Restoring: "+msg, frac)
	}); err != nil {
		removeFolder()
		return RestoreResult{}, err
	}
	res, err := create(ctx, progress)
	if err != nil {
		removeFolder()
		return RestoreResult{}, err
	}
	return res, nil
}

// settingsFrom is the settings a new environment takes over from a saved
// definition: image (or image) and resources. What belongs to this
// machine's other resources is dropped and named.
func (s *Service) settingsFrom(def environments.Definition, image string) (environments.Settings, []string) {
	set := environments.Settings{Image: def.Image.Ref, MemoryBytes: def.Limits.MemoryBytes, NanoCPUs: def.Limits.NanoCPUs, Volumes: []environments.VolumeGrant{}}
	if image != "" {
		set.Image = image
	}
	var dropped []string
	if def.Network.Kind == environments.NetworkVPN || def.Network.Connection != "" {
		dropped = append(dropped, "the VPN connection and its LAN exceptions")
	}
	if def.DockerAccess {
		dropped = append(dropped, "Docker access")
	}
	for _, g := range def.Volumes {
		if _, ok := s.Env.Volume(g.Name); ok {
			set.Volumes = append(set.Volumes, g)
		} else {
			dropped = append(dropped, fmt.Sprintf("volume %s, which doesn't exist here", g.Name))
		}
	}
	return set, dropped
}

func (s *Service) restoreFolder(ctx context.Context, it Item, repo Repo, snap Snapshot, paths []string, progress func(string, float64)) (RestoreResult, error) {
	if len(paths) == 0 {
		return RestoreResult{}, errors.New("choose the files or folders to restore")
	}
	var includes []string
	for _, p := range paths {
		clean, err := cleanSnapshotPath(p)
		if err != nil {
			return RestoreResult{}, err
		}
		if clean == "" {
			return RestoreResult{}, errors.New("restore everything as new, or here, rather than to a folder")
		}
		includes = append(includes, "/"+clean)
	}
	folder, err := s.itemFolder(it)
	if err != nil {
		return RestoreResult{}, err
	}
	release, err := s.hold(it, "restoring files", false)
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	done, err := s.useRemote(repo.Remote)
	if err != nil {
		return RestoreResult{}, err
	}
	defer done()
	// The first free name, taken where it is made: on a host volume, whose
	// folder the supervisor can't see, by the root helper.
	name, err := s.Env.MakeFreeFolder(ctx, folder, "Restored "+snap.Time.In(s.Loc).Format("2006-01-02 1504"))
	if err != nil {
		return RestoreResult{}, err
	}
	progress("Restoring", 0)
	if err := s.Tool.Restore(ctx, repo, snap.ID, it.files(), engine.Mount{Source: filepath.Join(folder.Path, name), MustExist: true}, includes, false, func(frac float64, msg string) {
		progress("Restoring: "+msg, frac)
	}); err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Folder: name}, nil
}

// Browse lists dir (relative to the item) in a snapshot of it. Paths in the
// entries are relative to the item too.
func (s *Service) Browse(ctx context.Context, it Item, repoName, snapshot, dir string) ([]Entry, error) {
	clean, err := cleanSnapshotPath(dir)
	if err != nil {
		return nil, err
	}
	repoName, err = s.repoFor(it, repoName)
	if err != nil {
		return nil, err
	}
	repo, _, err := s.repo(repoName)
	if err != nil {
		return nil, err
	}
	snap, err := s.snapshotOf(ctx, repoName, it, snapshot)
	if err != nil {
		return nil, err
	}
	root := it.files()
	entries, err := s.Tool.Ls(ctx, repo, snap.ID, path.Join(root, clean))
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Path = strings.TrimPrefix(strings.TrimPrefix(entries[i].Path, root), "/")
	}
	return entries, nil
}

// Download writes p (relative to the item) of a snapshot to the writer
// open returns for the file name to offer: a file as itself, a folder (dir)
// as a zip. open is called once the download can start.
func (s *Service) Download(ctx context.Context, it Item, repoName, snapshot, p string, dir bool, open func(name string) io.Writer) error {
	clean, err := cleanSnapshotPath(p)
	if err != nil {
		return err
	}
	repoName, err = s.repoFor(it, repoName)
	if err != nil {
		return err
	}
	repo, _, err := s.repo(repoName)
	if err != nil {
		return err
	}
	snap, err := s.snapshotOf(ctx, repoName, it, snapshot)
	if err != nil {
		return err
	}
	name := path.Base(clean)
	if clean == "" {
		name = it.Name + "-" + snap.Time.In(s.Loc).Format("2006-01-02")
		dir = true
	}
	if dir {
		name += ".zip"
	}
	return s.Tool.Dump(ctx, repo, snap.ID, path.Join(it.files(), clean), dir, open(name))
}

// CheckRestorePaths checks the paths of a restore to a folder, as the
// browser sends them.
func CheckRestorePaths(paths []string) error {
	for _, p := range paths {
		if _, err := cleanSnapshotPath(p); err != nil {
			return err
		}
	}
	return nil
}
