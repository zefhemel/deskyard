// Package accounts keeps the supervisor's accounts and which environments
// each may use, in accounts.json. Grants key on environment names.
package accounts

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	Admin        bool   `json:"admin"`
	SessionEpoch int    `json:"sessionEpoch"`
	TokenEpoch   uint64 `json:"tokenEpoch,omitempty"`
	// EnvironmentTokenEpochs counts, per environment name, the times this
	// account lost access to it; agent tokens record the count they were made at.
	EnvironmentTokenEpochs map[string]uint64 `json:"environmentTokenEpochs,omitempty"`
	Environments           []string          `json:"environments"` // environment names
	Created                time.Time         `json:"created"`
	LastLogin              time.Time         `json:"lastLogin,omitzero"`
}

// HasAccess reports whether the account may use the named environment.
func (a Account) HasAccess(name string) bool {
	return a.Admin || slices.Contains(a.Environments, name)
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

// Load reads dataDir/accounts.json (mode 0600: it holds password hashes).
func Load(dataDir string) (*Store, error) {
	s := &Store{path: filepath.Join(dataDir, "accounts.json")}
	if err := store.Restrict(s.path); err != nil {
		return nil, err
	}
	var f file
	if err := store.ReadJSON(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	s.list = f.Accounts
	return s, nil
}

func (s *Store) publish(next []Account) error {
	if err := store.WriteJSON(s.path, file{Accounts: next}); err != nil {
		return err
	}
	s.list = next
	return nil
}

func (s *Store) next() []Account {
	out := make([]Account, len(s.list))
	for i, a := range s.list {
		out[i] = clone(a)
	}
	return out
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
	next := []Account{{Username: username, PasswordHash: auth.HashPassword(password), Admin: true, Created: time.Now().UTC()}}
	if err := s.publish(next); err != nil {
		return "", err
	}
	return generated, nil
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
	a.Environments = slices.Clone(a.Environments)
	a.EnvironmentTokenEpochs = maps.Clone(a.EnvironmentTokenEpochs)
	return a
}

// retainTokenRevocations makes authorization loss survive later grants and
// username reuse. A role change invalidates tokens account-wide; access changes
// only advance the affected environment's revision.
func retainTokenRevocations(before Account, after *Account) {
	after.TokenEpoch = before.TokenEpoch
	after.EnvironmentTokenEpochs = maps.Clone(before.EnvironmentTokenEpochs)
	if before.Admin != after.Admin {
		after.TokenEpoch++
	}
	for _, name := range before.Environments {
		if !after.HasAccess(name) {
			if after.EnvironmentTokenEpochs == nil {
				after.EnvironmentTokenEpochs = map[string]uint64{}
			}
			after.EnvironmentTokenEpochs[name] = before.EnvironmentTokenEpochs[name] + 1
		}
	}
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

func (s *Store) Create(username, password string, admin bool, environments []string) (Account, error) {
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
		Environments: slices.Clone(environments), Created: time.Now().UTC()}
	if a.Environments == nil {
		a.Environments = []string{}
	}
	if err := s.publish(append(s.next(), a)); err != nil {
		return Account{}, err
	}
	return clone(a), nil
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
	retainTokenRevocations(before, &a)
	next := s.next()
	next[i] = a
	err := s.publish(next)
	s.mu.Unlock()
	if err != nil {
		return Account{}, err
	}
	s.notify(a.Username)
	return clone(a), nil
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
	err := s.publish(slices.Delete(s.next(), i, i+1))
	s.mu.Unlock()
	if err == nil {
		s.notify(name)
	}
	return err
}

func (s *Store) TouchLogin(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(username); i >= 0 {
		next := s.next()
		next[i].LastLogin = time.Now().UTC()
		return s.publish(next)
	}
	return nil
}

// SetAccess makes exactly the given accounts (besides admins) able to use
// the named environment.
func (s *Store) SetAccess(name string, usernames []string) error {
	s.mu.Lock()
	var changed []string
	next := s.next()
	for i := range next {
		a := &next[i]
		want := slices.Contains(usernames, a.Username)
		has := slices.Contains(a.Environments, name)
		switch {
		case want && !has:
			a.Environments = append(a.Environments, name)
			changed = append(changed, a.Username)
		case !want && has:
			a.Environments = slices.DeleteFunc(a.Environments, func(n string) bool { return n == name })
			changed = append(changed, a.Username)
		}
		retainTokenRevocations(s.list[i], a)
	}
	err := s.publish(next)
	s.mu.Unlock()
	if err == nil && len(changed) > 0 {
		s.notify(changed...)
	}
	return err
}

// WithAccess lists the regular accounts that may use the named environment.
func (s *Store) WithAccess(name string) []string {
	var out []string
	for _, a := range s.List() {
		if !a.Admin && slices.Contains(a.Environments, name) {
			out = append(out, a.Username)
		}
	}
	return out
}

// Forget removes every grant of the named environment, when it is removed or
// before its name is used again.
func (s *Store) Forget(name string) error {
	return s.Retain(func(n string) bool { return n != name })
}

// Retain keeps only the grants of environments keep accepts; at startup it
// drops those of names without a definition. It writes only on a change.
func (s *Store) Retain(keep func(name string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, changed := s.next(), false
	for i := range next {
		n := len(next[i].Environments)
		next[i].Environments = slices.DeleteFunc(next[i].Environments, func(name string) bool { return !keep(name) })
		if len(next[i].Environments) != n {
			changed = true
			retainTokenRevocations(s.list[i], &next[i])
		}
	}
	if !changed {
		return nil
	}
	return s.publish(next)
}

func (a Account) IsAdmin() bool { return a.Admin }
