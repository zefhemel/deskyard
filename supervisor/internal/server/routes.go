package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
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
	// Only for admins.
	admin := func(pattern string, fn http.HandlerFunc) { m.Handle(pattern, s.authed(s.adminOnly(fn))) }
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

	admin("GET /api/vpn/connections", s.handleListVPN)
	admin("POST /api/vpn/connections", s.handleCreateVPN)
	admin("PUT /api/vpn/connections/{id}", s.handleUpdateVPN)
	admin("DELETE /api/vpn/connections/{id}", s.handleDeleteVPN)
	admin("POST /api/vpn/test", s.handleTestVPN)
	admin("PUT /api/environments/{name}/network/services", s.handleNetworkServices)
	h("GET /api/environments", s.handleListEnvironments)
	admin("POST /api/environments", s.handleCreateEnvironment)
	admin("GET /api/validate-name", s.handleValidateName)
	h("GET /api/environments/{name}/terminal", s.handleTerminal)
	h("GET /api/environments/{name}/terminal/sessions", s.handleTerminalSessions)
	h("POST /api/environments/{name}/terminal/sessions", s.handleCreateTerminalSession)
	h("PATCH /api/environments/{name}/terminal/sessions/{session}", s.handleRenameTerminalSession)
	h("DELETE /api/environments/{name}/terminal/sessions/{session}", s.handleEndTerminalSession)
	h("GET /api/environments/{name}", s.handleGetEnvironment)
	h("PATCH /api/environments/{name}", s.handleUpdateEnvironment)
	h("DELETE /api/environments/{name}", s.handleRemoveEnvironment)
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
	remote("GET /api/environments/{name}/exec", whileRunning(s.remoteExec))
	remote("GET /api/environments/{name}/forward", whileRunning(s.remoteForward))
	remote("GET /api/environments/{name}/cdp", whileRunning(s.remoteCDP))
	remote("/api/environments/{name}/cdp/{rest...}", whileRunning(s.remoteCDP))

	h("GET /api/accounts", s.handleListAccounts)
	admin("POST /api/accounts", s.handleCreateAccount)
	admin("PATCH /api/accounts/{username}", s.handleUpdateAccount)
	admin("DELETE /api/accounts/{username}", s.handleDeleteAccount)
	admin("POST /api/accounts/{username}/link", s.handleAccountLink)
	admin("GET /api/accounts/{username}/credentials", s.handleAccountCredentials)
	admin("DELETE /api/accounts/{username}/credentials/{id}", s.handleDeleteAccountCredential)

	h("GET /api/images", s.handleListImages)
	admin("POST /api/images", s.handleAddImage)
	admin("POST /api/images/install", s.handleInstallImage)
	admin("DELETE /api/images", s.handleRemoveImage)
	admin("POST /api/images/check", s.handleCheckImages)
	admin("POST /api/images/upgrade", s.handleUpgradeImage)

	admin("GET /api/volumes", s.handleListVolumes)
	admin("POST /api/volumes", s.handleCreateVolume)
	admin("DELETE /api/volumes/{name}", s.handleDeleteVolume)
	admin("GET /api/volumes/{name}/usage", s.handleVolumeUsage)

	admin("GET /api/remotes", s.handleListRemotes)
	admin("POST /api/remotes", s.handleCreateRemote)
	admin("POST /api/remotes/test", s.handleTestRemote)
	admin("GET /api/remotes/{name}", s.handleGetRemote)
	admin("PUT /api/remotes/{name}", s.handleUpdateRemote)
	admin("DELETE /api/remotes/{name}", s.handleDeleteRemote)
	admin("POST /api/remotes/{name}/test", s.handleTestRemote)
	admin("GET /api/backups", s.handleBackups)
	admin("POST /api/backups/repositories", s.handleCreateRepository)
	admin("DELETE /api/backups/repositories/{name}", s.handleRemoveRepository)
	admin("GET /api/backups/repositories/{name}/recovery-key", s.handleRecoveryKey)
	admin("GET /api/backups/{kind}/{name}", s.handleGetBackup)
	admin("PUT /api/backups/{kind}/{name}", s.handlePutBackup)
	admin("DELETE /api/backups/{kind}/{name}", s.handleDeleteBackup)
	admin("POST /api/backups/{kind}/{name}/run", s.handleRunBackup)
	admin("GET /api/backups/{kind}/{name}/snapshots", s.handleSnapshots)
	admin("GET /api/backups/{kind}/{name}/snapshots/{id}/files", s.handleSnapshotFiles)
	admin("GET /api/backups/{kind}/{name}/snapshots/{id}/download", s.handleSnapshotDownload)
	admin("POST /api/backups/{kind}/{name}/restore", s.handleRestore)

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
	a, ok := s.Accounts.Get(req.Username)
	if !ok || !a.HasPassword() || !auth.CheckPassword(a.PasswordHash, req.Password) {
		if !ok || !a.HasPassword() {
			auth.CheckPassword(dummyHash, req.Password) // same time either way
		}
		s.Throttle.Fail(keys...)
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.Throttle.Succeed(keys...)
	s.signedIn(a, "password", r)
	s.replaceSession(w, r, a)
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
	a, err := s.Accounts.BumpSessionEpoch(me.Username)
	if err != nil {
		// The other sessions end although the new epoch isn't saved (the
		// store holds it); this browser keeps its sign-in under it.
		if a, ok := s.Accounts.Usable(me.Username, me.ID, time.Now()); ok && errors.Is(err, accounts.ErrNotSaved) {
			s.reissueSession(w, r, a)
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

// --- helpers for environment routes -------------------------------------------------------

// environmentFor resolves {name} for the signed-in account, writing 404 when it
// doesn't exist or the account has no grant of it (at any level), and 403
// when the action isn't allowed.
func (s *Server) environmentFor(w http.ResponseWriter, r *http.Request, action auth.Action) (environments.Environment, accounts.Account, bool) {
	a, _ := s.account(r)
	c, err := s.Environments.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, auth.ErrNoAccess.Error())
		return c, a, false
	}
	return c, a, allow(w, a, action, c.ID)
}

// allow reports whether auth.Check allows a the action on the named
// environment, writing why not when it doesn't: 404 for an environment a
// has no access to, as for one that doesn't exist, and 403 otherwise.
func allow(w http.ResponseWriter, a accounts.Account, action auth.Action, name string) bool {
	err := auth.Check(a, action, name)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrNoAccess):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusForbidden, err.Error())
	}
	return false
}

// adminOnly lets only admins through to next.
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a, _ := s.account(r); allow(w, a, auth.ManageAccounts, "") {
			next(w, r)
		}
	}
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, environments.ErrNotFound), errors.Is(err, accounts.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, environments.ErrExists), errors.Is(err, accounts.ErrExists),
		errors.Is(err, environments.ErrBusy), errors.Is(err, accounts.ErrLastAdmin), errors.Is(err, environments.ErrStopped),
		errors.Is(err, environments.ErrConflict), errors.Is(err, storage.ErrBusy):
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
// as stopping it partway would leave the environment or volume forgotten
// and its files half deleted.
func removalContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
}
