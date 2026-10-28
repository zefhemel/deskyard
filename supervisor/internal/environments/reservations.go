package environments

import (
	"errors"
	"fmt"
	"sync"
)

// ErrItemBusy refuses work on an environment's or a volume's files while a
// backup, restore or removal holds them.
var ErrItemBusy = errors.New("a backup, restore or removal is running")

// Item kinds.
const (
	KindEnvironment = "environment" // its definition and home folder
	KindVolume      = "volume"
)

// Item is an environment or a volume, by name: what is reserved, and what
// backups are of.
type Item struct {
	Kind, Name string
}

func (i Item) String() string { return i.Kind + " " + i.Name }

// reservation is what holds an item. Only environments have operations: a
// lifecycle operation (Begin) holds the environment, with its transient
// state, against another. Work on the files (HoldFiles) holds them against
// other work on them; while they are being replaced, nothing may mount them.
type reservation struct {
	operating bool
	operation string
	files     string // what holds the files; "" when nothing does
	replacing bool
}

// Begin marks an environment busy for an operation; Done clears it. An empty state
// reserves the slot without overriding the engine's live state.
func (s *Service) Begin(name, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := Item{KindEnvironment, name}
	r := s.reserved[it]
	if r.operating {
		return ErrBusy
	}
	r.operating, r.operation = true, state
	s.reserved[it] = r
	return nil
}

func (s *Service) Done(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := Item{KindEnvironment, name}
	r := s.reserved[it]
	r.operating, r.operation = false, ""
	s.put(it, r)
}

// put saves r as it's reservation, forgetting one that holds nothing; s.mu
// is held.
func (s *Service) put(it Item, r reservation) {
	if r == (reservation{}) {
		delete(s.reserved, it)
	} else {
		s.reserved[it] = r
	}
}

// Busy returns the transient states of environments in the middle of a long operation.
func (s *Service) Busy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for it, r := range s.reserved {
		if r.operating {
			out[it.Name] = r.operation
		}
	}
	return out
}

// HoldFiles holds it's files for what (such as "backing up") until release,
// refusing with ErrItemBusy while something else holds them. While files
// being replaced (as by a restore in place) are held, no environment using
// them starts or is changed, and none is granted them; while any hold is
// there, the environment isn't removed and the volume isn't deleted.
func (s *Service) HoldFiles(it Item, what string, replacing bool) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.reserved[it]
	if r.files != "" {
		return nil, fmt.Errorf("%w: %s is %s", ErrItemBusy, it, r.files)
	}
	r.files, r.replacing = what, replacing
	s.reserved[it] = r
	return sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		r := s.reserved[it]
		r.files, r.replacing = "", false
		s.put(it, r)
	}), nil
}

// FilesActivity is what holds it's files ("" when nothing does).
func (s *Service) FilesActivity(it Item) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved[it].files
}

// itemsOf is the environment name, when not "", and the volumes of grants.
func itemsOf(name string, grants []VolumeGrant) []Item {
	var items []Item
	if name != "" {
		items = append(items, Item{KindEnvironment, name})
	}
	for _, g := range grants {
		items = append(items, Item{KindVolume, g.Name})
	}
	return items
}

// replacing refuses with ErrItemBusy when the files of any of items are
// being replaced.
func (s *Service) replacing(items []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		if r := s.reserved[it]; r.replacing {
			return fmt.Errorf("%w: %s is %s", ErrItemBusy, it, r.files)
		}
	}
	return nil
}
