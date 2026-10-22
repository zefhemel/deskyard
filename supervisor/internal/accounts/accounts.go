// Package accounts keeps the supervisor's accounts and which desktops each
// may use, in accounts.json.
package accounts

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

type Account struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	Admin        bool      `json:"admin"`
	SessionEpoch int       `json:"sessionEpoch"`
	Desktops     []string  `json:"desktops"` // desktop IDs (the deskyard.id label)
	Created      time.Time `json:"created"`
	LastLogin    time.Time `json:"lastLogin,omitzero"`
}

// HasAccess reports whether the account may use the desktop.
func (a Account) HasAccess(desktopID string) bool {
	return a.Admin || slices.Contains(a.Desktops, desktopID)
}

var (
	ErrNotFound  = errors.New("no such account")
	ErrExists    = errors.New("an account with that name already exists")
	ErrLastAdmin = errors.New("the last admin can't be deleted or made a regular account")
	usernameRE   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)
)

func ValidUsername(u string) error {
	if !usernameRE.MatchString(u) {
		return fmt.Errorf("a username is 1 to 32 characters: lowercase letters, digits, '.', '_' and '-', starting with a letter")
	}
	return nil
}

type file struct {
	Accounts []Account `json:"accounts"`
}

// Store is safe for concurrent use; every change is written to disk at once.
type Store struct {
	mu   sync.Mutex
	path string
	list []Account
	// OnChange is called (without the lock) after a change, with the usernames
	// whose sessions or access changed, so open connections can be checked.
	OnChange func(usernames ...string)
}

func Load(stateDir string) (*Store, error) {
	s := &Store{path: filepath.Join(stateDir, "accounts.json")}
	var f file
	if err := store.ReadJSON(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	s.list = f.Accounts
	return s, nil
}

func (s *Store) save() error {
	return store.WriteJSON(s.path, file{Accounts: s.list})
}

func (s *Store) notify(names ...string) {
	if s.OnChange != nil {
		s.OnChange(names...)
	}
}

// EnsureAdmin creates the first admin when there are no accounts yet. It
// returns the generated password when none was given, so it can be shown once.
func (s *Store) EnsureAdmin(username, password string) (generated string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.list) > 0 {
		return "", nil
	}
	if err := ValidUsername(username); err != nil {
		return "", err
	}
	if password == "" {
		password = auth.RandomPassword()
		generated = password
	}
	s.list = []Account{{Username: username, PasswordHash: auth.HashPassword(password), Admin: true, Created: time.Now().UTC()}}
	return generated, s.save()
}

func (s *Store) index(username string) int {
	username = strings.ToLower(username)
	for i, a := range s.list {
		if a.Username == username {
			return i
		}
	}
	return -1
}

func (s *Store) Get(username string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(username)
	if i < 0 {
		return Account{}, false
	}
	return clone(s.list[i]), true
}

func clone(a Account) Account {
	a.Desktops = slices.Clone(a.Desktops)
	return a
}

func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, len(s.list))
	for i, a := range s.list {
		out[i] = clone(a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (s *Store) admins() int {
	n := 0
	for _, a := range s.list {
		if a.Admin {
			n++
		}
	}
	return n
}

func (s *Store) Create(username, password string, admin bool, desktops []string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = strings.ToLower(username)
	if err := ValidUsername(username); err != nil {
		return Account{}, err
	}
	if s.index(username) >= 0 {
		return Account{}, ErrExists
	}
	if password == "" {
		return Account{}, errors.New("a password is required")
	}
	a := Account{Username: username, PasswordHash: auth.HashPassword(password), Admin: admin,
		Desktops: slices.Clone(desktops), Created: time.Now().UTC()}
	if a.Desktops == nil {
		a.Desktops = []string{}
	}
	s.list = append(s.list, a)
	return clone(a), s.save()
}

// Update applies fn to the account; it bumps the session epoch when the
// password or admin flag changes, and refuses to leave no admin.
func (s *Store) Update(username string, fn func(a *Account)) (Account, error) {
	s.mu.Lock()
	i := s.index(username)
	if i < 0 {
		s.mu.Unlock()
		return Account{}, ErrNotFound
	}
	before := s.list[i]
	a := clone(before)
	fn(&a)
	a.Username = before.Username
	if before.Admin && !a.Admin && s.admins() == 1 {
		s.mu.Unlock()
		return Account{}, ErrLastAdmin
	}
	if a.PasswordHash != before.PasswordHash || a.Admin != before.Admin {
		a.SessionEpoch = before.SessionEpoch + 1
	}
	s.list[i] = a
	err := s.save()
	s.mu.Unlock()
	s.notify(a.Username)
	return clone(a), err
}

func (s *Store) SetPassword(username, password string) error {
	if password == "" {
		return errors.New("a password is required")
	}
	_, err := s.Update(username, func(a *Account) { a.PasswordHash = auth.HashPassword(password) })
	return err
}

func (s *Store) Delete(username string) error {
	s.mu.Lock()
	i := s.index(username)
	if i < 0 {
		s.mu.Unlock()
		return ErrNotFound
	}
	if s.list[i].Admin && s.admins() == 1 {
		s.mu.Unlock()
		return ErrLastAdmin
	}
	name := s.list[i].Username
	s.list = slices.Delete(s.list, i, i+1)
	err := s.save()
	s.mu.Unlock()
	s.notify(name)
	return err
}

func (s *Store) TouchLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(username); i >= 0 {
		s.list[i].LastLogin = time.Now().UTC()
		_ = s.save()
	}
}

// SetAccess makes exactly the given accounts (besides admins) able to use the desktop.
func (s *Store) SetAccess(desktopID string, usernames []string) error {
	s.mu.Lock()
	var changed []string
	for i := range s.list {
		a := &s.list[i]
		want := slices.Contains(usernames, a.Username)
		has := slices.Contains(a.Desktops, desktopID)
		switch {
		case want && !has:
			a.Desktops = append(a.Desktops, desktopID)
			changed = append(changed, a.Username)
		case !want && has:
			a.Desktops = slices.DeleteFunc(a.Desktops, func(id string) bool { return id == desktopID })
			changed = append(changed, a.Username)
		}
	}
	err := s.save()
	s.mu.Unlock()
	s.notify(changed...)
	return err
}

// WithAccess lists the regular accounts that may use the desktop.
func (s *Store) WithAccess(desktopID string) []string {
	var out []string
	for _, a := range s.List() {
		if !a.Admin && slices.Contains(a.Desktops, desktopID) {
			out = append(out, a.Username)
		}
	}
	return out
}

// Forget removes desktop IDs that aren't in keep (when known is false for them).
func (s *Store) Forget(isGone func(desktopID string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		s.list[i].Desktops = slices.DeleteFunc(s.list[i].Desktops, isGone)
	}
	return s.save()
}

func (a Account) IsAdmin() bool { return a.Admin }
