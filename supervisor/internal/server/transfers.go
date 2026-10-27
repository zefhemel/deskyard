package server

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// fileTransfer is a file upload in progress. It goes on only while
// its account may still use the environment fully, as at its start.
type fileTransfer struct {
	username    string
	accountID   string
	epoch       int // the session epoch of its browser login; noSession for a client credential
	environment string
	cancel      context.CancelFunc
}

type transfers struct {
	mu     sync.Mutex
	active map[*fileTransfer]bool
}

func newTransfers() *transfers {
	return &transfers{active: map[*fileTransfer]bool{}}
}

// revalidate closes the account's transfers it may no longer make.
func (t *transfers) revalidate(s *Server, username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for c := range t.active {
		if c.username == username && !s.transferAllowed(c) {
			c.cancel()
		}
	}
}

// closeAccount closes every transfer of the account, as when it should be
// gone but deleting it failed.
func (t *transfers) closeAccount(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for c := range t.active {
		if c.username == username {
			c.cancel()
		}
	}
}

func (s *Server) transferAllowed(c *fileTransfer) bool {
	a, ok := s.accountNow(c.username, c.accountID, time.Now())
	return ok && (c.epoch == noSession || a.SessionEpoch == c.epoch) && auth.Can(a, auth.View, c.environment)
}

// startTransfer registers a file transfer of the environment for the
// request's account. Its context ends when the client goes, and when the
// account may no longer use the environment fully (access lowered or
// removed, the account deleted, expired or its session ended) or the client
// credential it came with is deleted: the request's blocked reads and
// writes then fail too, so it can't stream on. done unregisters it.
func (s *Server) startTransfer(w http.ResponseWriter, r *http.Request, environment string) (ctx context.Context, done func()) {
	r, untrack := s.trackStream(r)
	ctx, cancel := context.WithCancel(r.Context())
	p, _ := s.principal(r)
	c := &fileTransfer{username: p.account.Username, accountID: p.account.ID, epoch: p.epoch(), environment: environment, cancel: cancel}
	s.transfers.mu.Lock()
	s.transfers.active[c] = true
	s.transfers.mu.Unlock()
	if !s.transferAllowed(c) { // revoked before it was registered
		cancel()
	}
	rc := http.NewResponseController(w)
	stop := context.AfterFunc(ctx, func() {
		now := time.Now()
		_ = rc.SetReadDeadline(now)
		_ = rc.SetWriteDeadline(now)
	})
	return ctx, func() {
		stop()
		s.transfers.mu.Lock()
		delete(s.transfers.active, c)
		s.transfers.mu.Unlock()
		cancel()
		untrack()
	}
}

// ctxWriter writes to w until ctx ends.
type ctxWriter struct {
	ctx context.Context
	w   io.Writer
}

func (c ctxWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.w.Write(p)
}
