package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
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
	h("POST /api/jobs/{id}/cancel", s.handleCancelJob)

	h("GET /api/vpn/connections", s.handleListVPN)
	h("POST /api/vpn/connections", s.handleCreateVPN)
	h("PUT /api/vpn/connections/{id}", s.handleUpdateVPN)
	h("DELETE /api/vpn/connections/{id}", s.handleDeleteVPN)
	h("POST /api/vpn/test", s.handleTestVPN)
	h("GET /api/environments", s.handleListEnvironments)
	h("POST /api/environments", s.handleCreateEnvironment)
	h("GET /api/validate-name", s.handleValidateName)
	h("GET /api/environments/{name}/terminal", s.handleTerminal)
	h("GET /api/environments/{name}/terminal/sessions", s.handleTerminalSessions)
	h("GET /api/environments/{name}", s.handleGetEnvironment)
	h("PATCH /api/environments/{name}", s.handleUpdateEnvironment)
	h("DELETE /api/environments/{name}", s.handleRemoveEnvironment)
	h("POST /api/environments/{name}/reclaim-memory", s.handleReclaimMemory)
	h("PUT /api/environments/{name}/memory-policy", s.handleMemoryPolicy)
	h("POST /api/environments/{name}/{action}", s.handlePower) // start, stop, restart, upgrade, keys
	h("POST /api/environments/{name}/mic", s.handleMic)
	h("PUT /api/environments/{name}/access", s.handleSetAccess)
	h("GET /api/environments/{name}/screen.png", s.handleScreen)
	h("GET /api/environments/{name}/processes", s.handleProcesses)
	h("GET /api/environments/{name}/stats", s.handleStats)
	h("GET /api/environments/{name}/folder", s.handleFolder)
	h("GET /api/environments/{name}/activity", s.handleActivity)
	h("GET /api/environments/{name}/screen-events", s.handleScreenEvents)
	h("GET /api/environments/{name}/files", s.handleFiles)
	h("GET /api/environments/{name}/download", s.handleDownload)
	h("POST /api/environments/{name}/upload", s.handleUpload)
	h("GET /api/environments/{name}/tokens", s.handleListTokens)
	h("POST /api/environments/{name}/tokens", s.handleCreateToken)
	h("DELETE /api/environments/{name}/tokens/{id}", s.handleDeleteToken)

	h("GET /api/accounts", s.handleListAccounts)
	h("POST /api/accounts", s.handleCreateAccount)
	h("PATCH /api/accounts/{username}", s.handleUpdateAccount)
	h("DELETE /api/accounts/{username}", s.handleDeleteAccount)

	h("GET /api/images", s.handleListImages)
	h("POST /api/images", s.handleAddImage)
	h("POST /api/images/install", s.handleInstallImage)
	h("DELETE /api/images", s.handleRemoveImage)
	h("POST /api/images/check", s.handleCheckImages)
	h("POST /api/images/upgrade", s.handleUpgradeImage)

	h("GET /api/volumes", s.handleListVolumes)
	h("POST /api/volumes", s.handleCreateVolume)
	h("DELETE /api/volumes/{name}", s.handleDeleteVolume)
	h("GET /api/volumes/{name}/usage", s.handleVolumeUsage)

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
	if err := s.Accounts.TouchLogin(a.Username); err != nil {
		s.Log.Warn("recording account login", "account", a.Username, "err", err)
	}
	s.Sessions.Issue(w, a.Username, a.SessionEpoch)
	s.Log.Info("signed in", "account", a.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, meJSON{a.Username, a.Admin})
}

var dummyHash = auth.HashPassword("dummy")

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.terminalOrigin(r) {
		writeError(w, 403, "cross-origin request refused")
		return
	}
	if claims, ok := s.Sessions.Claims(r); ok {
		s.terminals.logout(claims)
	}
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

// --- helpers for environment routes -------------------------------------------------------

// environmentFor resolves {name} for the signed-in account, writing 404 when it
// doesn't exist or the account can't see it, and 403 when the action isn't allowed.
func (s *Server) environmentFor(w http.ResponseWriter, r *http.Request, action auth.Action) (environments.Environment, accounts.Account, bool) {
	a, _ := s.account(r)
	c, err := s.Environments.Get(r.Context(), r.PathValue("name"))
	if err != nil || !auth.Can(a, auth.View, c.ID) {
		writeError(w, http.StatusNotFound, "no such environment")
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
	case errors.Is(err, environments.ErrNotFound), errors.Is(err, accounts.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, environments.ErrExists), errors.Is(err, accounts.ErrExists),
		errors.Is(err, environments.ErrBusy), errors.Is(err, accounts.ErrLastAdmin), errors.Is(err, environments.ErrStopped),
		errors.Is(err, environments.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func timeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
