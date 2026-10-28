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

// sweep makes links, credentials and shares follow the accounts: it deletes
// the shares whose sharer lost them, the links and credentials whose account
// is gone (also when one of the same name replaced it), and closes the
// connections credentials hold to environments their account may no longer
// use fully. It runs on every account change and once at startup, and first
// retries saving the revocations the accounts hold unsaved. Failed writes
// are logged; an orphaned share is refused (and held deleted) all the same.
func (s *Server) sweep() {
	if err := s.Accounts.Flush(); err != nil {
		s.Log.Error("saving the accounts' revocations", "err", err)
	}
	now := time.Now()
	// The deletion notifies accountsChanged, which closes the shares'
	// browser connections and sweeps again for their links and credentials.
	if _, err := s.Accounts.DeleteOrphans(now); err != nil {
		s.Log.Error("deleting shares whose sharer lost access", "err", err)
	}
	current := func(username, id string) bool {
		a, ok := s.Accounts.Get(username)
		return ok && a.ID == id
	}
	if err := s.links.retain(func(l link) bool { return current(l.Username, l.AccountID) }); err != nil {
		s.Log.Error("deleting login links of deleted accounts", "err", err)
	}
	if err := s.credentials.retain(func(c credential) bool { return current(c.Username, c.AccountID) }); err != nil {
		s.Log.Error("deleting client credentials of deleted accounts", "err", err)
	}
	for _, c := range s.credentials.list("") {
		a, ok := s.Accounts.Usable(c.Username, c.AccountID, now)
		level := levelOf(a, ok, now)
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
// ended (requests check the expiry).
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
	// Unsaved, the store holds them deleted all the same.
	if _, err := s.Accounts.DeleteWhere(func(a accounts.Account) bool { return a.Expired(now) }); err != nil {
		s.Log.Error("deleting expired accounts", "accounts", expired, "err", err)
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
