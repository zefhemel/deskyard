// Package server is the supervisor's HTTP side: the UI and its API, the
// desktop proxy, and the agent API. API.md in the module root is the contract.
package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

type Server struct {
	Log      *slog.Logger
	Eng      engine.Engine
	Desktops *desktops.Service
	Accounts *accounts.Store
	Sessions *auth.Sessions
	Throttle *auth.Throttle
	StateDir string
	UI       fs.FS // the built web UI (index.html, assets/…)

	jobs    *jobs
	events  *hub
	viewers *viewers
	clients *clientFiles
	stats   *statsCollector
	thumbs  *thumbnails
	tokens  *tokenStore
	images  *catalog

	api *http.ServeMux
}

type Options struct {
	Log          *slog.Logger
	Eng          engine.Engine
	Desktops     *desktops.Service
	Accounts     *accounts.Store
	StateDir     string
	UI           fs.FS
	Secure       bool // served over HTTPS
	DefaultImage string
}

func New(o Options) (*Server, error) {
	key, err := sessionKey(o.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		Log: o.Log, Eng: o.Eng, Desktops: o.Desktops, Accounts: o.Accounts,
		Sessions: &auth.Sessions{Key: key, Secure: o.Secure}, Throttle: &auth.Throttle{},
		StateDir: o.StateDir, UI: o.UI,
		jobs: newJobs(), events: newHub(), viewers: newViewers(),
		clients: &clientFiles{eng: o.Eng, dir: filepath.Join(o.StateDir, "clients")},
	}
	s.stats = newStatsCollector(s)
	s.thumbs = newThumbnails(s)
	if s.tokens, err = loadTokens(o.StateDir); err != nil {
		return nil, err
	}
	if s.images, err = loadCatalog(o.StateDir, o.DefaultImage); err != nil {
		return nil, err
	}
	o.Accounts.OnChange = s.accountsChanged
	s.routes()
	return s, nil
}

// Run starts the background work: engine events, stats, update checks.
func (s *Server) Run(ctx context.Context) {
	go s.watchEngine(ctx)
	go s.stats.run(ctx)
	go s.tokens.expireLoop(ctx, s)
	go s.images.checkLoop(ctx, s)
}

func sessionKey(stateDir string) ([]byte, error) {
	p := filepath.Join(stateDir, "session.key")
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
		if strings.HasPrefix(path, "/api/agent/") {
			s.serveAgent(w, r)
			return
		}
		s.api.ServeHTTP(w, r)
	case first == "assets" || strings.Contains(first, "."):
		s.serveStatic(w, r)
	case first == "" || desktops.IsReserved(first):
		s.serveIndex(w, r)
	default:
		s.serveDesktop(w, r, first, rest)
	}
}

// --- sessions -----------------------------------------------------------------

type ctxKey struct{}

// account returns the signed-in account of the request, if any.
func (s *Server) account(r *http.Request) (accounts.Account, bool) {
	a, ok := r.Context().Value(ctxKey{}).(accounts.Account)
	return a, ok
}

// withSession resolves the session cookie into the request context; it
// refreshes cookies that are more than a day old.
func (s *Server) withSession(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	username, epoch, refresh, ok := s.Sessions.Read(r)
	if !ok {
		return r, false
	}
	a, found := s.Accounts.Get(username)
	if !found || a.SessionEpoch != epoch {
		return r, false
	}
	if refresh {
		s.Sessions.Issue(w, a.Username, a.SessionEpoch)
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)), true
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
	http.FileServerFS(s.UI).ServeHTTP(w, r)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Every page but the login page needs a session.
	if !strings.HasPrefix(r.URL.Path, "/login") {
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

// accountsChanged closes desktop connections and event streams of accounts
// whose access or sessions changed.
func (s *Server) accountsChanged(usernames ...string) {
	for _, u := range usernames {
		a, ok := s.Accounts.Get(u)
		s.viewers.revoke(u, func(desktopID string) bool { return ok && a.HasAccess(desktopID) })
		s.tokens.creatorChanged(s, u, a, ok)
	}
	s.events.refresh()
}

func since(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
