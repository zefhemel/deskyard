// Package accounts keeps the supervisor's accounts and which computers
// each may use at which level, in accounts.json. Grants key on computer
// names.
package accounts

import (
	"crypto/rand"
	"encoding/hex"
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

// Grant lets an account use a computer at a level.
type Grant struct {
	Computer string     `json:"computer"`
	Level    auth.Level `json:"level"`
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

// Level is how far the account may use the named computer; admins have
// full access to every computer.
func (a Account) Level(name string) auth.Level {
	if a.Admin {
		return auth.LevelFull
	}
	return grantLevel(a.Grants, name)
}

// HasAccess reports whether the account has full use of the named computer.
func (a Account) HasAccess(name string) bool { return a.Level(name) == auth.LevelFull }

func (a Account) IsShare() bool     { return a.SharedBy != nil }
func (a Account) HasPassword() bool { return a.PasswordHash != "" }

// Expired reports whether the account's time limit has passed by now; an
// expired account is treated as deleted.
func (a Account) Expired(now time.Time) bool { return a.Expires != nil && now.After(*a.Expires) }

// GrantNames lists the computers the account has a grant of, at any level.
func (a Account) GrantNames() []string {
	out := make([]string, len(a.Grants))
	for i, g := range a.Grants {
		out[i] = g.Computer
	}
	return out
}

func grantLevel(grants []Grant, name string) auth.Level {
	for _, g := range grants {
		if g.Computer == name {
			return g.Level
		}
	}
	return auth.LevelNone
}

// withLevel is grants with the named computer at level; LevelNone drops it.
func withLevel(grants []Grant, name string, level auth.Level) []Grant {
	out := slices.DeleteFunc(slices.Clone(grants), func(g Grant) bool { return g.Computer == name })
	if level != auth.LevelNone {
		out = append(out, Grant{Computer: name, Level: level})
	}
	return out
}

func rank(l auth.Level) int {
	switch l {
	case auth.LevelFull:
		return 2
	case auth.LevelObserve:
		return 1
	}
	return 0
}

// Lowered maps the computers after grants less than before to their
// new level (LevelNone: the grant is gone).
func Lowered(before, after []Grant) map[string]auth.Level {
	out := map[string]auth.Level{}
	for _, g := range before {
		if l := grantLevel(after, g.Computer); rank(l) < rank(g.Level) {
			out[g.Computer] = l
		}
	}
	return out
}

// Capped is grants with each computer in caps at most at its level
// there; LevelNone drops the grant.
func Capped(grants []Grant, caps map[string]auth.Level) []Grant {
	out := slices.Clone(grants)
	for name, l := range caps {
		if rank(grantLevel(out, name)) > rank(l) {
			out = withLevel(out, name, l)
		}
	}
	return out
}

func validGrants(grants []Grant) error {
	seen := map[string]bool{}
	for _, g := range grants {
		if !auth.ValidLevel(g.Level) {
			return fmt.Errorf("%q is not an access level: use full or observe", g.Level)
		}
		if g.Computer == "" || seen[g.Computer] {
			return fmt.Errorf("a grant needs one computer, once: %q", g.Computer)
		}
		seen[g.Computer] = true
	}
	return nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var (
	ErrNotFound  = errors.New("no such account")
	ErrExists    = errors.New("an account with that name already exists")
	ErrLastAdmin = errors.New("the last admin with a password can't be deleted, made a regular account or lose the password")
	// ErrAdminExpires keeps the last-admin guard whole: an admin with a
	// password never expires, so the expiry sweep can't remove one.
	ErrAdminExpires = errors.New("admins with a password can't expire")
	// ErrNotSaved marks a failure to write the accounts: nothing changed.
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

// loaded is an account as stored, including the computers list grants
// replaced, kept under its old key "environments".
type loaded struct {
	Account
	Grants    []loadedGrant `json:"grants"`
	Computers []string      `json:"environments"`
}

// loadedGrant is a grant as stored. Grants saved before computers were called
// computers name theirs under "environment", read only when "computer" is
// absent.
type loadedGrant struct {
	Grant
	Environment *string `json:"environment"`
}

// legacy reports whether the grant was stored under "environment".
func (g loadedGrant) legacy() bool { return g.Environment != nil }

func (g loadedGrant) grant() Grant {
	if g.Computer == "" && g.Environment != nil {
		g.Computer = *g.Environment
	}
	return g.Grant
}

// Store is safe for concurrent use; every change is written to disk at once.
type Store struct {
	mu   sync.Mutex
	path string
	list []Account
	// OnChange is called (without the lock) after a change, with the usernames
	// whose sessions or access changed, so open connections can be checked.
	OnChange func(usernames ...string)
	// HeldSessionEpoch, when set, is the session epoch the account's
	// cookies may name although it isn't saved (a sign-out that couldn't be
	// saved). Called with the lock held; a bump starts from it, so it ends
	// those sessions too.
	HeldSessionEpoch func(a Account) int
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
	// Accounts without an ID get one, a computers list becomes full grants,
	// and grants stored under "environment" move to "computer"; the file is
	// rewritten so they keep them.
	changed := false
	for _, l := range f.Accounts {
		a := l.Account
		if l.Grants != nil {
			a.Grants = make([]Grant, 0, len(l.Grants))
			for _, g := range l.Grants {
				a.Grants = append(a.Grants, g.grant())
				changed = changed || g.legacy()
			}
		}
		if a.ID == "" {
			a.ID = randomID()
			changed = true
		}
		if l.Computers != nil {
			for _, name := range l.Computers {
				if grantLevel(a.Grants, name) == auth.LevelNone {
					a.Grants = append(a.Grants, Grant{Computer: name, Level: auth.LevelFull})
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
		if err := s.publish(s.list); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) publish(next []Account) error {
	if err := store.WriteJSON(s.path, file{Accounts: next}); err != nil {
		return notSaved{err}
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
	next := []Account{{ID: randomID(), Username: username, PasswordHash: auth.HashPassword(password), Admin: true,
		Grants: []Grant{}, Created: time.Now().UTC()}}
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
	out := make([]Account, len(s.list))
	for i, a := range s.list {
		out[i] = clone(a)
	}
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
	a := Account{ID: randomID(), Username: username, Description: n.Description, Admin: n.Admin,
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
	if err := s.publish(append(s.next(), a)); err != nil {
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
		held := 0
		if s.HeldSessionEpoch != nil {
			held = s.HeldSessionEpoch(before)
		}
		a.SessionEpoch = max(a.SessionEpoch, before.SessionEpoch, held) + 1
	}
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

// BumpSessionEpoch ends every browser session of the account.
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
	var gone []string
	var keep []Account
	removed := 0
	for _, a := range s.next() {
		if fn(clone(a)) {
			gone = append(gone, a.Username)
			if passwordAdmin(a) {
				removed++
			}
		} else {
			keep = append(keep, a)
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
	err := s.publish(keep)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	s.notify(gone...)
	return gone, nil
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

// SetAccess makes exactly the given regular accounts able to use the named
// computer, each at its level; every other regular account loses its
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
	err := s.publish(next)
	s.mu.Unlock()
	if err == nil && len(changed) > 0 {
		s.notify(changed...)
	}
	return err
}

// WithAccess maps the regular accounts (not admins, not shares) with a grant
// of the named computer to their level.
func (s *Store) WithAccess(name string) map[string]auth.Level {
	out := map[string]auth.Level{}
	for _, a := range s.List() {
		if l := grantLevel(a.Grants, name); !a.Admin && !a.IsShare() && l != auth.LevelNone {
			out[a.Username] = l
		}
	}
	return out
}

// Forget removes every grant of the named computer, when it is removed or
// before its name is used again.
func (s *Store) Forget(name string) error {
	return s.Retain(func(n string) bool { return n != name })
}

// Retain keeps only the grants of computers keep accepts; at startup it
// drops those of names without a definition. It writes only on a change.
func (s *Store) Retain(keep func(name string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, changed := s.next(), false
	for i := range next {
		n := len(next[i].Grants)
		next[i].Grants = slices.DeleteFunc(next[i].Grants, func(g Grant) bool { return !keep(g.Computer) })
		if len(next[i].Grants) != n {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.publish(next)
}

func (a Account) IsAdmin() bool { return a.Admin }
