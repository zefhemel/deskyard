// Package accounts keeps the supervisor's accounts and which environments
// each may use at which level, in accounts.json. Grants key on environment
// names.
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

// Grant lets an account use an environment at a level.
type Grant struct {
	Environment string     `json:"environment"`
	Level       auth.Level `json:"level"`
}

// Sharer is the account that created a share, by ID so a recreated
// account of the same name isn't its sharer.
type Sharer struct {
	Username string `json:"username"`
	ID       string `json:"id"`
}

// Account is a password account, or, without a password, a token account
// that signs in through login links and client credentials. A share is a
// token account with SharedBy set.
type Account struct {
	// ID is random and immutable: cookies, links and credentials bind to it,
	// so a reused username never matches them.
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"passwordHash,omitempty"`
	Description  string     `json:"description,omitempty"`
	Admin        bool       `json:"admin"`
	SessionEpoch int        `json:"sessionEpoch"`
	Grants       []Grant    `json:"grants"`
	SharedBy     *Sharer    `json:"sharedBy,omitempty"`
	Expires      *time.Time `json:"expires,omitempty"`
	Created      time.Time  `json:"created"`
	LastLogin    time.Time  `json:"lastLogin,omitzero"`
}

// Level is how far the account may use the named environment; admins have
// full access to every environment.
func (a Account) Level(name string) auth.Level {
	if a.Admin {
		return auth.LevelFull
	}
	return grantLevel(a.Grants, name)
}

func (a Account) IsShare() bool     { return a.SharedBy != nil }
func (a Account) HasPassword() bool { return a.PasswordHash != "" }

// ShareGrant is a share's one grant, of the environment it shares; false
// for an account that isn't a share, or a share whose environment is gone.
func (a Account) ShareGrant() (Grant, bool) {
	if a.SharedBy == nil || len(a.Grants) == 0 {
		return Grant{}, false
	}
	return a.Grants[0], true
}

// Expired reports whether the account's time limit has passed by now; an
// expired account is treated as deleted.
func (a Account) Expired(now time.Time) bool { return a.Expires != nil && now.After(*a.Expires) }

// Environments lists the environments the account has a grant of, at any
// level.
func (a Account) Environments() []string {
	out := make([]string, len(a.Grants))
	for i, g := range a.Grants {
		out[i] = g.Environment
	}
	return out
}

func grantLevel(grants []Grant, name string) auth.Level {
	for _, g := range grants {
		if g.Environment == name {
			return g.Level
		}
	}
	return auth.LevelNone
}

// withLevel is grants with the named environment at level; LevelNone drops it.
func withLevel(grants []Grant, name string, level auth.Level) []Grant {
	out := slices.DeleteFunc(slices.Clone(grants), func(g Grant) bool { return g.Environment == name })
	if level != auth.LevelNone {
		out = append(out, Grant{Environment: name, Level: level})
	}
	return out
}

// narrowed is what after revokes of before, the same account (by ID) as
// after a change: before, no longer an admin when after isn't, and each
// grant at most at after's level. With epoch, its session epoch moves on to
// after's too, ending its browser sessions. Nothing else changes: it never
// gains anything.
func narrowed(before, after Account, epoch bool) Account {
	a := clone(before)
	a.Admin = before.Admin && after.Admin
	a.Grants = slices.DeleteFunc(a.Grants, func(g Grant) bool { return grantLevel(after.Grants, g.Environment) == auth.LevelNone })
	for i, g := range a.Grants {
		if l := grantLevel(after.Grants, g.Environment); !l.AtLeast(g.Level) {
			a.Grants[i].Level = l
		}
	}
	if epoch {
		a.SessionEpoch = max(before.SessionEpoch, after.SessionEpoch)
	}
	return a
}

func validGrants(grants []Grant) error {
	seen := map[string]bool{}
	for _, g := range grants {
		if !auth.ValidLevel(g.Level) {
			return fmt.Errorf("%q is not an access level: use full or observe", g.Level)
		}
		if g.Environment == "" || seen[g.Environment] {
			return fmt.Errorf("a grant needs one environment, once: %q", g.Environment)
		}
		seen[g.Environment] = true
	}
	return nil
}

var (
	ErrNotFound  = errors.New("no such account")
	ErrExists    = errors.New("an account with that name already exists")
	ErrLastAdmin = errors.New("the last admin with a password can't be deleted, made a regular account or lose the password")
	// ErrAdminExpires keeps the last-admin guard whole: an admin with a
	// password never expires, so the expiry sweep can't remove one.
	ErrAdminExpires = errors.New("admins with a password can't expire")
	// ErrNotSaved marks a failure to write the accounts: of the change, only
	// what it revokes holds, in memory until a later write saves it.
	ErrNotSaved = errors.New("the accounts couldn't be saved")
	usernameRE  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)
)

// notSaved is a write error that is also ErrNotSaved, with its message.
type notSaved struct{ err error }

func (e notSaved) Error() string   { return e.err.Error() }
func (e notSaved) Unwrap() []error { return []error{ErrNotSaved, e.err} }

func ValidUsername(u string) error {
	if !usernameRE.MatchString(u) {
		return fmt.Errorf("a username is 1 to 32 characters: lowercase letters, digits, '.', '_' and '-', starting with a letter")
	}
	return nil
}

type file struct {
	Accounts []Account `json:"accounts"`
}

// loaded is an account as stored, including the environments list grants
// replaced.
type loaded struct {
	Account
	Environments []string `json:"environments"`
}

// Store is safe for concurrent use; every change is written to disk at once.
// It fails closed: when writing a change fails, what the change revokes
// (accounts deleted, admin roles taken, grants lowered, session epochs moved
// on) holds anyway, in memory, and is saved by the next write or Flush; the
// rest of the change is dropped.
type Store struct {
	mu   sync.Mutex
	path string
	list []Account
	// unsaved: list holds revocations the file doesn't have yet.
	unsaved bool
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
	var f struct {
		Accounts []loaded `json:"accounts"`
	}
	if err := store.ReadJSON(s.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	// Accounts without an ID get one, and an environments list becomes full
	// grants; the file is rewritten so they keep them.
	changed := false
	for _, l := range f.Accounts {
		a := l.Account
		if a.ID == "" {
			a.ID = auth.RandomID(16)
			changed = true
		}
		if l.Environments != nil {
			for _, name := range l.Environments {
				if grantLevel(a.Grants, name) == auth.LevelNone {
					a.Grants = append(a.Grants, Grant{Environment: name, Level: auth.LevelFull})
				}
			}
			changed = true
		}
		if a.Grants == nil {
			a.Grants = []Grant{}
		}
		s.list = append(s.list, a)
	}
	if changed {
		if err := store.WriteJSON(s.path, file{Accounts: s.list}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// commit saves next as the accounts and makes it theirs. When saving fails,
// only what next revokes takes effect (narrowed; epoch: session epochs
// too), and the store holds it unsaved; commit then returns ErrNotSaved
// with the usernames whose access changed all the same. The lock is held.
func (s *Store) commit(next []Account, epoch bool) (revoked []string, err error) {
	if err := store.WriteJSON(s.path, file{Accounts: next}); err != nil {
		after := map[string]Account{}
		for _, a := range next {
			after[a.ID] = a
		}
		held := []Account{}
		for _, a := range s.list {
			n, ok := after[a.ID]
			if !ok {
				revoked = append(revoked, a.Username)
				s.unsaved = true
				continue
			}
			if n = narrowed(a, n, epoch); !equalAccess(a, n) {
				revoked = append(revoked, a.Username)
				s.unsaved = true
			}
			held = append(held, n)
		}
		s.list = held
		return revoked, notSaved{err}
	}
	s.list, s.unsaved = next, false
	return nil, nil
}

// equalAccess reports whether a and b, the same account, may use the same.
func equalAccess(a, b Account) bool {
	return a.Admin == b.Admin && a.SessionEpoch == b.SessionEpoch && slices.Equal(a.Grants, b.Grants)
}

// Flush saves the revocations held unsaved, if any.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unsaved {
		return nil
	}
	_, err := s.commit(s.next(), false)
	return err
}

// Unsaved reports whether the store holds revocations it couldn't save yet.
func (s *Store) Unsaved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsaved
}

func (s *Store) next() []Account {
	out := make([]Account, len(s.list))
	for i, a := range s.list {
		out[i] = clone(a)
	}
	return out
}

func (s *Store) notify(names ...string) {
	if s.OnChange != nil && len(names) > 0 {
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
	next := []Account{{ID: auth.RandomID(16), Username: username, PasswordHash: auth.HashPassword(password), Admin: true,
		Grants: []Grant{}, Created: time.Now().UTC()}}
	if _, err := s.commit(next, false); err != nil {
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

// Usable is the named account while it is the one with this ID and may use
// anything now: it hasn't expired, and isn't a share whose sharer lost it.
// Requests and the connections they hold check it.
func (s *Store) Usable(username, id string, now time.Time) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(username)
	if i < 0 || s.list[i].ID != id || s.list[i].Expired(now) || s.orphaned(s.list[i], now) {
		return Account{}, false
	}
	return clone(s.list[i]), true
}

// orphaned reports whether a is a share that lost its reason to exist: its
// environment is gone (it has no grant), or its sharer (by ID) is gone,
// expired or no longer full on the environment. Admins are full everywhere,
// so their shares stay while they are admins. The lock is held.
func (s *Store) orphaned(a Account, now time.Time) bool {
	if a.SharedBy == nil {
		return false
	}
	g, ok := a.ShareGrant()
	if !ok {
		return true
	}
	i := s.index(a.SharedBy.Username)
	if i < 0 {
		return true
	}
	sharer := s.list[i]
	return sharer.ID != a.SharedBy.ID || sharer.Expired(now) || sharer.Level(g.Environment) != auth.LevelFull
}

func clone(a Account) Account {
	a.Grants = slices.Clone(a.Grants)
	if a.SharedBy != nil {
		sharer := *a.SharedBy
		a.SharedBy = &sharer
	}
	if a.Expires != nil {
		expires := *a.Expires
		a.Expires = &expires
	}
	return a
}

func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.next()
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// passwordAdmin is an admin that can sign in with a password; the
// last-admin guard keeps one, so the instance always has a password login.
func passwordAdmin(a Account) bool { return a.Admin && a.HasPassword() }

func (s *Store) passwordAdmins() int {
	n := 0
	for _, a := range s.list {
		if passwordAdmin(a) {
			n++
		}
	}
	return n
}

// NewAccount describes an account to create; without a password it is a
// token account.
type NewAccount struct {
	Username, Password, Description string
	Admin                           bool
	Grants                          []Grant
	SharedBy                        *Sharer
	Expires                         *time.Time
}

func (s *Store) Create(n NewAccount) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username := strings.ToLower(n.Username)
	if err := ValidUsername(username); err != nil {
		return Account{}, err
	}
	if s.index(username) >= 0 {
		return Account{}, ErrExists
	}
	if err := validGrants(n.Grants); err != nil {
		return Account{}, err
	}
	if n.Admin && n.Password != "" && n.Expires != nil {
		return Account{}, ErrAdminExpires
	}
	a := Account{ID: auth.RandomID(16), Username: username, Description: n.Description, Admin: n.Admin,
		Grants: slices.Clone(n.Grants), Expires: n.Expires, Created: time.Now().UTC()}
	if n.Password != "" {
		a.PasswordHash = auth.HashPassword(n.Password)
	}
	if a.Grants == nil {
		a.Grants = []Grant{}
	}
	if n.SharedBy != nil {
		sharer := *n.SharedBy
		a.SharedBy = &sharer
	}
	if _, err := s.commit(append(s.next(), a), false); err != nil {
		return Account{}, err
	}
	return clone(a), nil
}

// Update applies fn to the account; it bumps the session epoch when the
// password or admin flag changes, and refuses to
// leave no admin with a password. The ID and username don't change.
func (s *Store) Update(username string, fn func(a *Account)) (Account, error) {
	return s.update(username, fn, false)
}

func (s *Store) update(username string, fn func(a *Account), bump bool) (Account, error) {
	s.mu.Lock()
	i := s.index(username)
	if i < 0 {
		s.mu.Unlock()
		return Account{}, ErrNotFound
	}
	before := s.list[i]
	a := clone(before)
	fn(&a)
	a.ID, a.Username = before.ID, before.Username
	if a.Grants == nil {
		a.Grants = []Grant{}
	}
	if err := validGrants(a.Grants); err != nil {
		s.mu.Unlock()
		return Account{}, err
	}
	if passwordAdmin(a) && a.Expires != nil {
		s.mu.Unlock()
		return Account{}, ErrAdminExpires
	}
	if passwordAdmin(before) && !passwordAdmin(a) && s.passwordAdmins() == 1 {
		s.mu.Unlock()
		return Account{}, ErrLastAdmin
	}
	if bump || a.PasswordHash != before.PasswordHash || a.Admin != before.Admin {
		a.SessionEpoch = max(a.SessionEpoch, before.SessionEpoch) + 1
	}
	next := s.next()
	next[i] = a
	// Only a sign-out's epoch holds unsaved: a password or role change
	// that isn't saved leaves the sessions as they were.
	revoked, err := s.commit(next, bump)
	s.mu.Unlock()
	if err != nil {
		s.notify(revoked...)
		return Account{}, err
	}
	s.notify(a.Username)
	return clone(a), nil
}

// BumpSessionEpoch ends every browser session of the account. Unsaved, it
// holds in memory all the same.
func (s *Store) BumpSessionEpoch(username string) (Account, error) {
	return s.update(username, func(*Account) {}, true)
}

func (s *Store) SetPassword(username, password string) error {
	if password == "" {
		return errors.New("a password is required")
	}
	_, err := s.Update(username, func(a *Account) { a.PasswordHash = auth.HashPassword(password) })
	return err
}

func (s *Store) Delete(username string) error {
	username = strings.ToLower(username)
	gone, err := s.DeleteWhere(func(a Account) bool { return a.Username == username })
	if err == nil && len(gone) == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteWhere deletes every account fn accepts in one write and returns
// their usernames. It refuses to delete the last admin with a password;
// token accounts never count for that.
func (s *Store) DeleteWhere(fn func(Account) bool) ([]string, error) {
	s.mu.Lock()
	return s.deleteWhere(func(a Account) bool { return fn(clone(a)) })
}

// DeleteOrphans deletes the shares whose sharer lost them by now (see
// Usable), and returns their usernames.
func (s *Store) DeleteOrphans(now time.Time) ([]string, error) {
	s.mu.Lock()
	return s.deleteWhere(func(a Account) bool { return s.orphaned(a, now) })
}

// deleteWhere is DeleteWhere, called with the lock held, which it releases.
func (s *Store) deleteWhere(fn func(Account) bool) ([]string, error) {
	var gone []string
	var keep []Account
	removed := 0
	for _, a := range s.list {
		if fn(a) {
			gone = append(gone, a.Username)
			if passwordAdmin(a) {
				removed++
			}
		} else {
			keep = append(keep, clone(a))
		}
	}
	if len(gone) == 0 {
		s.mu.Unlock()
		return nil, nil
	}
	if removed > 0 && removed == s.passwordAdmins() {
		s.mu.Unlock()
		return nil, ErrLastAdmin
	}
	if keep == nil {
		keep = []Account{}
	}
	_, err := s.commit(keep, false)
	s.mu.Unlock()
	s.notify(gone...) // deleted, saved or not
	if err != nil {
		return nil, err
	}
	return gone, nil
}

func (s *Store) TouchLogin(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.index(username); i >= 0 {
		next := s.next()
		next[i].LastLogin = time.Now().UTC()
		_, err := s.commit(next, false)
		return err
	}
	return nil
}

// SetAccess makes exactly the given regular accounts able to use the named
// environment, each at its level; every other regular account loses its
// grant of it. Admins (full everywhere) and shares (whose one grant is set
// when they are shared) are left alone.
func (s *Store) SetAccess(name string, levels map[string]auth.Level) error {
	for u, l := range levels {
		if !auth.ValidLevel(l) {
			return fmt.Errorf("%q is not an access level for %s: use full or observe", l, u)
		}
	}
	s.mu.Lock()
	var changed []string
	next := s.next()
	for i := range next {
		a := &next[i]
		if a.Admin || a.IsShare() {
			continue
		}
		want := levels[a.Username]
		if grantLevel(a.Grants, name) != want {
			a.Grants = withLevel(a.Grants, name, want)
			changed = append(changed, a.Username)
		}
	}
	revoked, err := s.commit(next, false)
	s.mu.Unlock()
	if err != nil {
		changed = revoked
	}
	s.notify(changed...)
	return err
}

// WithAccess maps the regular accounts (not admins, not shares) with a grant
// of the named environment to their level.
func (s *Store) WithAccess(name string) map[string]auth.Level {
	out := map[string]auth.Level{}
	for _, a := range s.List() {
		if l := grantLevel(a.Grants, name); !a.Admin && !a.IsShare() && l != auth.LevelNone {
			out[a.Username] = l
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
		n := len(next[i].Grants)
		next[i].Grants = slices.DeleteFunc(next[i].Grants, func(g Grant) bool { return !keep(g.Environment) })
		if len(next[i].Grants) != n {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	_, err := s.commit(next, false)
	return err
}

func (a Account) IsAdmin() bool { return a.Admin }
