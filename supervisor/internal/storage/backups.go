package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// Item kinds: what a backup policy and its snapshots are of.
const (
	KindEnvironment = "environment" // the definition and home folder
	KindVolume      = "volume"
)

// Item is an environment or a volume, by name.
type Item struct {
	Kind, Name string
}

// ParseItem checks kind and name.
func ParseItem(kind, name string) (Item, error) {
	if kind != KindEnvironment && kind != KindVolume {
		return Item{}, fmt.Errorf("backups are of an environment or a volume, not %q", kind)
	}
	if err := environments.ValidName(name); err != nil {
		return Item{}, err
	}
	return Item{kind, name}, nil
}

// ParseTag reads an item's restic tag, kind:name.
func ParseTag(tag string) (Item, error) {
	kind, name, ok := strings.Cut(tag, ":")
	if !ok {
		return Item{}, fmt.Errorf("%q isn't an item tag", tag)
	}
	return ParseItem(kind, name)
}

// Tag is the item's restic tag and its key in backups.json.
func (i Item) Tag() string { return i.Kind + ":" + i.Name }

func (i Item) String() string { return i.Tag() }

func (i Item) MarshalText() ([]byte, error) { return []byte(i.Tag()), nil }

func (i *Item) UnmarshalText(b []byte) error {
	it, err := ParseTag(string(b))
	if err != nil {
		return err
	}
	*i = it
	return nil
}

// Repository is a restic repository in Folder below a remote's root. Its
// recovery key is in secret/backups/<name>/restic-password.
type Repository struct {
	Name    string    `json:"name"`
	Remote  string    `json:"remote"`
	Folder  string    `json:"folder"`
	Created time.Time `json:"created"`
}

// DefaultFolder is where a repository goes on its remote unless told otherwise.
const DefaultFolder = "deskyard-backups"

func (r Repository) validate() error {
	if err := environments.ValidName(r.Name); err != nil {
		return fmt.Errorf("invalid repository name: %w", err)
	}
	if err := environments.ValidName(r.Remote); err != nil {
		return fmt.Errorf("invalid remote: %w", err)
	}
	if r.Folder == "" || strings.HasPrefix(r.Folder, "/") || strings.ContainsAny(r.Folder, "\r\n\x00\\:") || slices.Contains(strings.Split(r.Folder, "/"), "..") || len(r.Folder) > 1024 {
		return errors.New("the folder is a relative path below the remote's root, without .. or line breaks")
	}
	return nil
}

// Policy is an item's backup policy: where to, when, and what to keep.
type Policy struct {
	Item       Item      `json:"item"`
	Repository string    `json:"repository"`
	Enabled    bool      `json:"enabled"`
	Schedule   Schedule  `json:"schedule"`
	Retention  Retention `json:"retention"`
	Updated    time.Time `json:"updated"` // the schedule counts from here
}

func (p Policy) validate() error {
	if _, err := ParseItem(p.Item.Kind, p.Item.Name); err != nil {
		return err
	}
	if err := p.Schedule.Validate(); err != nil {
		return err
	}
	return p.Retention.Validate()
}

const backupsVersion = 1

type backupsFile struct {
	Version      int          `json:"version"`
	Repositories []Repository `json:"repositories"`
	Policies     []Policy     `json:"policies"`
}

// Store is backups.json: the repositories and the policies.
type Store struct {
	path string

	mu       sync.Mutex
	repos    map[string]Repository
	policies map[Item]Policy
}

// LoadStore reads path; a missing file is an empty store.
func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, repos: map[string]Repository{}, policies: map[Item]Policy{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f backupsFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version != backupsVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", path, f.Version, backupsVersion)
	}
	for _, r := range f.Repositories {
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("%s: repository %q: %w", path, r.Name, err)
		}
		if _, dup := s.repos[r.Name]; dup {
			return nil, fmt.Errorf("%s: repository %q is defined twice", path, r.Name)
		}
		s.repos[r.Name] = r
	}
	for _, p := range f.Policies {
		if err := p.validate(); err != nil {
			return nil, fmt.Errorf("%s: policy of %s: %w", path, p.Item, err)
		}
		if _, ok := s.repos[p.Repository]; !ok {
			return nil, fmt.Errorf("%s: policy of %s: no repository %q", path, p.Item, p.Repository)
		}
		if _, dup := s.policies[p.Item]; dup {
			return nil, fmt.Errorf("%s: %s has two policies", path, p.Item)
		}
		s.policies[p.Item] = p
	}
	return s, nil
}

func (s *Store) Repositories() []Repository {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Repository, 0, len(s.repos))
	for _, r := range s.repos {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (s *Store) Repository(name string) (Repository, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[name]
	return r, ok
}

// RepositoriesOnRemote names the repositories on the remote, sorted.
func (s *Store) RepositoriesOnRemote(remote string) []string {
	var out []string
	for _, r := range s.Repositories() {
		if r.Remote == remote {
			out = append(out, r.Name)
		}
	}
	return out
}

func (s *Store) AddRepository(r Repository) error {
	if err := r.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.repos[r.Name]; ok {
		return fmt.Errorf("repository %q: %w", r.Name, ErrExists)
	}
	repos := maps(s.repos)
	repos[r.Name] = r
	return s.save(repos, s.policies)
}

// RemoveRepository forgets the repository, refusing while a policy uses it.
func (s *Store) RemoveRepository(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.repos[name]; !ok {
		return fmt.Errorf("repository %q: %w", name, ErrNotFound)
	}
	var users []string
	for it, p := range s.policies {
		if p.Repository == name {
			users = append(users, it.Tag())
		}
	}
	if len(users) > 0 {
		slices.Sort(users)
		return fmt.Errorf("%w: the backups of %s use repository %s", ErrInUse, strings.Join(users, ", "), name)
	}
	repos := maps(s.repos)
	delete(repos, name)
	return s.save(repos, s.policies)
}

// Policies returns every policy, sorted by item.
func (s *Store) Policies() []Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedPolicies(s.policies)
}

func (s *Store) Policy(it Item) (Policy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policies[it]
	return p, ok
}

// PutPolicy saves p, stamping when it changed.
func (s *Store) PutPolicy(p Policy) error {
	if err := p.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.repos[p.Repository]; !ok {
		return fmt.Errorf("no repository %q", p.Repository)
	}
	p.Updated = time.Now().UTC()
	policies := maps(s.policies)
	policies[p.Item] = p
	return s.save(s.repos, policies)
}

// DeletePolicy removes it's policy; none is not an error.
func (s *Store) DeletePolicy(it Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.policies[it]; !ok {
		return nil
	}
	policies := maps(s.policies)
	delete(policies, it)
	return s.save(s.repos, policies)
}

func (s *Store) save(repos map[string]Repository, policies map[Item]Policy) error {
	f := backupsFile{Version: backupsVersion, Repositories: []Repository{}, Policies: sortedPolicies(policies)}
	for _, r := range repos {
		f.Repositories = append(f.Repositories, r)
	}
	slices.SortFunc(f.Repositories, func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })
	if err := store.WriteJSON(s.path, f); err != nil {
		return err
	}
	s.repos, s.policies = repos, policies
	return nil
}

func sortedPolicies(m map[Item]Policy) []Policy {
	out := make([]Policy, 0, len(m))
	for _, p := range m {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Policy) int { return strings.Compare(a.Item.Tag(), b.Item.Tag()) })
	return out
}

// RunState is what happened to an item's backups: the last attempt and
// success, the snapshot it made, and the last error ("" after a success).
type RunState struct {
	LastAttempt time.Time `json:"lastAttempt"`
	LastSuccess time.Time `json:"lastSuccess"`
	Snapshot    string    `json:"snapshot,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type stateFile struct {
	Items  map[string]RunState  `json:"items"`
	Prunes map[string]time.Time `json:"prunes"`
}

// State is state/backups.json, the run state: rewritten as backups run, so
// a restart neither loses nor repeats a run.
type State struct {
	path string

	mu sync.Mutex
	f  stateFile
}

func LoadState(path string) (*State, error) {
	st := &State{path: path, f: stateFile{Items: map[string]RunState{}, Prunes: map[string]time.Time{}}}
	if err := store.ReadJSON(path, &st.f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.f.Items == nil {
		st.f.Items = map[string]RunState{}
	}
	if st.f.Prunes == nil {
		st.f.Prunes = map[string]time.Time{}
	}
	return st, nil
}

func (st *State) Get(it Item) RunState {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.f.Items[it.Tag()]
}

func (st *State) Record(it Item, rs RunState) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.f.Items[it.Tag()] = rs
	return store.WriteJSON(st.path, st.f)
}

func (st *State) Forget(it Item) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.f.Items, it.Tag())
	return store.WriteJSON(st.path, st.f)
}

func (st *State) LastPrune(repo string) time.Time {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.f.Prunes[repo]
}

func (st *State) RecordPrune(repo string, t time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.f.Prunes[repo] = t
	return store.WriteJSON(st.path, st.f)
}
