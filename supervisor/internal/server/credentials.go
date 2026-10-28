package server

import (
	"crypto/subtle"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
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
	// Link is the login link whose redemption made it, if one did.
	Link string `json:"link,omitempty"`
}

type credentialStore struct {
	mu   sync.Mutex
	path string
	all  []credential

	// onGone is called, with no lock held, for every credential deleted.
	onGone func(credential)
	// closed is called, with no lock held, with every credential removed,
	// also when that couldn't be saved: its live connections close.
	closed func(id string)
}

func loadCredentials(dataDir string) (*credentialStore, error) {
	cs := &credentialStore{path: filepath.Join(dataDir, "credentials.json")}
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
	return cs.add(credential{AccountID: a.ID, Username: a.Username, Kind: kind, Name: name})
}

// createRedeeming is create for a CLI redeeming the login link with this ID.
func (cs *credentialStore) createRedeeming(a accounts.Account, name, link string) (credential, string, error) {
	return cs.add(credential{AccountID: a.ID, Username: a.Username, Kind: "cli", Name: name, Link: link})
}

func (cs *credentialStore) add(c credential) (credential, string, error) {
	id, bearer, hash := auth.NewSecret(credentialPrefix)
	c.ID, c.Hash, c.Created = id, hash, time.Now().UTC()
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
	id, secret, ok := auth.ParseBearer(credentialPrefix, bearer)
	if !ok {
		return credential{}, false
	}
	hash := auth.HashSecret(secret)
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
	cs.mu.Unlock()
	if cs.closed != nil {
		for _, c := range gone {
			cs.closed(c.ID)
		}
	}
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

// has reports whether credential id is still on file.
func (cs *credentialStore) has(id string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return slices.ContainsFunc(cs.all, func(c credential) bool { return c.ID == id })
}
