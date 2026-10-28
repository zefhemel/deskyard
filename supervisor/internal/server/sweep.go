package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// levelOf is how far the account (ok: it exists) may use the named
// environment now; an expired account may use nothing.
func levelOf(a accounts.Account, ok bool, now time.Time) func(environment string) auth.Level {
	return func(environment string) auth.Level {
		if !ok || a.Expired(now) {
			return auth.LevelNone
		}
		return a.Level(environment)
	}
}

// accountNow is the named account as it stands now (with the revocations
// that couldn't be saved), while it is still the one with id and may use
// anything: it hasn't expired and isn't a share whose sharer lost it, as
// when deleting it failed. Requests and the connections
// they hold check it, so revocation holds even when persisting it failed.
func (s *Server) accountNow(username, id string, now time.Time) (accounts.Account, bool) {
	a, ok := s.accountState(username)
	if !ok || a.ID != id || a.Expired(now) {
		return accounts.Account{}, false
	}
	if a.SharedBy != nil {
		sharers := map[string]accounts.Account{}
		if sharer, ok := s.accountState(a.SharedBy.Username); ok {
			sharers[sharer.Username] = sharer
		}
		if orphanedShare(a, sharers, now) {
			return accounts.Account{}, false
		}
	}
	return a, true
}

// orphanedShare reports whether share has lost its reason to exist: its
// environment is gone (it has no grant), or its sharer (by ID, from
// sharers) is gone, expired or no longer full on the environment. Admins
// are full everywhere, so their shares stay while they are admins.
func orphanedShare(share accounts.Account, sharers map[string]accounts.Account, now time.Time) bool {
	if share.SharedBy == nil {
		return false
	}
	g, ok := share.ShareGrant()
	if !ok {
		return true
	}
	sharer, ok := sharers[share.SharedBy.Username]
	return !ok || sharer.ID != share.SharedBy.ID || levelOf(sharer, true, now)(g.Environment) != auth.LevelFull
}

// sweep makes links, credentials and shares follow the accounts: it deletes
// the shares whose sharer lost them, the links and credentials whose account
// is gone (also when one of the same name replaced it), and closes the
// connections credentials hold to environments their account may no longer
// use fully. It runs on every account change and once at startup, and first
// retries saving the revocations that couldn't be saved. Failed writes are
// logged and the connections are closed anyway.
func (s *Server) sweep() {
	s.retryUnsaved()
	now := time.Now()
	byName := map[string]accounts.Account{}
	for _, a := range s.accountStates() {
		byName[a.Username] = a
	}
	// The deletion notifies accountsChanged, which closes the shares'
	// browser connections and sweeps again for their links and credentials.
	if _, err := s.Accounts.DeleteWhere(func(a accounts.Account) bool { return orphanedShare(a, byName, now) }); err != nil {
		s.Log.Error("deleting shares whose sharer lost access", "err", err)
		for _, a := range byName {
			if orphanedShare(a, byName, now) {
				s.closeAccount(a.Username)
			}
		}
	}
	current := func(username, id string) bool {
		a, ok := s.accountState(username)
		return ok && a.ID == id
	}
	if err := s.links.retain(func(l link) bool { return current(l.Username, l.AccountID) }); err != nil {
		s.Log.Error("deleting login links of deleted accounts", "err", err)
	}
	if err := s.credentials.retain(func(c credential) bool { return current(c.Username, c.AccountID) }); err != nil {
		s.Log.Error("deleting client credentials of deleted accounts", "err", err)
	}
	for _, c := range s.credentials.list("") {
		a, ok := s.accountState(c.Username)
		level := levelOf(a, ok && a.ID == c.AccountID && !orphanedShare(a, byName, now), now)
		s.credentials.closeWhere(c.ID, func(environment string) bool { return environment != "" && level(environment) != auth.LevelFull })
	}
}

// closeAccount closes every live connection of the account, when it should
// be gone but deleting it failed.
func (s *Server) closeAccount(username string) {
	none := func(string) auth.Level { return auth.LevelNone }
	s.viewers.revoke(username, none)
	s.screenWatchers.revoke(username, none)
	s.terminals.closeAccount(username)
	s.transfers.closeAccount(username)
	for _, c := range s.credentials.list("") {
		if c.Username == username {
			s.credentials.closeWhere(c.ID, func(string) bool { return true })
		}
	}
}

// expireLoop deletes accounts as they expire, which ends their access, and
// retries the sweep, whose deletions may have failed.
func (s *Server) expireLoop(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		s.expireAccounts(time.Now())
		s.sweep()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// expireAccounts deletes the accounts expired by now. Their access already
// ended (requests check the expiry); when the deletion fails their
// connections are closed anyway.
func (s *Server) expireAccounts(now time.Time) {
	var expired []string
	for _, a := range s.Accounts.List() {
		if a.Expired(now) {
			expired = append(expired, a.Username)
		}
	}
	if len(expired) == 0 {
		return
	}
	if _, err := s.Accounts.DeleteWhere(func(a accounts.Account) bool { return a.Expired(now) }); err != nil {
		s.Log.Error("deleting expired accounts", "accounts", expired, "err", err)
		s.accountsChanged(expired...) // expired accounts may use nothing
	}
}

// removeTokensFile deletes the access tokens of earlier versions: accounts,
// login links and client credentials replace them, and they aren't migrated.
func (s *Server) removeTokensFile() error {
	err := os.Remove(filepath.Join(s.Layout.Data, "tokens.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil {
		s.Log.Info("removed tokens.json: access tokens are replaced by accounts")
	}
	return err
}
