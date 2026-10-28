package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
)

// sweep makes shares and live connections follow the accounts: it deletes
// the shares whose sharer lost them, and closes the live connections their
// accounts no longer allow (links and credentials go with their account).
// It runs on every account change, every expiry tick and once at startup,
// and first retries saving the revocations the accounts hold unsaved.
// Failed writes are logged; an orphaned share is refused (and held
// deleted) all the same.
func (s *Server) sweep() {
	if err := s.Accounts.Flush(); err != nil {
		s.Log.Error("saving the accounts' revocations", "err", err)
	}
	now := time.Now()
	// The deletion notifies accountsChanged, which closes the shares'
	// connections.
	if _, err := s.Accounts.DeleteOrphans(now); err != nil {
		s.Log.Error("deleting shares whose sharer lost access", "err", err)
	}
	// Shares orphaned or expired by others' changes, and accounts expired
	// by time, may have connections of their own.
	s.revalidate()
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
