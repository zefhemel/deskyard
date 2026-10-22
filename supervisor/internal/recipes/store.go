package recipes

import (
	"errors"
	"io/fs"
	"sort"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// ErrNotFound is returned for an unknown slug.
var ErrNotFound = errors.New("recipe not found")

// Store keeps recipes in one JSON file (.deskyard/recipes.json).
type Store struct {
	mu   sync.Mutex
	path string
}

type storeFile struct {
	Recipes []Recipe `json:"recipes"`
}

// NewStore returns a store over path. A missing file is an empty store.
func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) load() ([]Recipe, error) {
	var f storeFile
	if err := store.ReadJSON(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return f.Recipes, nil
}

func (s *Store) save(rs []Recipe) error {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Slug < rs[j].Slug })
	return store.WriteJSON(s.path, storeFile{Recipes: rs})
}

// List returns all recipes, ordered by slug.
func (s *Store) List() ([]Recipe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get returns the recipe with this slug, or ErrNotFound.
func (s *Store) Get(slug string) (Recipe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return Recipe{}, err
	}
	for _, r := range rs {
		if r.Slug == slug {
			return r, nil
		}
	}
	return Recipe{}, ErrNotFound
}

// Put validates r, then adds it or replaces the recipe with the same slug.
func (s *Store) Put(r Recipe) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return err
	}
	for i := range rs {
		if rs[i].Slug == r.Slug {
			rs[i] = r
			return s.save(rs)
		}
	}
	return s.save(append(rs, r))
}

// Delete removes the recipe with this slug, or returns ErrNotFound.
func (s *Store) Delete(slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, err := s.load()
	if err != nil {
		return err
	}
	for i := range rs {
		if rs[i].Slug == slug {
			return s.save(append(rs[:i], rs[i+1:]...))
		}
	}
	return ErrNotFound
}
