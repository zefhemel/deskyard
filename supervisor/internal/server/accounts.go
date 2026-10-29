package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

type accountJSON struct {
	Username    string           `json:"username"`
	Kind        string           `json:"kind"` // "password" or "token"
	Admin       bool             `json:"admin"`
	Description string           `json:"description"`
	Grants      []accounts.Grant `json:"grants"` // of defined computers, sorted
	SharedBy    *accounts.Sharer `json:"sharedBy"`
	Expires     *time.Time       `json:"expires"`
	LinkPending bool             `json:"linkPending"`
	Created     time.Time        `json:"created"`
	LastLogin   *time.Time       `json:"lastLogin"`
}

func accountKind(a accounts.Account) string {
	if a.HasPassword() {
		return "password"
	}
	return "token"
}

// toAccountJSON shows a with its grants of defined computers.
func (s *Server) toAccountJSON(a accounts.Account) accountJSON {
	if held, ok := s.unsaved.apply(a); ok {
		a = held
	}
	return accountJSON{Username: a.Username, Kind: accountKind(a), Admin: a.Admin, Description: a.Description,
		Grants: s.definedGrants(a), SharedBy: a.SharedBy, Expires: a.Expires, LinkPending: s.links.pending(a.ID),
		Created: a.Created, LastLogin: since(a.LastLogin)}
}

// definedGrants is a's grants of defined computers, sorted by computer.
func (s *Server) definedGrants(a accounts.Account) []accounts.Grant {
	out := []accounts.Grant{}
	for _, g := range a.Grants {
		if _, ok := s.Computers.Defs.Get(g.Computer); ok {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(x, y accounts.Grant) int { return strings.Compare(x.Computer, y.Computer) })
	return out
}

// checkGrants writes 400 for a grant of an undefined computer.
func (s *Server) checkGrants(w http.ResponseWriter, grants []accounts.Grant) bool {
	for _, g := range grants {
		if _, ok := s.Computers.Defs.Get(g.Computer); !ok {
			writeError(w, http.StatusBadRequest, "no computer "+g.Computer)
			return false
		}
	}
	return true
}

// checkExpiry writes 400 for an expiry that has passed.
func checkExpiry(w http.ResponseWriter, expires *time.Time) bool {
	if expires != nil && !expires.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "the expiry must be in the future")
		return false
	}
	return true
}

const maxDescription = 200

func checkDescription(w http.ResponseWriter, d string) bool {
	if utf8.RuneCountInString(d) > maxDescription {
		writeError(w, http.StatusBadRequest, "a description is at most 200 characters")
		return false
	}
	return true
}

// optionalTime is a JSON field that may be omitted (Set false), null (Set,
// Time nil) or a time.
type optionalTime struct {
	Set  bool
	Time *time.Time
}

func (o *optionalTime) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.Time)
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !me.Admin {
		// Invited accounts may know each other's usernames (avatars, who's
		// viewing) and their own shares'; a share, an outsider, only its own.
		out := []map[string]string{}
		for _, a := range s.Accounts.List() {
			if a.Username == me.Username || !me.IsShare() && (!a.IsShare() || a.SharedBy.ID == me.ID) {
				out = append(out, map[string]string{"username": a.Username})
			}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	// As access stands, so what the Edit dialog saves back keeps what
	// couldn't be saved.
	out := []accountJSON{}
	for _, a := range s.accountStates() {
		out = append(out, s.toAccountJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Username, Password, Description string
		Admin                           bool
		Grants                          []accounts.Grant
		Expires                         *time.Time
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !s.checkGrants(w, req.Grants) || !checkExpiry(w, req.Expires) || !checkDescription(w, req.Description) {
		return
	}
	// Without a password it is a token account, which signs in through a
	// login link.
	a, err := s.Accounts.Create(accounts.NewAccount{Username: req.Username, Password: req.Password, Description: req.Description,
		Admin: req.Admin, Grants: req.Grants, Expires: req.Expires})
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("account created", "account", a.Username, "kind", accountKind(a), "admin", a.Admin, "by", me.Username)
	s.events.refresh()
	writeJSON(w, http.StatusCreated, s.toAccountJSON(a))
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Password    *string           `json:"password"`
		Admin       *bool             `json:"admin"`
		Description *string           `json:"description"`
		Grants      *[]accounts.Grant `json:"grants"`
		Expires     optionalTime      `json:"expires"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	current, ok := s.Accounts.Get(r.PathValue("username"))
	if !ok {
		writeError(w, http.StatusNotFound, accounts.ErrNotFound.Error())
		return
	}
	switch {
	case req.Password != nil && *req.Password == "":
		writeError(w, http.StatusBadRequest, "the password can't be empty")
		return
	case req.Password != nil && !current.HasPassword():
		writeError(w, http.StatusBadRequest, "token accounts have no password")
		return
	case current.IsShare() && (req.Admin != nil || req.Grants != nil):
		writeError(w, http.StatusBadRequest, "a share's access is set when it is shared")
		return
	}
	if req.Grants != nil && !s.checkGrants(w, *req.Grants) {
		return
	}
	if req.Description != nil && !checkDescription(w, *req.Description) {
		return
	}
	if req.Expires.Set && !checkExpiry(w, req.Expires.Time) {
		return
	}
	s.revoking.Lock()
	defer s.revoking.Unlock()
	a, err := s.Accounts.Update(current.Username, func(a *accounts.Account) {
		if req.Password != nil {
			a.PasswordHash = auth.HashPassword(*req.Password)
		}
		if req.Admin != nil {
			a.Admin = *req.Admin
		}
		if req.Description != nil {
			a.Description = *req.Description
		}
		if req.Grants != nil {
			a.Grants = slices.Clone(*req.Grants)
		}
		if req.Expires.Set {
			a.Expires = req.Expires.Time
		}
	})
	if err != nil {
		// What it revokes ends although it isn't saved.
		if r := (unsavedRevocation{username: current.Username, id: current.ID}); errors.Is(err, accounts.ErrNotSaved) {
			r.demoted = req.Admin != nil && !*req.Admin && current.Admin
			if req.Grants != nil {
				r.caps = accounts.Lowered(current.Grants, *req.Grants)
			}
			if r.demoted || len(r.caps) > 0 {
				s.revokeUnsaved(r)
			}
		}
		writeError(w, errorStatus(err), err.Error())
		return
	}
	// Saved, this change decides the account's grants and role anew: what
	// an earlier one that couldn't be saved revoked of them no longer
	// applies.
	if req.Grants != nil || req.Admin != nil {
		s.unsaved.forget(current.Username, func(u *unsavedRevocation) {
			if req.Grants != nil {
				u.caps = nil
			}
			if req.Admin != nil {
				u.demoted = false
			}
		})
		s.events.refresh()
	}
	// Changing one's own password or role keeps this session.
	if a.Username == me.Username {
		s.reissueSession(w, r, a)
	}
	writeJSON(w, http.StatusOK, s.toAccountJSON(a))
}

// handleDeleteAccount deletes the account; the sweep its change runs deletes
// its link, its credentials and the shares it made, and closes their
// connections.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	if !s.deleteAccount(w, r.PathValue("username")) {
		return
	}
	s.Log.Info("account deleted", "account", r.PathValue("username"), "by", me.Username)
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccount deletes the named account, writing the error when it
// can't. When the deletion isn't saved the account ends anyway: it is held
// deleted in memory, which closes its live connections and refuses its
// reconnects, until the sweep saves the deletion.
func (s *Server) deleteAccount(w http.ResponseWriter, username string) bool {
	s.revoking.Lock()
	defer s.revoking.Unlock()
	before, _ := s.Accounts.Get(username)
	err := s.Accounts.Delete(username)
	switch {
	case err == nil:
		return true
	case errors.Is(err, accounts.ErrNotFound), errors.Is(err, accounts.ErrLastAdmin):
		writeError(w, errorStatus(err), err.Error())
	default:
		s.Log.Error("deleting account", "account", username, "err", err)
		s.revokeUnsaved(unsavedRevocation{username: before.Username, id: before.ID, deleted: true})
		s.closeAccount(username)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
	return false
}

// handleAccountLink issues a new login link for a token account, replacing
// its unused one.
func (s *Server) handleAccountLink(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !requireAdmin(w, me) {
		return
	}
	a, ok := s.Accounts.Get(r.PathValue("username"))
	if !ok {
		writeError(w, http.StatusNotFound, accounts.ErrNotFound.Error())
		return
	}
	if a.HasPassword() {
		writeError(w, http.StatusBadRequest, "password accounts sign in with their password")
		return
	}
	s.writeNewLink(w, r, a, me)
}

// writeNewLink issues a new login link for a by me and answers with it.
func (s *Server) writeNewLink(w http.ResponseWriter, r *http.Request, a, me accounts.Account) {
	code, err := s.links.issue(a, me.Username)
	if err != nil {
		s.Log.Error("issuing a login link", "account", a.Username, "err", err)
		writeError(w, http.StatusInternalServerError, "the link couldn't be saved")
		return
	}
	s.Log.Info("login link issued", "account", a.Username, "by", me.Username)
	writeJSON(w, http.StatusOK, map[string]string{"link": s.linkURL(r, code)})
}

// --- client credentials ------------------------------------------------------------------

type credentialJSON struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Name     string     `json:"name"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"lastUsed"`
	Current  bool       `json:"current"`
}

// writeCredentials answers with the credentials of the account with this
// ID, marking the one making the request.
func (s *Server) writeCredentials(w http.ResponseWriter, r *http.Request, accountID string) {
	mine, _ := s.credentialOf(r)
	out := []credentialJSON{}
	for _, c := range s.credentials.list(accountID) {
		out = append(out, credentialJSON{ID: c.ID, Kind: c.Kind, Name: c.Name, Created: c.Created, LastUsed: c.LastUsed, Current: c.ID == mine.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteCredential deletes credential id of the account with this ID and
// closes its connections.
func (s *Server) deleteCredential(w http.ResponseWriter, accountID, id string) {
	if !slices.ContainsFunc(s.credentials.list(accountID), func(c credential) bool { return c.ID == id }) {
		writeError(w, http.StatusNotFound, "no such credential")
		return
	}
	if err := s.credentials.remove(func(c credential) bool { return c.ID == id && c.AccountID == accountID }); err != nil {
		s.Log.Error("deleting a client credential", "credential", id, "err", err)
		writeError(w, http.StatusInternalServerError, "the credential couldn't be deleted")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOwnCredentials(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	s.writeCredentials(w, r, me.ID)
}

func (s *Server) handleDeleteOwnCredential(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	s.deleteCredential(w, me.ID, r.PathValue("id"))
}

// adminAccount resolves {username} for an admin.
func (s *Server) adminAccount(w http.ResponseWriter, r *http.Request) (accounts.Account, bool) {
	me, _ := s.account(r)
	if !requireAdmin(w, me) {
		return accounts.Account{}, false
	}
	a, ok := s.Accounts.Get(r.PathValue("username"))
	if !ok {
		writeError(w, http.StatusNotFound, accounts.ErrNotFound.Error())
	}
	return a, ok
}

func (s *Server) handleAccountCredentials(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.adminAccount(w, r); ok {
		s.writeCredentials(w, r, a.ID)
	}
}

func (s *Server) handleDeleteAccountCredential(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.adminAccount(w, r); ok {
		s.deleteCredential(w, a.ID, r.PathValue("id"))
	}
}
