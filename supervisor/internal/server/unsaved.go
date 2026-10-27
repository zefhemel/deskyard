package server

import (
	"maps"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// unsavedRevocation is a revocation of one account (by ID) that couldn't be
// saved: the account deleted, its admin role taken, grants lowered, or its
// browser sessions ended.
type unsavedRevocation struct {
	username string
	id       string
	deleted  bool
	demoted  bool
	caps     map[string]auth.Level // environment → the most it may still use
	epoch    int                   // the least session epoch its cookies may name
}

// unsavedRevocations holds revocations while writing accounts.json fails.
// accountState applies them, so they hold for every request and live
// connection, and the sweep retries saving them.
type unsavedRevocations struct {
	mu sync.Mutex
	m  map[string]unsavedRevocation // by username
}

func newUnsavedRevocations() *unsavedRevocations {
	return &unsavedRevocations{m: map[string]unsavedRevocation{}}
}

// add merges r into the account's unsaved revocations.
func (u *unsavedRevocations) add(r unsavedRevocation) {
	u.mu.Lock()
	defer u.mu.Unlock()
	old, ok := u.m[r.username]
	if !ok || old.id != r.id {
		old = unsavedRevocation{username: r.username, id: r.id, caps: map[string]auth.Level{}}
	}
	old.deleted = old.deleted || r.deleted
	old.demoted = old.demoted || r.demoted
	old.epoch = max(old.epoch, r.epoch)
	maps.Copy(old.caps, r.caps)
	u.m[r.username] = old
}

// forget drops what fn accepts of the account's unsaved revocations, as
// when a later change saved its own decision about them.
func (u *unsavedRevocations) forget(username string, fn func(r *unsavedRevocation)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if r, ok := u.m[username]; ok {
		r.caps = maps.Clone(r.caps)
		fn(&r)
		if !r.deleted && !r.demoted && len(r.caps) == 0 && r.epoch == 0 {
			delete(u.m, username)
		} else {
			u.m[username] = r
		}
	}
}

func (u *unsavedRevocations) list() []unsavedRevocation {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]unsavedRevocation, 0, len(u.m))
	for _, r := range u.m {
		r.caps = maps.Clone(r.caps)
		out = append(out, r)
	}
	return out
}

// apply is a as its unsaved revocations leave it; false when it is deleted.
func (u *unsavedRevocations) apply(a accounts.Account) (accounts.Account, bool) {
	u.mu.Lock()
	r, ok := u.m[a.Username]
	u.mu.Unlock()
	if !ok || r.id != a.ID {
		return a, true
	}
	if r.deleted {
		return accounts.Account{}, false
	}
	if r.demoted {
		a.Admin = false
	}
	a.Grants = accounts.Capped(a.Grants, r.caps)
	a.SessionEpoch = max(a.SessionEpoch, r.epoch)
	return a, true
}

// saved reports whether the stored account a already holds r.
func (r unsavedRevocation) saved(a accounts.Account) bool {
	return !r.deleted && (!r.demoted || !a.Admin) && len(accounts.Lowered(a.Grants, accounts.Capped(a.Grants, r.caps))) == 0 &&
		a.SessionEpoch >= r.epoch
}

// accountState is the named account as it stands, with the revocations
// that couldn't be saved applied.
func (s *Server) accountState(username string) (accounts.Account, bool) {
	a, ok := s.Accounts.Get(username)
	if !ok {
		return a, false
	}
	return s.unsaved.apply(a)
}

// accountStates are all accounts as accountState has them.
func (s *Server) accountStates() []accounts.Account {
	var out []accounts.Account
	for _, a := range s.Accounts.List() {
		if a, ok := s.unsaved.apply(a); ok {
			out = append(out, a)
		}
	}
	return out
}

// revokeUnsaved holds r although saving it failed, and closes the live
// connections it ends. The caller holds s.revoking.
func (s *Server) revokeUnsaved(r unsavedRevocation) {
	s.unsaved.add(r)
	s.accountsChanged(r.username)
}

// retryUnsaved saves the revocations that couldn't be saved; it forgets
// those of accounts gone or replaced, and those saved since. While a change
// decides access anew (or this retry's own save notifies) it leaves them to
// a later sweep: the change may forget them, and saving them after it
// would undo its decision.
func (s *Server) retryUnsaved() {
	if !s.revoking.TryLock() {
		return
	}
	defer s.revoking.Unlock()
	for _, r := range s.unsaved.list() {
		a, ok := s.Accounts.Get(r.username)
		if !ok || a.ID != r.id || r.saved(a) {
			s.unsaved.forget(r.username, func(u *unsavedRevocation) { *u = unsavedRevocation{} })
			continue
		}
		var err error
		if r.deleted {
			_, err = s.Accounts.DeleteWhere(func(a accounts.Account) bool { return a.Username == r.username && a.ID == r.id })
		} else {
			_, err = s.Accounts.Update(r.username, func(a *accounts.Account) {
				a.Admin = a.Admin && !r.demoted
				a.Grants = accounts.Capped(a.Grants, r.caps)
				a.SessionEpoch = max(a.SessionEpoch, r.epoch)
			})
		}
		if err != nil {
			s.Log.Error("saving a revocation", "account", r.username, "err", err)
			continue
		}
		// Holding s.revoking, nothing added to it since it was listed.
		s.unsaved.forget(r.username, func(u *unsavedRevocation) { *u = unsavedRevocation{} })
	}
}
