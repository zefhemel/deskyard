package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
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

// Agent tokens: one environment each, an optional expiry, revocable; stored
// as a SHA-256 of the secret (the secret is random, so a slow hash adds
// nothing). A token names its environment and records when that environment
// was created, so a later environment of the same name doesn't honour it.
type token struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Environment        string     `json:"environment"`
	EnvironmentCreated time.Time  `json:"environmentCreated"`
	Hash               string     `json:"hash"`
	CreatedBy          string     `json:"createdBy"`
	CreatorID          string     `json:"creatorID,omitempty"`
	Created            time.Time  `json:"created"`
	Expires            *time.Time `json:"expires"`
	LastUsed           *time.Time `json:"lastUsed"`
}

func (t token) expired(now time.Time) bool { return t.Expires != nil && now.After(*t.Expires) }

type tokenStore struct {
	mu       sync.Mutex
	accounts *accounts.Store
	path     string
	list     []token
	open     map[string]map[int]context.CancelFunc // token ID → open agent connections
	nextID   int
	dirty    bool
	closed   map[string]bool // tokens whose connections were closed on expiry
	// created is when the named environment was created; false when it
	// isn't defined. Nil until bind.
	created func(name string) (time.Time, bool)

	// onGone is called, with no lock held, for every token that stops
	// working: revoked, its creator lost access, its environment removed,
	// or expired.
	onGone func(token)
}

func loadTokens(dataDir string) (*tokenStore, error) {
	ts := &tokenStore{path: filepath.Join(dataDir, "tokens.json"), open: map[string]map[int]context.CancelFunc{}, closed: map[string]bool{}}
	var f struct {
		Tokens []token `json:"tokens"`
	}
	if err := store.Restrict(ts.path); err != nil {
		return nil, err
	}
	if err := store.ReadJSON(ts.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ts.list = f.Tokens
	return ts, nil
}

func tokenCreatorAllowed(t token, a accounts.Account, exists bool) bool {
	return exists && a.HasAccess(t.Environment) && t.CreatorID != "" && t.CreatorID == a.ID
}

// environmentCurrent reports whether t's environment is the one it was made
// for: defined, and created when t records. Without bind it can't tell.
func (ts *tokenStore) environmentCurrent(t token) bool {
	if ts.created == nil {
		return true
	}
	created, ok := ts.created(t.Environment)
	return ok && created.Equal(t.EnvironmentCreated)
}

// bind attaches the accounts and environment definitions tokens are checked
// against, and purges, before the server accepts requests, records whose
// creator identity (account ID), access or environment no longer match.
func (ts *tokenStore) bind(accts *accounts.Store, created func(name string) (time.Time, bool)) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.created = created
	var next []token
	for _, t := range ts.list {
		a, exists := accts.Get(t.CreatedBy)
		if tokenCreatorAllowed(t, a, exists) && ts.environmentCurrent(t) {
			next = append(next, t)
		}
	}
	if len(next) != len(ts.list) {
		if err := store.WriteJSON(ts.path, map[string]any{"tokens": next}); err != nil {
			return err
		}
		ts.list = next
	}
	ts.accounts = accts
	return nil
}

func (s *Server) tokenCreatorAllowed(t token) bool {
	a, exists := s.Accounts.Get(t.CreatedBy)
	return tokenCreatorAllowed(t, a, exists)
}

func (ts *tokenStore) create(name, environment, createdBy string, expires *time.Time) (token, string, error) {
	secretBytes := make([]byte, 32)
	_, _ = rand.Read(secretBytes)
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	t := token{ID: randomID(8), Name: name, Environment: environment, Hash: hashSecret(secret),
		CreatedBy: createdBy, Created: time.Now().UTC(), Expires: expires}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.created != nil {
		created, ok := ts.created(environment)
		if !ok {
			return token{}, "", errors.New("no such environment")
		}
		t.EnvironmentCreated = created
	}
	if ts.accounts != nil {
		a, exists := ts.accounts.Get(createdBy)
		if !exists || !a.HasAccess(environment) {
			return token{}, "", errors.New("token creator no longer has access to the environment")
		}
		t.CreatorID = a.ID
	}
	next := append(slices.Clone(ts.list), t)
	if err := store.WriteJSON(ts.path, map[string]any{"tokens": next}); err != nil {
		return token{}, "", err
	}
	ts.list = next
	return t, "dyt_" + t.ID + "_" + secret, nil
}

// check resolves a bearer token; it returns the token when valid, which
// includes its environment being the one it was made for.
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
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(hashSecret(secret))) != 1 || t.expired(time.Now()) || !ts.environmentCurrent(t) {
			return token{}, false
		}
		now := time.Now().UTC()
		if t.LastUsed == nil || now.Sub(*t.LastUsed) > time.Minute {
			next := slices.Clone(ts.list)
			next[i].LastUsed = &now
			if err := store.WriteJSON(ts.path, map[string]any{"tokens": next}); err == nil {
				ts.list = next
			}
		}
		return ts.list[i], true
	}
	return token{}, false
}

// forEnvironment lists the tokens of the named environment; tokens left over
// from an earlier environment of that name are not its.
func (ts *tokenStore) forEnvironment(name string) []token {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []token
	for _, t := range ts.list {
		if t.Environment == name && ts.environmentCurrent(t) {
			out = append(out, t)
		}
	}
	return out
}

func (ts *tokenStore) activeCount(name string) int {
	n := 0
	for _, t := range ts.forEnvironment(name) {
		if !t.expired(time.Now()) {
			n++
		}
	}
	return n
}

// remove deletes tokens matching fn and closes their connections.
func (ts *tokenStore) remove(fn func(token) bool) error {
	var gone, keep []token
	ts.mu.Lock()
	for _, t := range ts.list {
		if fn(t) {
			gone = append(gone, t)
		} else {
			keep = append(keep, t)
		}
	}
	if len(gone) == 0 {
		ts.mu.Unlock()
		return nil
	}
	if err := store.WriteJSON(ts.path, map[string]any{"tokens": keep}); err != nil {
		ts.mu.Unlock()
		return err
	}
	ts.list = keep
	for _, t := range gone {
		for _, cancel := range ts.open[t.ID] {
			cancel()
		}
		delete(ts.open, t.ID)
	}
	ts.mu.Unlock()
	ts.notifyGone(gone)
	return nil
}

func (ts *tokenStore) notifyGone(gone []token) {
	if ts.onGone == nil {
		return
	}
	for _, t := range gone {
		ts.onGone(t)
	}
}

// forget removes every token of the named environment, including any left
// over from an earlier environment of that name.
func (ts *tokenStore) forget(name string) error {
	return ts.remove(func(t token) bool { return t.Environment == name })
}

// creatorChanged revokes the tokens of an account that was deleted or lost
// access to the token's environment.
func (ts *tokenStore) creatorChanged(s *Server, username string) error {
	// Serialize the current account lookup with token creation and cleanup. A
	// delayed callback must not apply an older grant to tokens issued afterward.
	var gone, keep []token
	ts.mu.Lock()
	a, exists := s.Accounts.Get(username)
	for _, t := range ts.list {
		if t.CreatedBy != username || tokenCreatorAllowed(t, a, exists) {
			keep = append(keep, t)
			continue
		}
		gone = append(gone, t)
		// Permission loss closes live access even when file cleanup fails.
		for _, cancel := range ts.open[t.ID] {
			cancel()
		}
		delete(ts.open, t.ID)
	}
	var err error
	if len(gone) > 0 {
		err = store.WriteJSON(ts.path, map[string]any{"tokens": keep})
		if err == nil {
			ts.list = keep
		}
	}
	ts.mu.Unlock()
	ts.notifyGone(gone)
	return err
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
		ts.expireNow(time.Now())
	}
}

// expireNow closes the connections of tokens that have expired by now, once
// per token.
func (ts *tokenStore) expireNow(now time.Time) {
	var gone []token
	ts.mu.Lock()
	for _, t := range ts.list {
		if t.expired(now) && !ts.closed[t.ID] {
			for _, cancel := range ts.open[t.ID] {
				cancel()
			}
			delete(ts.open, t.ID)
			ts.closed[t.ID] = true
			gone = append(gone, t)
		}
	}
	ts.mu.Unlock()
	ts.notifyGone(gone)
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
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	out := []tokenJSON{}
	for _, t := range s.tokens.forEnvironment(c.Name) {
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
	c, a, ok := s.environmentFor(w, r, auth.Share)
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
	t, secret, err := s.tokens.create(strings.TrimSpace(req.Name), c.Name, a.Username, req.Expires)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("agent token created", "environment", c.Name, "token", t.Name, "by", a.Username)
	j := toTokenJSON(t)
	j.Token = secret
	j.RemoteAdd = "deskyard remote add " + c.Name + " " + baseURL(r) + "/" + c.Name + " --token " + secret
	s.publishEnvironment(r.Context(), c.Name)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Share)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var found *token
	for _, t := range s.tokens.forEnvironment(c.Name) {
		if t.ID == id {
			found = &t
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "no such token")
		return
	}
	if found.CreatedBy != a.Username && !a.IsAdmin() {
		writeError(w, http.StatusForbidden, "only its creator or an admin can revoke this token")
		return
	}
	if err := s.tokens.remove(func(t token) bool { return t.ID == id }); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("agent token revoked", "environment", c.Name, "token", found.Name, "by", a.Username)
	s.publishEnvironment(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}
