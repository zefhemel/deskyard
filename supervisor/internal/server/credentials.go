package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

const credentialPrefix = "dyc_"

// A client credential is what one signed-in client (the CLI, later WebDAV
// clients) holds: a bearer secret bound to the account's ID, so a recreated
// account of the same name never honours it. It grants whatever the account
// may do at the time of each request.
type credential struct {
	ID        string     `json:"id"`
	AccountID string     `json:"accountID"`
	Username  string     `json:"username"`
	Kind      string     `json:"kind"` // "cli"
	Name      string     `json:"name"` // describes the client, such as "deskyard CLI on zef-mbp"
	Hash      string     `json:"hash"`
	Created   time.Time  `json:"created"`
	LastUsed  *time.Time `json:"lastUsed"`
}

// credentialConn is a live connection made with a credential.
type credentialConn struct {
	environment string
	cancel      context.CancelFunc
}

type credentialStore struct {
	mu     sync.Mutex
	path   string
	all    []credential
	open   map[string]map[int]credentialConn // credential ID → its live connections
	nextID int

	// onGone is called, with no lock held, for every credential deleted.
	onGone func(credential)
}

func loadCredentials(dataDir string) (*credentialStore, error) {
	cs := &credentialStore{path: filepath.Join(dataDir, "credentials.json"), open: map[string]map[int]credentialConn{}}
	var f struct {
		Credentials []credential `json:"credentials"`
	}
	if err := store.Restrict(cs.path); err != nil {
		return nil, err
	}
	if err := store.ReadJSON(cs.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	cs.all = f.Credentials
	return cs, nil
}

func (cs *credentialStore) write(next []credential) error {
	return store.WriteJSON(cs.path, map[string]any{"credentials": next})
}

// create issues a credential for a and returns it with its bearer secret,
// which is never available again.
func (cs *credentialStore) create(a accounts.Account, kind, name string) (credential, string, error) {
	id, bearer, hash := newSecret(credentialPrefix)
	c := credential{ID: id, AccountID: a.ID, Username: a.Username, Kind: kind, Name: name, Hash: hash, Created: time.Now().UTC()}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	next := append(slices.Clone(cs.all), c)
	if err := cs.write(next); err != nil {
		return credential{}, "", err
	}
	cs.all = next
	return c, bearer, nil
}

// check resolves a bearer secret into its credential. Whether its account
// still exists with the same ID is the caller's to check. Last use is
// recorded at most once a minute, best effort.
func (cs *credentialStore) check(bearer string) (credential, bool) {
	id, secret, ok := parseBearer(credentialPrefix, bearer)
	if !ok {
		return credential{}, false
	}
	hash := hashSecret(secret)
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for i, c := range cs.all {
		if c.ID != id {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(hash)) != 1 {
			return credential{}, false
		}
		now := time.Now().UTC()
		if c.LastUsed == nil || now.Sub(*c.LastUsed) > time.Minute {
			next := slices.Clone(cs.all)
			next[i].LastUsed = &now
			if err := cs.write(next); err == nil {
				cs.all = next
			}
		}
		return cs.all[i], true
	}
	return credential{}, false
}

// list returns the credentials of the account with this ID ("": all),
// newest first.
func (cs *credentialStore) list(accountID string) []credential {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	var out []credential
	for _, c := range cs.all {
		if accountID == "" || c.AccountID == accountID {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b credential) int { return b.Created.Compare(a.Created) })
	return out
}

// remove deletes the credentials matching fn and closes their connections.
// Revocation closes live access even when the write fails; the error is
// returned and the credentials stay on file.
func (cs *credentialStore) remove(fn func(credential) bool) error {
	var gone, keep []credential
	cs.mu.Lock()
	for _, c := range cs.all {
		if fn(c) {
			gone = append(gone, c)
		} else {
			keep = append(keep, c)
		}
	}
	if len(gone) == 0 {
		cs.mu.Unlock()
		return nil
	}
	err := cs.write(keep)
	if err == nil {
		cs.all = keep
	}
	for _, c := range gone {
		for _, conn := range cs.open[c.ID] {
			conn.cancel()
		}
		delete(cs.open, c.ID)
	}
	cs.mu.Unlock()
	if err == nil && cs.onGone != nil {
		for _, c := range gone {
			cs.onGone(c)
		}
	}
	return err
}

// retain keeps only the credentials valid accepts, as remove does.
func (cs *credentialStore) retain(valid func(c credential) bool) error {
	return cs.remove(func(c credential) bool { return !valid(c) })
}

// track registers a live connection of credential id to the environment so
// revocation closes it; the returned function unregisters it. A connection
// to no environment ("") closes only when the credential or its account
// goes: it checks its access itself.
func (cs *credentialStore) track(id, environment string, cancel context.CancelFunc) func() {
	cs.mu.Lock()
	cs.nextID++
	n := cs.nextID
	if cs.open[id] == nil {
		cs.open[id] = map[int]credentialConn{}
	}
	cs.open[id][n] = credentialConn{environment: environment, cancel: cancel}
	cs.mu.Unlock()
	return func() {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		delete(cs.open[id], n)
		if len(cs.open[id]) == 0 {
			delete(cs.open, id)
		}
	}
}

// has reports whether credential id is still on file.
func (cs *credentialStore) has(id string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return slices.ContainsFunc(cs.all, func(c credential) bool { return c.ID == id })
}

// closeWhere closes the connections of credential id to the environments fn
// accepts, such as those its account lost access to.
func (cs *credentialStore) closeWhere(id string, fn func(environment string) bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for n, conn := range cs.open[id] {
		if fn(conn.environment) {
			conn.cancel()
			delete(cs.open[id], n)
		}
	}
}

// connected reports whether any credential of the account with this ID has
// a live connection open.
func (cs *credentialStore) connected(accountID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, c := range cs.all {
		if c.AccountID == accountID && len(cs.open[c.ID]) > 0 {
			return true
		}
	}
	return false
}
