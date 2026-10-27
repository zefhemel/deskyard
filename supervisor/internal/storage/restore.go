package storage

import (
	"bytes"
	"context"
	"encoding/json"
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

// helper runs a plain command as root in a helper, with mounts.
func (s *Service) helper(ctx context.Context, what string, mounts []engine.Mount, args ...string) error {
	res, err := s.Tool.Run.Run(ctx, Call{Mounts: mounts, Args: args})
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if res.Code != 0 {
		return failure(what, res)
	}
	return nil
}

// itemFolder is the host folder holding the item's files.
func (s *Service) itemFolder(it Item) (string, error) {
	if it.Kind == KindEnvironment {
		if _, ok := s.Env.Definition(it.Name); !ok {
			return "", fmt.Errorf("environment %s: %w", it.Name, ErrNotFound)
		}
		return s.Env.Layout().HomeOf(it.Name), nil
	}
	v, ok := s.Env.Volume(it.Name)
	if !ok {
		return "", fmt.Errorf("volume %s: %w", it.Name, ErrNotFound)
	}
	return s.Env.VolumeFolder(v), nil
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
	release, err := s.Hold(it, "restoring")
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
	var envs []string
	if it.Kind == KindEnvironment {
		envs = []string{it.Name}
	} else {
		for _, u := range s.Env.VolumeUsers(it.Name) {
			envs = append(envs, u.Environment)
		}
	}
	// After any backup that holds the lock, so nothing sits stopped waiting.
	s.opMu.Lock()
	defer s.opMu.Unlock()
	// Every one is reserved before any is stopped, so a busy one stops none.
	for _, name := range envs {
		release, err := s.Env.Reserve(name, "restoring")
		if err != nil {
			return fmt.Errorf("%s is busy: %w", name, err)
		}
		defer release()
	}
	var restart []string
	startAgain := func() {
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
	defer s.useRemote(repo.Remote)()
	progress("Restoring", 0)
	if err := s.Tool.Restore(ctx, repo, snap.ID, itemRoot(it), engine.Mount{Source: folder, MustExist: true}, nil, true, func(frac float64, msg string) {
		progress("Restoring: "+msg, frac)
	}); err != nil {
		if len(restart) > 0 {
			return fmt.Errorf("%w; %s left stopped, as its files may be half restored", err, strings.Join(restart, ", "))
		}
		return err
	}
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
	layout := s.Env.Layout()
	var folder string
	if it.Kind == KindEnvironment {
		if err := environments.ValidNewName(name); err != nil {
			return RestoreResult{}, err
		}
		if _, ok := s.Env.Definition(name); ok {
			return RestoreResult{}, fmt.Errorf("environment %q: %w", name, ErrExists)
		}
		folder = layout.HomeOf(name)
	} else {
		if err := environments.ValidName(name); err != nil {
			return RestoreResult{}, err
		}
		if _, ok := s.Env.Volume(name); ok {
			return RestoreResult{}, fmt.Errorf("volume %q: %w", name, ErrExists)
		}
		folder = layout.VolumeOf(name)
	}
	if _, err := os.Lstat(folder); err == nil {
		return RestoreResult{}, fmt.Errorf("%w: %s is in the way; remove it or choose another name", ErrExists, folder)
	}
	newItem := Item{it.Kind, name}
	release, err := s.Hold(newItem, "restoring")
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	if it.Kind == KindEnvironment {
		// As the create API does, so reconcile and other operations leave the
		// name alone while its home is restored and it is created.
		releaseName, err := s.Env.Reserve(name, "creating")
		if err != nil {
			return RestoreResult{}, fmt.Errorf("%s is busy: %w", name, err)
		}
		defer releaseName()
		if _, ok := s.Env.Definition(name); ok {
			return RestoreResult{}, fmt.Errorf("environment %q: %w", name, ErrExists)
		}
		if s.ForgetName != nil {
			if err := s.ForgetName(name); err != nil {
				return RestoreResult{}, err
			}
		}
	}
	var def environments.Definition
	if it.Kind == KindEnvironment {
		var buf bytes.Buffer
		if err := s.Tool.Dump(ctx, repo, snap.ID, path.Join("/deskyard/environment", it.Name, "definition.json"), false, &buf); err != nil {
			return RestoreResult{}, err
		}
		if err := json.Unmarshal(buf.Bytes(), &def); err != nil {
			return RestoreResult{}, fmt.Errorf("reading the saved settings: %w", err)
		}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	defer s.useRemote(repo.Remote)()
	if err := os.Mkdir(folder, 0o755); err != nil {
		return RestoreResult{}, err
	}
	removeFolder := func() {
		_ = s.helper(context.WithoutCancel(ctx), "removing "+folder, []engine.Mount{{Source: filepath.Dir(folder), Target: "/parent"}}, "rm", "-rf", "/parent/"+filepath.Base(folder))
		_ = os.RemoveAll(folder)
	}
	progress("Restoring", 0)
	target := engine.Mount{Source: folder, MustExist: true}
	if err := s.Tool.Restore(ctx, repo, snap.ID, itemRoot(it), target, nil, false, func(frac float64, msg string) {
		progress("Restoring: "+msg, frac)
	}); err != nil {
		removeFolder()
		return RestoreResult{}, err
	}
	// The folder itself is the supervisor's; the files keep their owner.
	if err := s.helper(ctx, "setting the folder's owner", []engine.Mount{{Source: folder, Target: "/target", MustExist: true}}, "chown", "1000:1000", "/target"); err != nil {
		removeFolder()
		return RestoreResult{}, err
	}
	res := RestoreResult{Created: name}
	if it.Kind == KindVolume {
		if _, _, err := s.Env.CreateVolume(ctx, name, ""); err != nil {
			removeFolder()
			return RestoreResult{}, err
		}
		return res, nil
	}
	set, dropped := s.settingsFrom(def, req.NewImage)
	res.Dropped = dropped
	if _, err := s.Env.Create(ctx, name, set, progress); err != nil {
		removeFolder()
		return RestoreResult{}, err
	}
	if err := s.Env.Stop(ctx, name); err != nil {
		return res, fmt.Errorf("created %s, but stopping it failed: %w", name, err)
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
	release, err := s.Hold(it, "restoring files")
	if err != nil {
		return RestoreResult{}, err
	}
	defer release()
	base := "Restored " + snap.Time.In(s.Loc).Format("2006-01-02 1504")
	name := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(filepath.Join(folder, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s (%d)", base, n)
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	defer s.useRemote(repo.Remote)()
	if err := s.helper(ctx, "making "+name, []engine.Mount{{Source: folder, Target: "/item", MustExist: true}}, "install", "-d", "-o", "1000", "-g", "1000", "-m", "0755", "/item/"+name); err != nil {
		return RestoreResult{}, err
	}
	progress("Restoring", 0)
	if err := s.Tool.Restore(ctx, repo, snap.ID, itemRoot(it), engine.Mount{Source: filepath.Join(folder, name), MustExist: true}, includes, false, func(frac float64, msg string) {
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
	root := itemRoot(it)
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
	return s.Tool.Dump(ctx, repo, snap.ID, path.Join(itemRoot(it), clean), dir, open(name))
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
