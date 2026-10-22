package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// Agent tokens: one computer each, an optional expiry, revocable; stored as
// a SHA-256 of the secret (the secret is random, so a slow hash adds nothing).
type token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ComputerID string     `json:"computerId"`
	Hash       string     `json:"hash"`
	CreatedBy  string     `json:"createdBy"`
	Created    time.Time  `json:"created"`
	Expires    *time.Time `json:"expires"`
	LastUsed   *time.Time `json:"lastUsed"`
}

func (t token) expired(now time.Time) bool { return t.Expires != nil && now.After(*t.Expires) }

type tokenStore struct {
	mu     sync.Mutex
	path   string
	list   []token
	open   map[string]map[int]context.CancelFunc // token ID → open agent connections
	nextID int
	dirty  bool
	closed map[string]bool // tokens whose connections were closed on expiry
}

func loadTokens(stateDir string) (*tokenStore, error) {
	ts := &tokenStore{path: filepath.Join(stateDir, "tokens.json"), open: map[string]map[int]context.CancelFunc{}, closed: map[string]bool{}}
	var f struct {
		Tokens []token `json:"tokens"`
	}
	if err := store.ReadJSON(ts.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ts.list = f.Tokens
	return ts, nil
}

func (ts *tokenStore) save() error {
	return store.WriteJSON(ts.path, map[string]any{"tokens": ts.list})
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func (ts *tokenStore) create(name, computerID, createdBy string, expires *time.Time) (token, string, error) {
	secretBytes := make([]byte, 32)
	_, _ = rand.Read(secretBytes)
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	t := token{ID: randomID(8), Name: name, ComputerID: computerID, Hash: hashSecret(secret),
		CreatedBy: createdBy, Created: time.Now().UTC(), Expires: expires}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.list = append(ts.list, t)
	return t, "dyt_" + t.ID + "_" + secret, ts.save()
}

// check resolves a bearer token; it returns the token when valid.
func (ts *tokenStore) check(bearer string) (token, bool) {
	rest, ok := strings.CutPrefix(bearer, "dyt_")
	if !ok {
		return token{}, false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok {
		return token{}, false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, t := range ts.list {
		if t.ID != id {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hashSecret(secret))) != 1 || t.expired(time.Now()) {
			return token{}, false
		}
		now := time.Now().UTC()
		if t.LastUsed == nil || now.Sub(*t.LastUsed) > time.Minute {
			ts.list[i].LastUsed = &now
			_ = ts.save()
		}
		return ts.list[i], true
	}
	return token{}, false
}

func (ts *tokenStore) forComputer(computerID string) []token {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []token
	for _, t := range ts.list {
		if t.ComputerID == computerID {
			out = append(out, t)
		}
	}
	return out
}

func (ts *tokenStore) activeCount(computerID string) int {
	n := 0
	for _, t := range ts.forComputer(computerID) {
		if !t.expired(time.Now()) {
			n++
		}
	}
	return n
}

// remove deletes tokens matching fn and closes their connections.
func (ts *tokenStore) remove(fn func(token) bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var keep []token
	for _, t := range ts.list {
		if fn(t) {
			for _, cancel := range ts.open[t.ID] {
				cancel()
			}
			delete(ts.open, t.ID)
			continue
		}
		keep = append(keep, t)
	}
	if len(keep) != len(ts.list) {
		ts.list = keep
		_ = ts.save()
	}
}

func (ts *tokenStore) forgetComputer(computerID string) {
	ts.remove(func(t token) bool { return t.ComputerID == computerID })
}

// creatorChanged revokes the tokens of an account that was deleted or lost
// access to the token's computer.
func (ts *tokenStore) creatorChanged(s *Server, username string, a accounts.Account, exists bool) {
	ts.remove(func(t token) bool {
		return t.CreatedBy == username && (!exists || !a.HasAccess(t.ComputerID))
	})
}

// track registers an open agent connection so revocation and expiry close it.
func (ts *tokenStore) track(id string, cancel context.CancelFunc) func() {
	ts.mu.Lock()
	ts.nextID++
	n := ts.nextID
	if ts.open[id] == nil {
		ts.open[id] = map[int]context.CancelFunc{}
	}
	ts.open[id][n] = cancel
	ts.mu.Unlock()
	return func() {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		delete(ts.open[id], n)
	}
}

// expireLoop closes the connections of tokens as they expire.
func (ts *tokenStore) expireLoop(ctx context.Context, s *Server) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		ts.mu.Lock()
		for _, t := range ts.list {
			if t.expired(now) && !ts.closed[t.ID] {
				for _, cancel := range ts.open[t.ID] {
					cancel()
				}
				delete(ts.open, t.ID)
				ts.closed[t.ID] = true
			}
		}
		ts.mu.Unlock()
	}
}

// --- API ---------------------------------------------------------------------------------

type tokenJSON struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedBy string     `json:"createdBy"`
	Created   time.Time  `json:"created"`
	Expires   *time.Time `json:"expires"`
	LastUsed  *time.Time `json:"lastUsed"`
	Expired   bool       `json:"expired"`
	Token     string     `json:"token,omitempty"`
	RemoteAdd string     `json:"remoteAdd,omitempty"`
}

func toTokenJSON(t token) tokenJSON {
	return tokenJSON{ID: t.ID, Name: t.Name, CreatedBy: t.CreatedBy, Created: t.Created, Expires: t.Expires,
		LastUsed: t.LastUsed, Expired: t.expired(time.Now())}
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.View)
	if !ok {
		return
	}
	out := []tokenJSON{}
	for _, t := range s.tokens.forComputer(c.ID) {
		out = append(out, toTokenJSON(t))
	}
	slices.SortFunc(out, func(a, b tokenJSON) int { return b.Created.Compare(a.Created) })
	writeJSON(w, http.StatusOK, out)
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.computerFor(w, r, auth.CreateToken)
	if !ok {
		return
	}
	var req struct {
		Name    string     `json:"name"`
		Expires *time.Time `json:"expires"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "a name is required")
		return
	}
	if req.Expires != nil && req.Expires.Before(time.Now()) {
		writeError(w, http.StatusBadRequest, "the expiry date is in the past")
		return
	}
	t, secret, err := s.tokens.create(strings.TrimSpace(req.Name), c.ID, a.Username, req.Expires)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("agent token created", "computer", c.Name, "token", t.Name, "by", a.Username)
	j := toTokenJSON(t)
	j.Token = secret
	j.RemoteAdd = "computer remote add " + c.Name + " " + baseURL(r) + "/" + c.Name + " --token " + secret
	s.publishComputer(r.Context(), c.Name)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.computerFor(w, r, auth.CreateToken)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var found *token
	for _, t := range s.tokens.forComputer(c.ID) {
		if t.ID == id {
			found = &t
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "no such token")
		return
	}
	if found.CreatedBy != a.Username && !auth.Can(a, auth.RevokeAnyToken, c.ID) {
		writeError(w, http.StatusForbidden, "only its creator or an admin can revoke this token")
		return
	}
	s.tokens.remove(func(t token) bool { return t.ID == id })
	s.Log.Info("agent token revoked", "computer", c.Name, "token", found.Name, "by", a.Username)
	s.publishComputer(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}
