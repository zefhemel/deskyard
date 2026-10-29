package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/storage"
)

func (s *Server) routes() {
	m := http.NewServeMux()
	// Without a session.
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("POST /api/links/inspect", s.handleInspectLink)
	m.HandleFunc("POST /api/links/redeem", s.handleRedeemLink)
	m.HandleFunc("POST /api/cli-login", s.handleStartCLILogin)
	m.HandleFunc("POST /api/cli-login/{id}/token", s.handleCLILoginToken)

	h := func(pattern string, fn http.HandlerFunc) { m.Handle(pattern, s.authed(fn)) }
	h("GET /api/me", s.handleMe)
	h("POST /api/me/password", s.handleOwnPassword)
	h("POST /api/me/sign-out-everywhere", s.handleSignOutEverywhere)
	h("POST /api/me/webdav-password", s.handleCreateWebDAVPassword)
	h("DELETE /api/me/webdav-password", s.handleDeleteWebDAVPassword)
	h("GET /api/me/credentials", s.handleOwnCredentials)
	h("DELETE /api/me/credentials/{id}", s.handleDeleteOwnCredential)
	h("GET /api/cli-login/{id}", s.handleViewCLILogin)
	h("POST /api/cli-login/{id}/approve", s.handleApproveCLILogin)
	h("POST /api/cli-login/{id}/deny", s.handleDenyCLILogin)
	h("GET /api/host", s.handleHost)
	h("GET /api/events", s.handleEvents)
	h("GET /api/jobs/{id}", s.handleJob)
	h("POST /api/jobs/{id}/cancel", s.handleCancelJob)

	h("GET /api/vpn/connections", s.handleListVPN)
	h("POST /api/vpn/connections", s.handleCreateVPN)
	h("PUT /api/vpn/connections/{id}", s.handleUpdateVPN)
	h("DELETE /api/vpn/connections/{id}", s.handleDeleteVPN)
	h("POST /api/vpn/test", s.handleTestVPN)
	h("GET /api/environments", s.handleListComputers)
	h("POST /api/environments", s.handleCreateComputer)
	h("GET /api/validate-name", s.handleValidateName)
	h("GET /api/environments/{name}/terminal", s.handleTerminal)
	h("GET /api/environments/{name}/terminal/sessions", s.handleTerminalSessions)
	h("POST /api/environments/{name}/terminal/sessions", s.handleCreateTerminalSession)
	h("DELETE /api/environments/{name}/terminal/sessions/{session}", s.handleEndTerminalSession)
	h("GET /api/environments/{name}", s.handleGetComputer)
	h("PATCH /api/environments/{name}", s.handleUpdateComputer)
	h("DELETE /api/environments/{name}", s.handleRemoveComputer)
	h("POST /api/environments/{name}/reclaim-memory", s.handleReclaimMemory)
	h("PUT /api/environments/{name}/memory-policy", s.handleMemoryPolicy)
	h("PUT /api/environments/{name}/webdav", s.handleWebDAVSetting)
	h("POST /api/environments/{name}/{action}", s.handlePower) // start, stop, restart, upgrade, keys
	h("POST /api/environments/{name}/mic", s.handleMic)
	h("PUT /api/environments/{name}/access", s.handleSetAccess)
	h("GET /api/environments/{name}/shares", s.handleListShares)
	h("POST /api/environments/{name}/shares", s.handleCreateShare)
	h("POST /api/shares/{username}/link", s.handleShareLink)
	h("DELETE /api/shares/{username}", s.handleRevokeShare)
	h("GET /api/environments/{name}/screen.png", s.handleScreen)
	h("GET /api/environments/{name}/processes", s.handleProcesses)
	h("GET /api/environments/{name}/stats", s.handleStats)
	h("GET /api/environments/{name}/folder", s.handleFolder)
	h("GET /api/environments/{name}/activity", s.handleActivity)
	h("GET /api/environments/{name}/screen-events", s.handleScreenEvents)
	h("POST /api/environments/{name}/upload", s.handleUpload)

	// Remote access: client credentials only.
	remote := func(pattern string, fn remoteHandler) { m.Handle(pattern, s.remote(fn)) }
	remote("GET /api/environments/{name}/status", s.handleRemoteStatus)
	remote("GET /api/environments/{name}/exec", s.handleRemoteExec)
	remote("GET /api/environments/{name}/forward", s.handleRemoteForward)
	remote("GET /api/environments/{name}/cdp", s.handleRemoteCDPRoot)
	remote("/api/environments/{name}/cdp/{rest...}", s.handleRemoteCDP)

	h("GET /api/accounts", s.handleListAccounts)
	h("POST /api/accounts", s.handleCreateAccount)
	h("PATCH /api/accounts/{username}", s.handleUpdateAccount)
	h("DELETE /api/accounts/{username}", s.handleDeleteAccount)
	h("POST /api/accounts/{username}/link", s.handleAccountLink)
	h("GET /api/accounts/{username}/credentials", s.handleAccountCredentials)
	h("DELETE /api/accounts/{username}/credentials/{id}", s.handleDeleteAccountCredential)

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

	h("GET /api/remotes", s.handleListRemotes)
	h("POST /api/remotes", s.handleCreateRemote)
	h("POST /api/remotes/test", s.handleTestRemote)
	h("GET /api/remotes/{name}", s.handleGetRemote)
	h("PUT /api/remotes/{name}", s.handleUpdateRemote)
	h("DELETE /api/remotes/{name}", s.handleDeleteRemote)
	h("POST /api/remotes/{name}/test", s.handleTestRemote)
	h("GET /api/backups", s.handleBackups)
	h("POST /api/backups/repositories", s.handleCreateRepository)
	h("DELETE /api/backups/repositories/{name}", s.handleRemoveRepository)
	h("GET /api/backups/repositories/{name}/recovery-key", s.handleRecoveryKey)
	h("GET /api/backups/{kind}/{name}", s.handleGetBackup)
	h("PUT /api/backups/{kind}/{name}", s.handlePutBackup)
	h("DELETE /api/backups/{kind}/{name}", s.handleDeleteBackup)
	h("POST /api/backups/{kind}/{name}/run", s.handleRunBackup)
	h("GET /api/backups/{kind}/{name}/snapshots", s.handleSnapshots)
	h("GET /api/backups/{kind}/{name}/snapshots/{id}/files", s.handleSnapshotFiles)
	h("GET /api/backups/{kind}/{name}/snapshots/{id}/download", s.handleSnapshotDownload)
	h("POST /api/backups/{kind}/{name}/restore", s.handleRestore)

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such API route")
	})
	s.api = m
}

// authed requires a client credential (an Authorization bearer, which needs
// no Origin check: there is no ambient credential to forge) or a session and,
// for state changes, a same-origin Origin.
func (s *Server) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := bearerOf(r); ok {
			r, ok := s.withCredential(r, bearer)
			if !ok {
				writeError(w, http.StatusUnauthorized, "the credential is unknown, revoked or expired")
				return
			}
			next(w, r)
			return
		}
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
	Kind     string `json:"kind"`  // "password" or "token"
	Share    bool   `json:"share"` // made by Share
}

func toMeJSON(a accounts.Account) meJSON {
	return meJSON{Username: a.Username, Admin: a.Admin, Kind: accountKind(a), Share: a.IsShare()}
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
	// Token accounts have no password and answer as unknown ones do.
	a, ok := s.accountState(req.Username)
	if !ok || !a.HasPassword() || !auth.CheckPassword(a.PasswordHash, req.Password) {
		if !ok || !a.HasPassword() {
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
	s.replaceSession(w, r, a)
	s.Log.Info("signed in", "account", a.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, toMeJSON(a))
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
	writeJSON(w, http.StatusOK, toMeJSON(a))
}

func (s *Server) handleOwnPassword(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	var req struct{ Current, New string }
	if err := readJSON(r, &req); err != nil || req.New == "" {
		writeError(w, http.StatusBadRequest, "a new password is required")
		return
	}
	if !a.HasPassword() {
		writeError(w, http.StatusBadRequest, "token accounts have no password")
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
	s.reissueSession(w, r, a)
	w.WriteHeader(http.StatusNoContent)
}

// handleSignOutEverywhere ends every browser session of the account and
// signs this browser in again.
func (s *Server) handleSignOutEverywhere(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	s.revoking.Lock()
	defer s.revoking.Unlock()
	a, err := s.Accounts.BumpSessionEpoch(me.Username)
	if err != nil {
		// The other sessions end although the new epoch isn't saved; this
		// browser keeps its sign-in under it.
		if errors.Is(err, accounts.ErrNotSaved) {
			s.revokeUnsaved(unsavedRevocation{username: me.Username, id: me.ID, epoch: me.SessionEpoch + 1})
			if a, ok := s.accountState(me.Username); ok {
				s.reissueSession(w, r, a)
			}
		}
		writeError(w, errorStatus(err), err.Error())
		return
	}
	if err := s.removeDAVPasswords(a.ID); err != nil {
		s.Log.Warn("removing the WebDAV password", "account", a.Username, "err", err)
	}
	s.reissueSession(w, r, a)
	s.Log.Info("signed out everywhere", "account", a.Username, "ip", clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers for computer routes -------------------------------------------------------

// computerFor resolves {name} for the signed-in account, writing 404 when it
// doesn't exist or the account has no grant of it (at any level), and 403
// when the action isn't allowed: an observer may only watch.
func (s *Server) computerFor(w http.ResponseWriter, r *http.Request, action auth.Action) (computers.Computer, accounts.Account, bool) {
	a, _ := s.account(r)
	c, err := s.Computers.Get(r.Context(), r.PathValue("name"))
	if err != nil || !auth.Can(a, auth.Observe, c.ID) {
		writeError(w, http.StatusNotFound, "no such computer")
		return c, a, false
	}
	if !auth.Can(a, action, c.ID) {
		msg := "only an admin can do that"
		if a.Level(c.ID) == auth.LevelObserve && (action == auth.View || action == auth.Power || action == auth.Share) {
			msg = "observe-only access can't do that" // full access would allow it
		}
		writeError(w, http.StatusForbidden, msg)
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
		errors.Is(err, computers.ErrBusy), errors.Is(err, accounts.ErrLastAdmin), errors.Is(err, computers.ErrStopped),
		errors.Is(err, computers.ErrConflict), errors.Is(err, storage.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, accounts.ErrNotSaved):
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

func timeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// removalContext is a removal's: 5 minutes, also when its client leaves,
// as stopping it partway would leave the computer or volume forgotten
// and its files half deleted.
func removalContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
}
