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
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

const linkPrefix = "dyl_"

var errLinkUsed = errors.New("this link was already used or replaced")

// A login link is a single-use way into a token account: its code
// (dyl_<id>_<secret>) is exchanged once for a browser session or a client
// credential. It is bound to the account's ID, so a recreated account of the
// same name never honours it. Unused links never expire.
type link struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	AccountID string    `json:"accountID"`
	CreatedBy string    `json:"createdBy"` // the username that issued it
	Hash      string    `json:"hash"`
	Created   time.Time `json:"created"`
}

type linkStore struct {
	mu   sync.Mutex
	path string
	all  []link
}

func loadLinks(dataDir string) (*linkStore, error) {
	ls := &linkStore{path: filepath.Join(dataDir, "links.json")}
	var f struct {
		Links []link `json:"links"`
	}
	if err := store.Restrict(ls.path); err != nil {
		return nil, err
	}
	if err := store.ReadJSON(ls.path, &f); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	ls.all = f.Links
	return ls, nil
}

func (ls *linkStore) write(next []link) error {
	return store.WriteJSON(ls.path, map[string]any{"links": next})
}

// issue makes a new link for a, replacing its unused one, and returns the
// code, which is never available again.
func (ls *linkStore) issue(a accounts.Account, by string) (string, error) {
	id, code, hash := newSecret(linkPrefix)
	l := link{ID: id, Username: a.Username, AccountID: a.ID, CreatedBy: by, Hash: hash, Created: time.Now().UTC()}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	next := slices.DeleteFunc(slices.Clone(ls.all), func(o link) bool { return o.AccountID == a.ID })
	next = append(next, l)
	if err := ls.write(next); err != nil {
		return "", err
	}
	ls.all = next
	return code, nil
}

// find returns the index of the link the code names, or -1. ls.mu is held.
func (ls *linkStore) find(code string) int {
	id, secret, ok := parseBearer(linkPrefix, code)
	if !ok {
		return -1
	}
	hash := hashSecret(secret)
	for i, l := range ls.all {
		if l.ID == id {
			if subtle.ConstantTimeCompare([]byte(l.Hash), []byte(hash)) != 1 {
				return -1
			}
			return i
		}
	}
	return -1
}

// peek resolves a code into its link without using it. Whether its account
// still exists with the same ID is the caller's to check.
func (ls *linkStore) peek(code string) (link, bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	i := ls.find(code)
	if i < 0 {
		return link{}, false
	}
	return ls.all[i], true
}

// redeem uses the link the code names: under the lock it calls accept (which
// checks the account and may refuse, keeping the link), then deletes the link
// and persists that. Only after redeem returns nil may the caller issue a
// session or credential. Of concurrent redemptions exactly one gets the link;
// the others get errLinkUsed. A failed write returns the error and keeps it.
func (ls *linkStore) redeem(code string, accept func(link) error) (link, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	i := ls.find(code)
	if i < 0 {
		return link{}, errLinkUsed
	}
	l := ls.all[i]
	if err := accept(l); err != nil {
		return link{}, err
	}
	next := slices.Delete(slices.Clone(ls.all), i, i+1)
	if err := ls.write(next); err != nil {
		return link{}, err
	}
	ls.all = next
	return l, nil
}

// has reports whether the link with this ID is unused.
func (ls *linkStore) has(id string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return slices.ContainsFunc(ls.all, func(l link) bool { return l.ID == id })
}

// pending reports whether the account with this ID has an unused link.
func (ls *linkStore) pending(accountID string) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return slices.ContainsFunc(ls.all, func(l link) bool { return l.AccountID == accountID })
}

// retain keeps only the links valid accepts, such as those whose account
// still exists with the same ID. A failed write keeps them all.
func (ls *linkStore) retain(valid func(l link) bool) error {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	next := slices.DeleteFunc(slices.Clone(ls.all), func(l link) bool { return !valid(l) })
	if len(next) == len(ls.all) {
		return nil
	}
	if err := ls.write(next); err != nil {
		return err
	}
	ls.all = next
	return nil
}
