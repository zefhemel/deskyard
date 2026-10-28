package accounts

import (
	"crypto/subtle"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// Login links and client credentials are bearer secrets of an account
// (auth.NewSecret), kept with it as hashes: deleting the account deletes
// them, and a recreated account of the same name never honours them.

const (
	LinkPrefix       = "dyl_"
	CredentialPrefix = "dyc_"
)

// ErrLinkUsed refuses a login link that was used, replaced or never was,
// or whose account may no longer use anything.
var ErrLinkUsed = errors.New("this link was already used or replaced")

// A Link is a single-use way into a token account: its code
// (dyl_<id>_<secret>) is exchanged once for a browser session or a client
// credential. Unused links never expire.
type Link struct {
	ID        string    `json:"id"`
	CreatedBy string    `json:"createdBy"` // the username that issued it
	Hash      string    `json:"hash"`
	Created   time.Time `json:"created"`
}

// A Credential is what one signed-in client (the CLI, a WebDAV client)
// holds. It grants whatever its account may do at the time of each request.
type Credential struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"` // "cli" or "webdav"
	Name     string     `json:"name"` // describes the client, such as "deskyard CLI on zef-mbp"
	Hash     string     `json:"hash"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"lastUsed"`
	// Its account's, as the store hands it out.
	AccountID string `json:"-"`
	Username  string `json:"-"`
}

func newLink(by string) (*Link, string) {
	id, code, hash := auth.NewSecret(LinkPrefix)
	return &Link{ID: id, CreatedBy: by, Hash: hash, Created: time.Now().UTC()}, code
}

// matches reports whether the bearer secret's secret is the one hash is of.
func matches(hash, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(auth.HashSecret(secret))) == 1
}

// IssueLink gives the account with this username and ID a new login link,
// issued by the named account, replacing its unused one, and returns the
// code, which is never available again.
func (s *Store) IssueLink(username, id, by string) (string, error) {
	var code string
	err := s.change(func() ([]string, error) {
		i := s.index(username)
		if i < 0 || s.list[i].ID != id {
			return nil, ErrNotFound
		}
		next := s.next()
		next[i].Link, code = newLink(by)
		return s.commit(next, nil)
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// link is the index of the account whose link the code names, or -1. The
// lock is held.
func (s *Store) link(code string) int {
	id, secret, ok := auth.ParseBearer(LinkPrefix, code)
	if !ok {
		return -1
	}
	for i, a := range s.list {
		if a.Link != nil && a.Link.ID == id {
			if !matches(a.Link.Hash, secret) {
				return -1
			}
			return i
		}
	}
	return -1
}

// PeekLink is the account the link with this code signs in as, while it
// may use anything (Usable), without using the link.
func (s *Store) PeekLink(code string, now time.Time) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.link(code)
	if i < 0 || s.usable(s.list[i].Username, s.list[i].ID, now) < 0 {
		return Account{}, false
	}
	return clone(s.list[i]), true
}

// NewCredential describes a credential to make.
type NewCredential struct{ Kind, Name string }

// RedeemLink uses the link with this code: under the lock it calls accept
// with its account, while that may use anything (Usable), which may refuse;
// then it deletes the link and, given cred, makes that credential for the
// account, in one write. Of concurrent redemptions exactly one gets the
// link; the others get ErrLinkUsed. When the write fails the link stays.
// accept must not call the store.
func (s *Store) RedeemLink(code string, now time.Time, cred *NewCredential, accept func(Account) error) (a Account, c Credential, bearer string, err error) {
	err = s.change(func() ([]string, error) {
		i := s.link(code)
		if i < 0 || s.usable(s.list[i].Username, s.list[i].ID, now) < 0 {
			return nil, ErrLinkUsed
		}
		a = clone(s.list[i])
		if err := accept(a); err != nil {
			return nil, err
		}
		next := s.next()
		next[i].Link = nil
		if cred != nil {
			c, bearer = newCredential(next[i], *cred)
			next[i].Credentials = append(next[i].Credentials, c)
		}
		_, err := s.commit(next, nil)
		return nil, err
	})
	if err != nil {
		return Account{}, Credential{}, "", err
	}
	a.Link = nil
	return a, c, bearer, nil
}

func newCredential(a Account, n NewCredential) (Credential, string) {
	id, bearer, hash := auth.NewSecret(CredentialPrefix)
	return Credential{ID: id, Kind: n.Kind, Name: n.Name, Hash: hash, Created: time.Now().UTC(), AccountID: a.ID, Username: a.Username}, bearer
}

// AddCredential makes a credential for the account with this username and
// ID and returns it with its bearer secret, which is never available again.
// With replace, it replaces the account's credentials of the same kind.
func (s *Store) AddCredential(username, id string, n NewCredential, replace bool) (c Credential, bearer string, err error) {
	err = s.change(func() ([]string, error) {
		i := s.index(username)
		if i < 0 || s.list[i].ID != id {
			return nil, ErrNotFound
		}
		next := s.next()
		if replace {
			next[i].Credentials = slices.DeleteFunc(next[i].Credentials, func(o Credential) bool { return o.Kind == n.Kind })
		}
		c, bearer = newCredential(next[i], n)
		next[i].Credentials = append(next[i].Credentials, c)
		return s.commit(next, nil)
	})
	if err != nil {
		return Credential{}, "", err
	}
	return c, bearer, nil
}

// findCredential is the indexes of the account and credential with this
// ID. The lock is held.
func (s *Store) findCredential(id string) (int, int, bool) {
	for i, a := range s.list {
		if j := a.credential(id); j >= 0 {
			return i, j, true
		}
	}
	return -1, -1, false
}

// credential is the index of the account's credential with this ID, or -1.
func (a Account) credential(id string) int {
	return slices.IndexFunc(a.Credentials, func(c Credential) bool { return c.ID == id })
}

// owned is the account's credential at j, as the store hands it out.
func (a Account) owned(j int) Credential {
	c := a.Credentials[j]
	c.AccountID, c.Username = a.ID, a.Username
	if c.LastUsed != nil {
		used := *c.LastUsed
		c.LastUsed = &used
	}
	return c
}

// CheckCredential resolves a bearer secret into its credential. Whether its
// account may use anything is the caller's to check (Usable). Last use is
// recorded at most once a minute, best effort.
func (s *Store) CheckCredential(bearer string) (c Credential, ok bool) {
	id, secret, parsed := auth.ParseBearer(CredentialPrefix, bearer)
	if !parsed {
		return Credential{}, false
	}
	_ = s.change(func() ([]string, error) {
		i, j, found := s.findCredential(id)
		if !found || !matches(s.list[i].Credentials[j].Hash, secret) {
			return nil, nil
		}
		ok = true
		now := time.Now().UTC()
		if used := s.list[i].Credentials[j].LastUsed; used == nil || now.Sub(*used) > time.Minute {
			next := s.next()
			next[i].Credentials[j].LastUsed = &now
			_, _ = s.commit(next, nil)
		}
		c = s.list[i].owned(j)
		return nil, nil
	})
	if !ok {
		return Credential{}, false
	}
	return c, true
}

// HasCredential reports whether the credential with this ID exists.
func (s *Store) HasCredential(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, ok := s.findCredential(id)
	return ok
}

// Credentials lists the credentials of the account with this ID ("": all),
// newest first.
func (s *Store) Credentials(accountID string) []Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Credential
	for _, a := range s.list {
		if accountID == "" || a.ID == accountID {
			for j := range a.Credentials {
				out = append(out, a.owned(j))
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Credential) int { return b.Created.Compare(a.Created) })
	return out
}

// RemoveCredentials deletes the credentials fn accepts and returns them.
// Unsaved, they are gone all the same.
func (s *Store) RemoveCredentials(fn func(Credential) bool) (gone []Credential, err error) {
	err = s.change(func() ([]string, error) {
		next := s.next()
		for i := range next {
			next[i].Credentials = slices.DeleteFunc(next[i].Credentials, func(c Credential) bool {
				c.AccountID, c.Username = next[i].ID, next[i].Username
				if fn(c) {
					gone = append(gone, c)
					return true
				}
				return false
			})
		}
		if len(gone) == 0 {
			return nil, nil
		}
		_, err := s.commit(next, revoked)
		return nil, err
	})
	return gone, err
}

// moveTokenFiles moves the login links and client credentials of earlier
// versions, kept in links.json and credentials.json, into the accounts they
// belong to, and returns the files to remove once the accounts are saved.
// Those of accounts gone, and a credential of a link redemption that
// stopped before deleting its link (its bearer never handed out), are
// dropped.
func (s *Store) moveTokenFiles() ([]string, error) {
	var links struct {
		Links []struct {
			Link
			Username  string `json:"username"`
			AccountID string `json:"accountID"`
		} `json:"links"`
	}
	var creds struct {
		Credentials []struct {
			Credential
			Username  string `json:"username"`
			AccountID string `json:"accountID"`
			Link      string `json:"link"`
		} `json:"credentials"`
	}
	var moved []string
	for _, f := range []struct {
		name string
		v    any
	}{{"links.json", &links}, {"credentials.json", &creds}} {
		p := filepath.Join(s.dataDir, f.name)
		if err := store.ReadJSON(p, f.v); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		moved = append(moved, p)
	}
	owner := func(username, id string) int {
		i := s.index(username)
		if i < 0 || s.list[i].ID != id {
			return -1
		}
		return i
	}
	unused := map[string]bool{}
	for _, l := range links.Links {
		if i := owner(l.Username, l.AccountID); i >= 0 {
			link := l.Link
			s.list[i].Link = &link
			unused[l.ID] = true
		}
	}
	for _, c := range creds.Credentials {
		if i := owner(c.Username, c.AccountID); i >= 0 && !(c.Link != "" && unused[c.Link]) && s.list[i].credential(c.ID) < 0 {
			s.list[i].Credentials = append(s.list[i].Credentials, c.Credential)
		}
	}
	return moved, nil
}

func removeFiles(paths []string) error {
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
