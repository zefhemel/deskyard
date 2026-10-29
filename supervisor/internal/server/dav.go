package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/davfs"
)

// WebDAV at /<name>/dav/: the computer's home and local volumes, for
// Finder and other WebDAV clients, while the computer runs or not. Only
// HTTP Basic auth is accepted, never the browser session: the account's
// password, or its WebDAV password when it has one. Files are always served
// as downloads, so nothing from a desktop renders on this origin.

const (
	davKind  = "webdav" // the client credential kind of WebDAV passwords
	davRealm = `Basic realm="Deskyard", charset="UTF-8"`
	// davPasswordTTL is how long a checked account password is remembered:
	// clients send it with every request, and checking it costs an argon2id
	// hash.
	davPasswordTTL = 5 * time.Minute
	// davMaxLock caps how long a WebDAV lock lasts without a refresh, so a
	// client that forgets one doesn't block the others' writes for good.
	davMaxLock = "Second-3600"
)

// davAllowed reports whether a may use name's files over WebDAV: a password
// account, not a share, with full use of the computer.
func davAllowed(a accounts.Account, name string) bool {
	return a.HasPassword() && !a.IsShare() && auth.Can(a, auth.View, name)
}

func (s *Server) serveDAV(w http.ResponseWriter, r *http.Request, name string) {
	a, cred, ok := s.davAccount(w, r)
	if !ok {
		return
	}
	c, err := s.Computers.Get(r.Context(), name)
	if err != nil || !c.WebDAV || !davAllowed(a, c.Name) {
		http.NotFound(w, r) // the same for missing, invisible and switched off
		return
	}
	files, err := davfs.New(s.davMounts(c))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer files.Close()

	prefix := "/" + c.Name + "/dav"
	if !s.davWritable(r, files, prefix) {
		http.Error(w, "read-only", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if cred != nil {
		defer s.credentials.track(cred.ID, c.Name, cancel)()
	}
	r = r.WithContext(ctx)
	switch r.Method {
	case "LOCK":
		if t := r.Header.Get("Timeout"); t == "" || davLockTooLong(t) {
			r.Header.Set("Timeout", davMaxLock)
		}
	case "COPY", "MOVE":
		// As the handler reads Overwrite: a COPY overwrites unless told not to, a MOVE only when told to.
		if overwrite := r.Header.Get("Overwrite"); r.Method == "COPY" && overwrite != "F" || overwrite == "T" {
			// An existing destination is set aside, and put back if the copy or move fails.
			if u, err := url.Parse(r.Header.Get("Destination")); err == nil && strings.HasPrefix(u.Path, prefix) {
				dest := strings.TrimPrefix(u.Path, prefix)
				ctx, done := davfs.WithReplacement(r.Context(), dest)
				r = r.WithContext(ctx)
				rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
				w = rec
				defer func() {
					if err := done(rec.code < 300); err != nil {
						s.Log.Warn("webdav: settling a replaced destination", "path", dest, "err", err)
					}
				}()
			}
		}
	case http.MethodGet, http.MethodHead:
		h := w.Header()
		h.Set("Content-Disposition", "attachment")
		h.Set("Content-Security-Policy", "sandbox")
		h.Set("X-Content-Type-Options", "nosniff")
	case http.MethodPut:
		ctx, body := davfs.WithUpload(r.Context(), r.Body)
		r = r.WithContext(ctx)
		r.Body = body
	}
	h := &webdav.Handler{
		Prefix: prefix, FileSystem: files, LockSystem: s.davLocks.of(c.Name),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				s.Log.Debug("webdav", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}
	h.ServeHTTP(w, r)
}

// davLockTooLong reports whether a LOCK's Timeout header asks for longer
// than davMaxLock (or forever).
func davLockTooLong(t string) bool {
	for _, v := range strings.Split(t, ",") {
		v = strings.TrimSpace(v)
		if s, ok := strings.CutPrefix(v, "Second-"); ok {
			n, err := strconv.ParseUint(s, 10, 32)
			return err != nil || n > 3600
		}
		if v != "" {
			return true // Infinite, or nothing we read
		}
	}
	return true
}

// statusRecorder notes the status a handler answered with.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// davMounts is the computer's tree: its home, and its local volumes at
// their paths. Host volumes are outside the data folder and left out.
func (s *Server) davMounts(c computers.Computer) []davfs.Mount {
	mounts := []davfs.Mount{{At: "/workspace", Folder: s.Computers.Layout.HomeOf(c.Name)}}
	for _, g := range c.Settings.Volumes {
		v, ok := s.Computers.Vols.Get(g.Name)
		if !ok || v.Kind == computers.VolumeHost {
			continue
		}
		mounts = append(mounts, davfs.Mount{At: computers.GrantPath(g), Folder: s.Computers.Layout.VolumeOf(g.Name), ReadOnly: g.ReadOnly})
	}
	return mounts
}

// davWritable refuses, before the WebDAV handler sees it, a change to a
// read-only or synthesized place, which the handler would answer with 404
// or 405 and Finder show as an I/O error.
func (s *Server) davWritable(r *http.Request, files *davfs.FS, prefix string) bool {
	target := strings.TrimPrefix(r.URL.Path, prefix)
	switch r.Method {
	case http.MethodPut, "MKCOL", http.MethodDelete, "PROPPATCH":
		return files.Writable(target)
	case "LOCK":
		// Clients lock what they write; not mounts, folders above them or
		// read-only places, where a lock would only get in others' way.
		return files.Writable(target)
	case "MOVE", "COPY":
		if r.Method == "MOVE" && !files.Writable(target) {
			return false
		}
		u, err := url.Parse(r.Header.Get("Destination"))
		if err != nil {
			return true // the handler refuses it
		}
		dest, ok := strings.CutPrefix(u.Path, prefix)
		if !ok {
			return true // the handler refuses it
		}
		// A dropped macOS file can't stand in for a real one.
		return files.Writable(dest) && (!davfs.Dropped(target) || davfs.Dropped(dest))
	}
	return true
}

// davAccount authenticates a WebDAV request with Basic auth, writing 401
// (with a challenge, so clients ask) or 429 when it fails. cred is the
// WebDAV password used, nil for the account password.
func (s *Server) davAccount(w http.ResponseWriter, r *http.Request) (accounts.Account, *credential, bool) {
	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", davRealm)
		http.Error(w, "sign in with your Deskyard username and password", http.StatusUnauthorized)
		return accounts.Account{}, nil, false
	}
	keys := []string{"u:" + username, "ip:" + clientIP(r)}
	if !s.Throttle.Allow(keys...) {
		http.Error(w, "too many attempts: wait a few seconds", http.StatusTooManyRequests)
		return accounts.Account{}, nil, false
	}
	a, cred, ok := s.davCheck(username, password)
	if !ok {
		s.Throttle.Fail(keys...)
		w.Header().Set("WWW-Authenticate", davRealm)
		http.Error(w, "wrong username or password", http.StatusUnauthorized)
		return accounts.Account{}, nil, false
	}
	s.Throttle.Succeed(keys...)
	return a, cred, true
}

func (s *Server) davCheck(username, password string) (accounts.Account, *credential, bool) {
	a, found := s.accountState(username)
	if found {
		a, found = s.accountNow(a.Username, a.ID, time.Now())
	}
	if strings.HasPrefix(password, credentialPrefix) {
		if c, ok := s.credentials.check(password); ok && found && c.Kind == davKind && c.AccountID == a.ID {
			return a, &c, true
		}
	}
	if !found || !a.HasPassword() {
		auth.CheckPassword(dummyHash, password) // same time either way
		return accounts.Account{}, nil, false
	}
	if s.hasDAVPassword(a.ID) {
		auth.CheckPassword(dummyHash, password) // same time as checking it
		return accounts.Account{}, nil, false   // the WebDAV password replaces the account's
	}
	return a, nil, s.davPasswords.check(a, password)
}

func (s *Server) hasDAVPassword(accountID string) bool {
	for _, c := range s.credentials.list(accountID) {
		if c.Kind == davKind {
			return true
		}
	}
	return false
}

// davPasswordCache remembers account passwords that checked out, by a keyed
// hash, for as long as the account's password hash and session epoch stay
// the same and at most davPasswordTTL.
type davPasswordCache struct {
	mu   sync.Mutex
	key  []byte
	seen map[string]davPasswordSeen
	now  func() time.Time
}

type davPasswordSeen struct {
	passwordHash string
	epoch        int
	until        time.Time
}

func newDAVPasswordCache() *davPasswordCache {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &davPasswordCache{key: key, seen: map[string]davPasswordSeen{}, now: time.Now}
}

func (pc *davPasswordCache) check(a accounts.Account, password string) bool {
	mac := hmac.New(sha256.New, pc.key)
	mac.Write([]byte(a.ID + "\x00" + password))
	k := string(mac.Sum(nil))
	now := pc.now()
	pc.mu.Lock()
	e, ok := pc.seen[k]
	pc.mu.Unlock()
	if ok && e.passwordHash == a.PasswordHash && e.epoch == a.SessionEpoch && now.Before(e.until) {
		return true
	}
	if !auth.CheckPassword(a.PasswordHash, password) {
		return false
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for k, e := range pc.seen {
		if !now.Before(e.until) {
			delete(pc.seen, k)
		}
	}
	pc.seen[k] = davPasswordSeen{passwordHash: a.PasswordHash, epoch: a.SessionEpoch, until: now.Add(davPasswordTTL)}
	return true
}

// davLockSystems holds each computer's WebDAV locks, in memory.
type davLockSystems struct {
	mu  sync.Mutex
	all map[string]webdav.LockSystem
}

func (l *davLockSystems) of(name string) webdav.LockSystem {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.all == nil {
		l.all = map[string]webdav.LockSystem{}
	}
	if l.all[name] == nil {
		l.all[name] = webdav.NewMemLS()
	}
	return l.all[name]
}

func (l *davLockSystems) forget(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.all, name)
}

// --- API -----------------------------------------------------------------------------------

// handleWebDAVSetting switches WebDAV for a computer, without touching
// its container. Switching it off drops its locks.
func (s *Server) handleWebDAVSetting(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		WebDAV *bool `json:"webdav"`
	}
	if err := readJSON(r, &req); err != nil || req.WebDAV == nil {
		writeError(w, http.StatusBadRequest, "webdav must be a boolean")
		return
	}
	if err := s.Computers.SetWebDAV(c.Name, *req.WebDAV); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	if !*req.WebDAV {
		s.davLocks.forget(c.Name)
	}
	s.publishComputer(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateWebDAVPassword makes the account's WebDAV password, replacing
// the one it had, and shows it this once. From then on WebDAV no longer
// takes the account password.
func (s *Server) handleCreateWebDAVPassword(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !me.HasPassword() || me.IsShare() {
		writeError(w, http.StatusForbidden, "only accounts with a password can use WebDAV")
		return
	}
	c, password, err := s.credentials.create(me, davKind, "WebDAV password")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.credentials.remove(func(o credential) bool {
		return o.AccountID == me.ID && o.Kind == davKind && o.ID != c.ID
	}); err != nil {
		s.Log.Warn("removing the old WebDAV password", "account", me.Username, "err", err)
	}
	s.Log.Info("WebDAV password set", "account", me.Username, "ip", clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]string{"password": password})
}

// handleDeleteWebDAVPassword removes the WebDAV password: WebDAV takes the
// account password again.
func (s *Server) handleDeleteWebDAVPassword(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if err := s.removeDAVPasswords(me.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeDAVPasswords(accountID string) error {
	return s.credentials.remove(func(c credential) bool { return c.AccountID == accountID && c.Kind == davKind })
}
