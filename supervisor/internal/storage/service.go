package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

var (
	// ErrBusy: another backup, restore or removal holds the item's files,
	// or a remote is in use or being changed.
	ErrBusy = environments.ErrItemBusy
	// ErrNeedsKey: a repository is already there; connecting needs its key.
	ErrNeedsKey = errors.New("a backup repository is already there: enter its recovery key to connect it")
)

// Reasons a snapshot was taken, kept as a restic tag.
const (
	ReasonScheduled  = "scheduled"
	ReasonManual     = "manual"
	ReasonPreRestore = "pre-restore"
)

const (
	pruneEvery   = 7 * 24 * time.Hour
	listingFresh = 30 * time.Second
)

// Service runs storage remotes and backups.
type Service struct {
	Remotes *Remotes
	Store   *Store
	State   *State
	Tool    Tool
	Env     Environments
	Loc     *time.Location
	Now     func() time.Time
	Log     *slog.Logger

	layout config.Layout
	opMu   sync.Mutex // one backup or restore at a time

	mu       sync.Mutex
	remoteOp map[string]int // remote → operations using it; -1 while it is changed
	listings map[string]listing
}

type listing struct {
	at    time.Time
	snaps []Snapshot
}

// Open loads the remotes, repositories, policies and run state from layout,
// for the supervisor instance.
func Open(layout config.Layout, instance string, run Runner, env Environments, log *slog.Logger) (*Service, error) {
	remotes, err := LoadRemotes(filepath.Join(layout.Data, "storage-remotes.json"), layout.Secret)
	if err != nil {
		return nil, err
	}
	st, err := LoadStore(filepath.Join(layout.Data, "backups.json"))
	if err != nil {
		return nil, err
	}
	state, err := LoadState(filepath.Join(layout.State, "backups.json"))
	if err != nil {
		return nil, err
	}
	host := instance
	if len(host) > 12 {
		host = host[:12]
	}
	return &Service{
		Remotes: remotes, Store: st, State: state, Env: env, Loc: time.Local, Now: time.Now, Log: log,
		Tool:   Tool{Run: run, Host: "deskyard-" + host, CacheDir: filepath.Join(layout.Cache, "restic")},
		layout: layout, remoteOp: map[string]int{}, listings: map[string]listing{},
	}, nil
}

func (s *Service) keyDir(repo string) string {
	return filepath.Join(s.layout.Secret, "backups", repo)
}

// repo is where restic finds the repository name.
func (s *Service) repo(name string) (Repo, Repository, error) {
	r, ok := s.Store.Repository(name)
	if !ok {
		return Repo{}, Repository{}, fmt.Errorf("repository %q: %w", name, ErrNotFound)
	}
	remote, ok := s.Remotes.Get(r.Remote)
	if !ok {
		return Repo{}, Repository{}, fmt.Errorf("repository %s's remote %q: %w", name, r.Remote, ErrNotFound)
	}
	return Repo{Remote: remote.Name, Path: joinPath(remote.Root, r.Folder), ConfigDir: s.Remotes.ConfigDir(remote.Name), KeyDir: s.keyDir(name)}, r, nil
}

// hold holds it's files for what, through the environments service, which
// owns every reservation of environments and volumes; replacing says the
// files are being replaced, so nothing may mount them meanwhile.
func (s *Service) hold(it Item, what string, replacing bool) (release func(), err error) {
	return s.Env.HoldFiles(environments.Item(it), what, replacing)
}

// Activity is what holds it's files ("" when nothing does).
func (s *Service) Activity(it Item) string {
	return s.Env.FilesActivity(environments.Item(it))
}

// useRemote counts an operation on the remote until done; ErrBusy while the
// remote is being changed.
func (s *Service) useRemote(remote string) (done func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteOp[remote] < 0 {
		return nil, fmt.Errorf("%w: remote %s is being changed", ErrBusy, remote)
	}
	s.remoteOp[remote]++
	return sync.OnceFunc(func() {
		s.mu.Lock()
		s.remoteOp[remote]--
		s.mu.Unlock()
	}), nil
}

// changeRemote holds the remote for a change until done, so that no
// operation starts on it meanwhile; ErrBusy while one uses it.
func (s *Service) changeRemote(remote string) (done func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteOp[remote] != 0 {
		return nil, fmt.Errorf("%w on remote %s", ErrBusy, remote)
	}
	s.remoteOp[remote] = -1
	return sync.OnceFunc(func() {
		s.mu.Lock()
		delete(s.remoteOp, remote)
		s.mu.Unlock()
	}), nil
}

func (s *Service) forgetListings(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listings, repo)
}

// --- remotes ---

// TestRemote connects to an unsaved remote with its secrets.
func (s *Service) TestRemote(ctx context.Context, r Remote, sec Secrets) error {
	if err := r.Validate(sec, true); err != nil {
		return err
	}
	return s.testWith(ctx, r, sec.stored(StoredSecrets{}))
}

func (s *Service) testWith(ctx context.Context, r Remote, sec StoredSecrets) error {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	dir := filepath.Join(s.layout.Secret, "remotes", ".test-"+hex.EncodeToString(raw))
	defer os.RemoveAll(dir)
	if err := WriteConfigDir(dir, r, sec); err != nil {
		return err
	}
	return s.Tool.TestRemote(ctx, dir, r.Name, r.Root)
}

// TestSavedRemote tests name as changed to r, with sec over its stored secrets.
func (s *Service) TestSavedRemote(ctx context.Context, name string, r Remote, sec Secrets) error {
	stored, err := s.Remotes.StoredSecrets(name)
	if err != nil {
		return err
	}
	r.Name = name
	if err := r.Validate(sec, false); err != nil {
		return err
	}
	return s.testWith(ctx, r, sec.stored(stored))
}

// CreateRemote saves a new remote after a successful connection test.
func (s *Service) CreateRemote(ctx context.Context, r Remote, sec Secrets) error {
	if _, ok := s.Remotes.Get(r.Name); ok {
		return fmt.Errorf("remote %q: %w", r.Name, ErrExists)
	}
	if err := s.TestRemote(ctx, r, sec); err != nil {
		return err
	}
	r.Created = s.Now().UTC()
	return s.Remotes.Add(r, sec)
}

// UpdateRemote saves changes to name after a successful connection test,
// refusing while a backup or restore uses it. None starts on it from the
// test until the change is saved.
func (s *Service) UpdateRemote(ctx context.Context, name string, r Remote, sec Secrets) error {
	done, err := s.changeRemote(name)
	if err != nil {
		return err
	}
	defer done()
	if err := s.TestSavedRemote(ctx, name, r, sec); err != nil {
		return err
	}
	r.Name = name
	return s.Remotes.Update(name, r, sec)
}

// DeleteRemote forgets name, refusing while a repository is on it, or a
// backup or restore uses it.
func (s *Service) DeleteRemote(name string) error {
	done, err := s.changeRemote(name)
	if err != nil {
		return err
	}
	defer done()
	if repos := s.Store.RepositoriesOnRemote(name); len(repos) > 0 {
		return fmt.Errorf("%w: backup repositories %s are on remote %s", ErrInUse, strings.Join(repos, ", "), name)
	}
	return s.Remotes.Delete(name)
}

// --- repositories ---

// CreateRepository adds the repository name in folder on remote. Without a
// key it initialises a new repository and returns its generated recovery
// key; a repository already there needs its key (ErrNeedsKey), which is
// verified and connects it, returning "".
func (s *Service) CreateRepository(ctx context.Context, name, remote, folder, key string) (string, error) {
	if folder == "" {
		folder = DefaultFolder
	}
	repo := Repository{Name: name, Remote: remote, Folder: strings.Trim(folder, "/"), Created: s.Now().UTC()}
	if err := repo.validate(); err != nil {
		return "", err
	}
	if _, ok := s.Store.Repository(name); ok {
		return "", fmt.Errorf("repository %q: %w", name, ErrExists)
	}
	rem, ok := s.Remotes.Get(remote)
	if !ok {
		return "", fmt.Errorf("remote %q: %w", remote, ErrNotFound)
	}
	generated := key == ""
	if generated {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		key = base64.StdEncoding.EncodeToString(raw)
	} else if strings.ContainsAny(key, "\r\n") {
		return "", errors.New("a recovery key is one line")
	}
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	tmp := filepath.Join(s.layout.Secret, "backups", ".new-"+hex.EncodeToString(raw))
	defer os.RemoveAll(tmp)
	if err := store.WriteFile(filepath.Join(tmp, "restic-password"), []byte(key)); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return "", err
	}
	done, err := s.useRemote(remote)
	if err != nil {
		return "", err
	}
	defer done()
	r := Repo{Remote: rem.Name, Path: joinPath(rem.Root, repo.Folder), ConfigDir: s.Remotes.ConfigDir(rem.Name), KeyDir: tmp}
	switch err := s.Tool.CheckRepo(ctx, r); {
	case err == nil && generated:
		return "", ErrNeedsKey
	case err == nil:
	case errors.Is(err, ErrNoRepo) && generated:
		if err := s.Tool.Init(ctx, r); err != nil {
			return "", err
		}
	case errors.Is(err, ErrWrongKey) && generated:
		return "", ErrNeedsKey
	default:
		return "", err
	}
	dir := s.keyDir(name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	if err := s.Store.AddRepository(repo); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if generated {
		return key, nil
	}
	return "", nil
}

func (s *Service) RecoveryKey(repo string) (string, error) {
	if _, ok := s.Store.Repository(repo); !ok {
		return "", fmt.Errorf("repository %q: %w", repo, ErrNotFound)
	}
	b, err := os.ReadFile(filepath.Join(s.keyDir(repo), "restic-password"))
	return string(b), err
}

// RemoveRepository forgets a repository and its local key; its snapshots stay
// on the remote.
func (s *Service) RemoveRepository(name string) error {
	if err := s.Store.RemoveRepository(name); err != nil {
		return err
	}
	s.forgetListings(name)
	return os.RemoveAll(s.keyDir(name))
}

// --- policies ---

// PutPolicy saves it's policy; the item must exist.
func (s *Service) PutPolicy(p Policy) error {
	if !s.exists(p.Item) {
		return fmt.Errorf("%s: %w", p.Item, ErrNotFound)
	}
	return s.Store.PutPolicy(p)
}

// ItemGone drops the policy and run state of a removed environment or a
// deleted volume. Their snapshots stay.
func (s *Service) ItemGone(it Item) error {
	return errors.Join(s.Store.DeletePolicy(it), s.State.Forget(it))
}

func (s *Service) exists(it Item) bool {
	if it.Kind == KindEnvironment {
		_, ok := s.Env.Definition(it.Name)
		return ok
	}
	_, ok := s.Env.Volume(it.Name)
	return ok
}

// --- backing up ---

// itemPaths are the item's folder in a helper, and the mounts that put its
// files there.
func (s *Service) itemSource(it Item) (root string, mounts []engine.Mount, cleanup func(), err error) {
	cleanup = func() {}
	switch it.Kind {
	case KindEnvironment:
		def, ok := s.Env.Definition(it.Name)
		if !ok {
			return "", nil, cleanup, fmt.Errorf("environment %s doesn't exist", it.Name)
		}
		b, err := json.MarshalIndent(def, "", "  ")
		if err != nil {
			return "", nil, cleanup, err
		}
		raw := make([]byte, 6)
		_, _ = rand.Read(raw)
		file := filepath.Join(s.layout.State, "backups", "definition-"+hex.EncodeToString(raw)+".json")
		if err := store.WriteFile(file, append(b, '\n')); err != nil {
			return "", nil, cleanup, err
		}
		root = "/deskyard/environment/" + it.Name
		return root, []engine.Mount{
			{Source: s.Env.Layout().HomeOf(it.Name), Target: root + "/home", ReadOnly: true, MustExist: true},
			{Source: file, Target: root + "/definition.json", ReadOnly: true, MustExist: true},
		}, func() { os.Remove(file) }, nil
	default:
		v, ok := s.Env.Volume(it.Name)
		if !ok {
			return "", nil, cleanup, fmt.Errorf("volume %s doesn't exist", it.Name)
		}
		root = "/deskyard/volume/" + it.Name
		return root, []engine.Mount{{Source: s.Env.VolumeFolder(v).Path, Target: root, ReadOnly: true, MustExist: true}}, cleanup, nil
	}
}

// BackUp backs it up now to its policy's repository, then forgets snapshots
// beyond the policy's retention and, at most weekly, prunes the repository.
// The outcome is recorded in the run state.
func (s *Service) BackUp(ctx context.Context, it Item, reason string, progress func(string, float64)) error {
	release, err := s.hold(it, "backing up", false)
	if err != nil {
		return err
	}
	defer release()
	return s.backUp(ctx, it, reason, progress)
}

// backUp is BackUp with it held.
func (s *Service) backUp(ctx context.Context, it Item, reason string, progress func(string, float64)) (err error) {
	start := s.Now().UTC()
	st := s.State.Get(it)
	defer func() {
		st.LastAttempt = start
		st.Error = ""
		if err != nil {
			st.Warnings = nil
		}
		if err != nil {
			st.Error = err.Error()
		}
		if rerr := s.State.Record(it, st); rerr != nil && s.Log != nil {
			s.Log.Error("recording backup state", "item", it, "err", rerr)
		}
	}()
	p, ok := s.Store.Policy(it)
	if !ok {
		return fmt.Errorf("%s has no backup policy", it)
	}
	repo, _, err := s.repo(p.Repository)
	if err != nil {
		return err
	}
	if progress == nil {
		progress = func(string, float64) {}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	done, err := s.useRemote(repo.Remote)
	if err != nil {
		return err
	}
	defer done()
	defer s.forgetListings(p.Repository)
	root, mounts, cleanup, err := s.itemSource(it)
	defer cleanup()
	if err != nil {
		return err
	}
	progress("Backing up "+it.String(), 0)
	id, warnings, err := s.Tool.Backup(ctx, repo, mounts, []string{root}, []string{"deskyard", it.Tag(), reason}, func(frac float64, msg string) {
		progress("Backing up: "+msg, frac)
	})
	if err != nil {
		return err
	}
	st.LastSuccess, st.Snapshot, st.Warnings = start, id, warnings
	progress("Forgetting old snapshots", -1)
	if err := s.Tool.Forget(ctx, repo, it.Tag(), p.Retention); err != nil {
		return err
	}
	if now := s.Now(); now.Sub(s.State.LastPrune(p.Repository)) >= pruneEvery {
		progress("Pruning the repository", -1)
		if err := s.Tool.Prune(ctx, repo); err != nil {
			return err
		}
		if err := s.State.RecordPrune(p.Repository, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// RunDue backs up every item whose policy is due, one after another.
func (s *Service) RunDue(ctx context.Context) {
	now := s.Now()
	for _, p := range s.Store.Policies() {
		if ctx.Err() != nil {
			return
		}
		if !Due(p, s.State.Get(p.Item), now, s.Loc) {
			continue
		}
		if !s.exists(p.Item) {
			st := s.State.Get(p.Item)
			st.LastAttempt, st.Error = now.UTC(), fmt.Sprintf("%s doesn't exist", p.Item)
			_ = s.State.Record(p.Item, st)
			continue
		}
		if err := s.BackUp(ctx, p.Item, ReasonScheduled, nil); err != nil && s.Log != nil {
			if errors.Is(err, ErrBusy) {
				continue // tried again next minute
			}
			s.Log.Warn("scheduled backup failed", "item", p.Item, "err", err)
		}
	}
}

// RunScheduler runs due backups every minute until ctx ends.
func (s *Service) RunScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// --- listing ---

// ItemSnapshot is a snapshot of an item, with its repository and why it
// was taken.
type ItemSnapshot struct {
	Snapshot
	Repository string `json:"repository"`
	Reason     string `json:"reason"`
}

// repoSnapshots lists every Deskyard snapshot in the repository, cached.
func (s *Service) repoSnapshots(ctx context.Context, name string) ([]Snapshot, error) {
	s.mu.Lock()
	l, ok := s.listings[name]
	s.mu.Unlock()
	if ok && s.Now().Sub(l.at) < listingFresh {
		return l.snaps, nil
	}
	repo, _, err := s.repo(name)
	if err != nil {
		return nil, err
	}
	snaps, err := s.Tool.Snapshots(ctx, repo, "deskyard")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.listings[name] = listing{at: s.Now(), snaps: snaps}
	s.mu.Unlock()
	return snaps, nil
}

func reasonOf(tags []string) string {
	for _, r := range []string{ReasonScheduled, ReasonManual, ReasonPreRestore} {
		if slices.Contains(tags, r) {
			return r
		}
	}
	return ""
}

func itemOf(tags []string) (Item, bool) {
	for _, t := range tags {
		if it, err := ParseTag(t); err == nil {
			return it, true
		}
	}
	return Item{}, false
}

// Snapshots lists this Deskyard's snapshots of it in every repository,
// newest first.
func (s *Service) Snapshots(ctx context.Context, it Item) ([]ItemSnapshot, error) {
	var out []ItemSnapshot
	for _, r := range s.Store.Repositories() {
		snaps, err := s.repoSnapshots(ctx, r.Name)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", r.Name, err)
		}
		for _, sn := range snaps {
			if sn.Host == s.Tool.Host && slices.Contains(sn.Tags, it.Tag()) {
				out = append(out, ItemSnapshot{Snapshot: sn, Repository: r.Name, Reason: reasonOf(sn.Tags)})
			}
		}
	}
	slices.SortFunc(out, func(a, b ItemSnapshot) int { return b.Time.Compare(a.Time) })
	return out, nil
}

// CatalogItem is an item found in a repository: its snapshots by one host.
type CatalogItem struct {
	Repository string    `json:"repository"`
	Host       string    `json:"host"`
	Item       Item      `json:"item"`
	Snapshots  int       `json:"snapshots"`
	Latest     time.Time `json:"latest"`
	LatestID   string    `json:"latestId"`
	// Local: this Deskyard's snapshots of an item it has.
	Local bool `json:"local"`
}

// Catalog lists every item in every repository, by host. A repository that
// can't be read is reported in errs, not failing the others.
func (s *Service) Catalog(ctx context.Context) (items []CatalogItem, errs map[string]string) {
	errs = map[string]string{}
	for _, r := range s.Store.Repositories() {
		snaps, err := s.repoSnapshots(ctx, r.Name)
		if err != nil {
			errs[r.Name] = err.Error()
			continue
		}
		byKey := map[string]*CatalogItem{}
		for _, sn := range snaps {
			it, ok := itemOf(sn.Tags)
			if !ok {
				continue
			}
			key := sn.Host + " " + it.Tag()
			c := byKey[key]
			if c == nil {
				c = &CatalogItem{Repository: r.Name, Host: sn.Host, Item: it, Local: sn.Host == s.Tool.Host && s.exists(it)}
				byKey[key] = c
			}
			c.Snapshots++
			if sn.Time.After(c.Latest) {
				c.Latest, c.LatestID = sn.Time, sn.ID
			}
		}
		for _, c := range byKey {
			items = append(items, *c)
		}
	}
	slices.SortFunc(items, func(a, b CatalogItem) int {
		return strings.Compare(a.Repository+" "+a.Item.Tag()+" "+a.Host, b.Repository+" "+b.Item.Tag()+" "+b.Host)
	})
	return items, errs
}

// snapshotOf finds a snapshot of it (by any host) in the repository.
func (s *Service) snapshotOf(ctx context.Context, repoName string, it Item, id string) (Snapshot, error) {
	snaps, err := s.repoSnapshots(ctx, repoName)
	if err != nil {
		return Snapshot{}, err
	}
	for _, sn := range snaps {
		if (sn.ID == id || sn.ShortID == id) && slices.Contains(sn.Tags, it.Tag()) {
			return sn, nil
		}
	}
	return Snapshot{}, fmt.Errorf("snapshot %s of %s: %w", id, it, ErrNotFound)
}

// itemRoot is where an item's files are in its snapshots.
func itemRoot(it Item) string {
	if it.Kind == KindEnvironment {
		return path.Join("/deskyard/environment", it.Name, "home")
	}
	return path.Join("/deskyard/volume", it.Name)
}
