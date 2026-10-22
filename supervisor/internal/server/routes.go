package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zefhemel/container-computer/supervisor/internal/accounts"
	"github.com/zefhemel/container-computer/supervisor/internal/auth"
	"github.com/zefhemel/container-computer/supervisor/internal/computers"
)

func (s *Server) routes() {
	m := http.NewServeMux()
	// Without a session.
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)

	h := func(pattern string, fn http.HandlerFunc) { m.Handle(pattern, s.authed(fn)) }
	h("GET /api/me", s.handleMe)
	h("POST /api/me/password", s.handleOwnPassword)
	h("GET /api/host", s.handleHost)
	h("GET /api/events", s.handleEvents)
	h("GET /api/jobs/{id}", s.handleJob)

	h("GET /api/computers", s.handleListComputers)
	h("POST /api/computers", s.handleCreateComputer)
	h("GET /api/validate-name", s.handleValidateName)
	h("GET /api/computers/{name}", s.handleGetComputer)
	h("PATCH /api/computers/{name}", s.handleUpdateComputer)
	h("DELETE /api/computers/{name}", s.handleRemoveComputer)
	h("POST /api/computers/{name}/{action}", s.handlePower) // start, stop, restart, upgrade, keys
	h("PUT /api/computers/{name}/access", s.handleSetAccess)
	h("GET /api/computers/{name}/screen.png", s.handleScreen)
	h("GET /api/computers/{name}/processes", s.handleProcesses)
	h("GET /api/computers/{name}/stats", s.handleStats)
	h("GET /api/computers/{name}/tokens", s.handleListTokens)
	h("POST /api/computers/{name}/tokens", s.handleCreateToken)
	h("DELETE /api/computers/{name}/tokens/{id}", s.handleDeleteToken)

	h("GET /api/accounts", s.handleListAccounts)
	h("POST /api/accounts", s.handleCreateAccount)
	h("PATCH /api/accounts/{username}", s.handleUpdateAccount)
	h("DELETE /api/accounts/{username}", s.handleDeleteAccount)

	h("GET /api/images", s.handleListImages)
	h("POST /api/images", s.handleAddImage)
	h("DELETE /api/images", s.handleRemoveImage)
	h("POST /api/images/check", s.handleCheckImages)
	h("POST /api/images/upgrade", s.handleUpgradeImage)

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such API route")
	})
	s.api = m
}

// authed requires a session, and a same-origin Origin for state changes.
func (s *Server) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, ok := s.withSession(w, r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		next(w, r)
	})
}

// --- sign in -----------------------------------------------------------------------------

type meJSON struct {
	Username string `json:"username"`
	Admin    bool   `json:"admin"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request refused")
		return
	}
	var req struct{ Username, Password string }
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	keys := []string{"u:" + req.Username, "ip:" + clientIP(r)}
	if !s.Throttle.Allow(keys...) {
		writeError(w, http.StatusTooManyRequests, "too many attempts: wait a few seconds")
		return
	}
	a, ok := s.Accounts.Get(req.Username)
	if !ok || !auth.CheckPassword(a.PasswordHash, req.Password) {
		if !ok {
			auth.CheckPassword(dummyHash, req.Password) // same time either way
		}
		s.Throttle.Fail(keys...)
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.Throttle.Succeed(keys...)
	s.Accounts.TouchLogin(a.Username)
	s.Sessions.Issue(w, a.Username, a.SessionEpoch)
	s.Log.Info("signed in", "account", a.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, meJSON{a.Username, a.Admin})
}

var dummyHash = auth.HashPassword("dummy")

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	writeJSON(w, http.StatusOK, meJSON{a.Username, a.Admin})
}

func (s *Server) handleOwnPassword(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	var req struct{ Current, New string }
	if err := readJSON(r, &req); err != nil || req.New == "" {
		writeError(w, http.StatusBadRequest, "a new password is required")
		return
	}
	if !auth.CheckPassword(a.PasswordHash, req.Current) {
		writeError(w, http.StatusForbidden, "the current password is wrong")
		return
	}
	if err := s.Accounts.SetPassword(a.Username, req.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, _ = s.Accounts.Get(a.Username)
	s.Sessions.Issue(w, a.Username, a.SessionEpoch)
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers for computer routes -----------------------------------------------------------

// computerFor resolves {name} for the signed-in account, writing 404 when it
// doesn't exist or the account can't see it, and 403 when the action isn't allowed.
func (s *Server) computerFor(w http.ResponseWriter, r *http.Request, action auth.Action) (computers.Computer, accounts.Account, bool) {
	a, _ := s.account(r)
	c, err := s.Computers.Get(r.Context(), r.PathValue("name"))
	if err != nil || !auth.Can(a, auth.View, c.ID) {
		writeError(w, http.StatusNotFound, "no such computer")
		return c, a, false
	}
	if !auth.Can(a, action, c.ID) {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return c, a, false
	}
	return c, a, true
}

func requireAdmin(w http.ResponseWriter, a accounts.Account) bool {
	if !a.Admin {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return false
	}
	return true
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, computers.ErrNotFound), errors.Is(err, accounts.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, computers.ErrExists), errors.Is(err, accounts.ErrExists),
		errors.Is(err, computers.ErrBusy), errors.Is(err, accounts.ErrLastAdmin), errors.Is(err, computers.ErrStopped):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func timeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
