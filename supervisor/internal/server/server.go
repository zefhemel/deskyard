// Package server is the supervisor's HTTP side: the UI and its API, the
// desktop proxy, and the agent API. API.md in the module root is the contract.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/storage"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

type Server struct {
	Log          *slog.Logger
	Eng          engine.Engine
	Environments *environments.Service
	Accounts     *accounts.Store
	Sessions     *auth.Sessions
	Throttle     *auth.Throttle
	Layout       config.Layout
	UI           fs.FS // the built web UI (index.html, assets/…)

	vpnTests  vpnTester
	jobs      *jobs
	events    *hub
	viewers   *viewers
	terminals *terminalConnections
	// terminalSessions caches running environments' shared terminal sessions.
	terminalSessions *terminalSessions
	forwards         *forwards
	viewersPub       *throttle
	streamDials      *dialGates
	clients          *clientFiles
	stats            *statsCollector
	inside           *insideReader
	thumbs           *thumbnails
	credentials      *credentialStore
	links            *linkStore
	cliLogins        *cliLogins // pending deskyard logins
	images           *catalog
	storage          *storage.Service // storage remotes and backups
	reconcile        chan struct{}    // wakes the environments' reconcile loop

	memoryReclaim engine.MemoryReclaimSupport

	activity       *activity.Tracker
	activityPub    *throttle
	screenWatchers *viewers   // screen-events streams, closed on revoke like viewers
	transfers      *transfers // file uploads, closed on revoke
	unsaved        *unsavedRevocations
	// revoking is held by a change that decides an account's access or
	// sessions, from its save to holding or forgetting what couldn't be
	// saved, and by the sweep saving those; neither undoes the other.
	revoking sync.Mutex
	seen     *lastSeen // browser requests, by account ID

	api *http.ServeMux
}

type Options struct {
	Log          *slog.Logger
	Eng          engine.Engine
	Environments *environments.Service
	Accounts     *accounts.Store
	Layout       config.Layout // the data folder; stores at its top, secrets, journals and caches below
	UI           fs.FS
	Secure       bool // served over HTTPS
	DefaultImage string

	// StorageRunner runs rclone and restic; nil uses helper containers on Eng.
	StorageRunner    storage.Runner
	ActivityPresence time.Duration   // how long an agent stays after its last action (0: 5 minutes)
	StartupContext   context.Context // optional; bounds capability detection with the startup lifetime
}

func New(o Options) (*Server, error) {
	connections, err := vpn.NewStore(filepath.Join(o.Layout.Data, "networks.json"), o.Layout.Secret)
	if err != nil {
		return nil, err
	}
	manager := vpn.NewManager(o.Eng, connections, o.Layout.State).ForInstance(o.Environments.Instance)
	if o.Environments.Networking == nil {
		if err := o.Environments.ConfigureNetworking(connections, manager, o.Layout.State); err != nil {
			return nil, err
		}
	}
	key, err := sessionKey(o.Layout.Secret)
	if err != nil {
		return nil, err
	}
	s := &Server{
		vpnTests: manager,
		Log:      o.Log, Eng: o.Eng, Environments: o.Environments, Accounts: o.Accounts,
		Sessions: &auth.Sessions{Key: key, Secure: o.Secure}, Throttle: &auth.Throttle{},
		Layout: o.Layout, UI: o.UI,
		jobs: newJobs(), events: newHub(), viewers: newViewers(), terminals: newTerminalConnections(), forwards: newForwards(), viewersPub: newThrottle(viewersInterval), streamDials: newDialGates(),
		clients:     &clientFiles{eng: o.Eng, dir: filepath.Join(o.Layout.Cache, "client")},
		activityPub: newThrottle(activityInterval), screenWatchers: newViewers(), transfers: newTransfers(), cliLogins: newCLILogins(), unsaved: newUnsavedRevocations(), seen: &lastSeen{at: map[string]time.Time{}},
		reconcile: make(chan struct{}, 1),
	}
	s.activity = s.newActivity(o.ActivityPresence)
	s.terminalSessions = newTerminalSessions(s)
	s.stats = newStatsCollector(s)
	s.thumbs = newThumbnails(s)
	if err := s.removeTokensFile(); err != nil {
		return nil, err
	}
	if s.credentials, err = loadCredentials(o.Layout.Data); err != nil {
		return nil, err
	}
	if s.links, err = loadLinks(o.Layout.Data); err != nil {
		return nil, err
	}
	// A CLI redemption that stopped between saving its credential and
	// deleting its link left the credential, whose bearer it never handed
	// out; the link still works.
	if err := s.credentials.remove(func(c credential) bool { return c.Link != "" && s.links.has(c.Link) }); err != nil {
		o.Log.Error("deleting credentials of unfinished link redemptions", "err", err)
	}
	// Grants of names without a definition are left over from a
	// removal whose cleanup didn't persist; a later environment of the name
	// must not inherit them.
	if err := s.Accounts.Retain(func(name string) bool { _, ok := s.Environments.Defs.Get(name); return ok }); err != nil {
		return nil, fmt.Errorf("dropping grants of removed environments: %w", err)
	}
	s.credentials.onGone = func(c credential) { s.activity.Forget(c.ID) }
	if s.images, err = loadCatalog(o.Layout.Data, o.DefaultImage); err != nil {
		return nil, err
	}
	s.Environments.ImageUsed = func(ref, id string) {
		if err := s.images.noteLocal(ref, id); err != nil {
			s.Log.Warn("recording local image", "ref", ref, "err", err)
		}
	}
	run := o.StorageRunner
	if run == nil {
		helpers := &storage.EngineRunner{Eng: o.Eng, BinDir: filepath.Join(o.Layout.Cache, "storage"), Instance: o.Environments.Instance}
		run = helpers
	}
	if s.storage, err = storage.Open(o.Layout, o.Environments.Instance, run, storage.EnvironmentsOf(o.Environments), o.Log); err != nil {
		return nil, err
	}
	s.Environments.ItemBusy = s.storage.Busy
	// As creating one does: what a removal of the name failed to purge
	// doesn't carry over to an environment restored under it.
	s.storage.ForgetName = func(name string) error {
		if err := s.forgetAccess(name); err != nil {
			return err
		}
		s.activity.ForgetDesktop(name)
		return nil
	}
	s.Environments.ConflictsChanged = s.events.refresh
	s.Environments.Removed = s.environmentRemoved
	// An environment whose pinned image was pulled again has a new revision.
	s.Environments.Repinned = func([]environments.Repin) { s.events.refresh() }
	o.Accounts.OnChange = s.accountsChanged
	o.Accounts.HeldSessionEpoch = func(a accounts.Account) int {
		a, _ = s.unsaved.apply(a)
		return a.SessionEpoch
	}
	s.sweep() // links, credentials and shares left over from changes that didn't persist
	s.detectMemoryReclaim(o.StartupContext)
	s.routes()
	return s, nil
}

// Run starts the background work: engine events, stats, update checks.
func (s *Server) Run(ctx context.Context) {
	context.AfterFunc(ctx, s.terminals.shutdown)
	s.inside = newInsideReader(s)
	go s.Environments.Networking.Run(ctx, func(name string) { s.publishEnvironment(ctx, name) })
	go s.watchEngine(ctx)
	go s.Environments.Run(ctx, s.reconcile)
	go s.stats.run(ctx)
	go s.inside.run(ctx)
	go s.expireLoop(ctx)
	go s.images.checkLoop(ctx, s)
	go s.activityLoop(ctx)
	go s.idleMemoryLoop(ctx)
	go s.terminalSessions.run(ctx)
	go s.storage.RunScheduler(ctx)
}

func sessionKey(secretDir string) ([]byte, error) {
	p := filepath.Join(secretDir, "session.key")
	b, err := os.ReadFile(p)
	if err == nil && len(b) >= 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	_, _ = rand.Read(b)
	return b, store.WriteFile(p, b)
}

// ServeHTTP dispatches on the first path segment: the API, the UI's assets
// and pages, or a desktop's desktop (/<name>/…).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path := r.URL.Path
	first, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	switch {
	case path == "/healthz":
		w.WriteHeader(http.StatusNoContent)
	case first == "api":
		s.api.ServeHTTP(w, r)
	case first == "assets" || strings.Contains(first, "."):
		s.serveStatic(w, r)
	case first == "" || environments.IsReserved(first):
		s.serveIndex(w, r)
	default:
		s.serveDesktop(w, r, first, rest)
	}
}

// --- sessions -----------------------------------------------------------------

type ctxKey struct{}

// principal is who a request acts as: an account, signed in either by a
// browser session (claims) or by a client credential.
type principal struct {
	account    accounts.Account
	credential *credential
	claims     *auth.SessionClaims
}

func (s *Server) principal(r *http.Request) (principal, bool) {
	p, ok := r.Context().Value(ctxKey{}).(principal)
	return p, ok
}

// epoch is the session epoch of the request's browser login, or noSession
// for a client credential.
func (p principal) epoch() int {
	if p.claims == nil {
		return noSession
	}
	return p.claims.Epoch
}

// lastSeen remembers, in memory, when each account's browser last made a
// request.
type lastSeen struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (l *lastSeen) touch(accountID string, t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.After(l.at[accountID]) {
		l.at[accountID] = t
	}
}

func (l *lastSeen) get(accountID string) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.at[accountID]
}

// signedIn reports whether the request's sign-in still holds for a, the
// account as it is now: a browser login ends when a's session epoch moves
// on. A credential's revocation closes what it opened (trackStream).
func (p principal) signedIn(a accounts.Account) bool {
	return p.epoch() == noSession || p.epoch() == a.SessionEpoch
}

// trackStream registers a long-lived stream opened with a client credential
// under it, so deleting the credential closes it; the stream checks its
// account's access itself. The returned request carries the stream's
// context, and the function unregisters it.
func (s *Server) trackStream(r *http.Request) (*http.Request, func()) {
	c, ok := s.credentialOf(r)
	if !ok {
		return r, func() {}
	}
	ctx, cancel := context.WithCancel(r.Context())
	untrack := s.credentials.track(c.ID, "", cancel)
	if !s.credentials.has(c.ID) { // deleted before it was registered
		cancel()
	}
	return r.WithContext(ctx), func() {
		untrack()
		cancel()
	}
}

// account returns the signed-in account of the request, if any.
func (s *Server) account(r *http.Request) (accounts.Account, bool) {
	p, ok := s.principal(r)
	return p.account, ok
}

// credentialOf returns the client credential the request was made with, if
// it was.
func (s *Server) credentialOf(r *http.Request) (credential, bool) {
	p, ok := s.principal(r)
	if !ok || p.credential == nil {
		return credential{}, false
	}
	return *p.credential, true
}

// issueSession signs this browser in to a, until a expires at the latest.
func (s *Server) issueSession(w http.ResponseWriter, a accounts.Account) {
	s.Sessions.IssueUntil(w, a.Username, a.ID, a.SessionEpoch, expiryOf(a))
}

// replaceSession signs this browser in to a. A browser login of another
// account it replaces ends as on logout, closing its terminals.
func (s *Server) replaceSession(w http.ResponseWriter, r *http.Request, a accounts.Account) {
	if old, ok := s.Sessions.Claims(r); ok && old.AccountID != a.ID {
		s.terminals.logout(old)
	}
	s.issueSession(w, a)
}

// expiryOf is when a expires, or zero when it doesn't.
func expiryOf(a accounts.Account) time.Time {
	if a.Expires == nil {
		return time.Time{}
	}
	return *a.Expires
}

// reissueSession signs this browser in again to a, as after a change that
// bumped its session epoch. Credential requests have no cookie to renew.
func (s *Server) reissueSession(w http.ResponseWriter, r *http.Request, a accounts.Account) {
	if p, ok := s.principal(r); ok && p.claims != nil {
		s.issueSession(w, a)
	}
}

// withSession resolves the session cookie into the request context; it
// refreshes cookies that are more than a day old. The cookie must name the
// account by its ID and current session epoch, and the account must still
// be usable (accountNow).
func (s *Server) withSession(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	claims, ok := s.Sessions.Claims(r)
	if !ok {
		return r, false
	}
	a, found := s.accountNow(claims.Username, claims.AccountID, time.Now())
	if !found || a.SessionEpoch != claims.Epoch {
		return r, false
	}
	s.seen.touch(a.ID, time.Now())
	if claims.Refresh && (a.Expires == nil || claims.Expires.Before(a.Expires.Truncate(time.Second))) {
		claims.NotAfter = expiryOf(a)
		s.Sessions.Refresh(w, claims)
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: a, claims: &claims})), true
}

// bearerOf returns the request's Authorization bearer, if it has one (an
// empty one included: it authenticates nobody).
func bearerOf(r *http.Request) (string, bool) {
	scheme, value, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// withCredential resolves a client credential into the request context. Its
// account must be the one it was issued to, still usable (accountNow).
func (s *Server) withCredential(r *http.Request, bearer string) (*http.Request, bool) {
	c, ok := s.credentials.check(bearer)
	if !ok {
		return r, false
	}
	a, found := s.accountNow(c.Username, c.AccountID, time.Now())
	if !found {
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, principal{account: a, credential: &c})), true
}

// sameOrigin reports whether the request's Origin is this server's own.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// --- responses ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	return json.NewDecoder(r.Body).Decode(v)
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// --- the UI -------------------------------------------------------------------------

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	// .webmanifest is missing from some hosts' MIME databases; nosniff
	// requires an explicit manifest type for installation to work there.
	if r.URL.Path == "/manifest.webmanifest" {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.FileServerFS(s.UI).ServeHTTP(w, r)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Every page but the login, link and CLI login pages needs a session: those
	// open signed out, and a redirect would drop a link's token fragment.
	if !strings.HasPrefix(r.URL.Path, "/login") && r.URL.Path != "/link" && r.URL.Path != "/cli-login" {
		if _, ok := s.withSession(w, r); !ok {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
	}
	s.writeIndex(w)
}

func (s *Server) writeIndex(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s.UI != nil {
		if b, err := fs.ReadFile(s.UI, "index.html"); err == nil {
			_, _ = w.Write(b)
			return
		}
	}
	_, _ = w.Write([]byte("<!doctype html><title>Supervisor</title><p>The web UI isn't built: run <code>make -C supervisor web</code>."))
}

// accountsChanged closes the connections of accounts whose access or
// sessions changed that their level no longer allows, sweeps the shares,
// links and credentials that depended on them, and refreshes event streams.
func (s *Server) accountsChanged(usernames ...string) {
	now := time.Now()
	for _, u := range usernames {
		a, ok := s.accountState(u)
		level := levelOf(a, ok, now)
		s.terminals.revalidate(s, u)
		s.transfers.revalidate(s, u)
		s.viewers.revoke(u, level)
		s.screenWatchers.revoke(u, level)
		if ok {
			s.viewers.endSessions(u, a.ID, a.SessionEpoch)
			s.screenWatchers.endSessions(u, a.ID, a.SessionEpoch)
		}
	}
	s.sweep()
	s.events.refresh()
}

func since(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
