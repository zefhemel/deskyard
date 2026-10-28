package server

import (
	"context"
	"net/http"
	"time"
)

// startTransfer registers a file transfer of the environment for the
// request's account. Its context ends when the client goes, and when the
// account may no longer use the environment fully (access lowered or
// removed, the account deleted, expired or its session ended) or the client
// credential it came with is deleted: the request's blocked reads and
// writes then fail too, so it can't stream on. done unregisters it.
func (s *Server) startTransfer(w http.ResponseWriter, r *http.Request, environment string) (ctx context.Context, done func()) {
	ctx, cancel := context.WithCancel(r.Context())
	unregister, ok := s.open(s.connOf(r, environment, cancel))
	if !ok { // revoked before it was registered
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
		unregister()
		cancel()
	}
}
