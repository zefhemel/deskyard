package server

import (
	"net/http"
	"slices"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// A share is a token account with one grant of an environment, made by
// Share on it. It ends with its sharer's full access (sweep).
type shareJSON struct {
	Username    string          `json:"username"`
	Environment string          `json:"environment"`
	Level       auth.Level      `json:"level"`
	SharedBy    accounts.Sharer `json:"sharedBy"`
	Expires     *time.Time      `json:"expires"`
	Created     time.Time       `json:"created"`
	LastLogin   *time.Time      `json:"lastLogin"`
	LinkPending bool            `json:"linkPending"`
	Connected   bool            `json:"connected"`
}

func (s *Server) toShareJSON(a accounts.Account) shareJSON {
	j := shareJSON{Username: a.Username, Expires: a.Expires, Created: a.Created, LastLogin: since(a.LastLogin),
		LinkPending: s.links.pending(a.ID)}
	if a.SharedBy != nil {
		j.SharedBy = *a.SharedBy
	}
	if len(a.Grants) > 0 {
		j.Environment, j.Level = a.Grants[0].Environment, a.Grants[0].Level
		j.Connected = slices.Contains(s.viewers.names(j.Environment), a.Username) || s.credentials.connected(a.ID)
	}
	return j
}

// sharesOf lists the shares of the named environment, sorted by username.
func (s *Server) sharesOf(name string) []shareJSON {
	out := []shareJSON{}
	for _, a := range s.Accounts.List() {
		if a.IsShare() && len(a.Grants) > 0 && a.Grants[0].Environment == name {
			out = append(out, s.toShareJSON(a))
		}
	}
	return out
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.sharesOf(c.Name))
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	c, me, ok := s.environmentFor(w, r, auth.Observe)
	if !ok {
		return
	}
	if me.IsShare() {
		writeError(w, http.StatusForbidden, "shares can't share")
		return
	}
	if !auth.Can(me, auth.Share, c.Name) {
		writeError(w, http.StatusForbidden, "observe-only access can't do that")
		return
	}
	var req struct {
		Username string
		Level    auth.Level
		Expires  *time.Time
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !auth.ValidLevel(req.Level) {
		writeError(w, http.StatusBadRequest, "the level is full or observe")
		return
	}
	if !checkExpiry(w, req.Expires) {
		return
	}
	a, err := s.Accounts.Create(accounts.NewAccount{Username: req.Username, Expires: req.Expires,
		Grants:   []accounts.Grant{{Environment: c.Name, Level: req.Level}},
		SharedBy: &accounts.Sharer{Username: me.Username, ID: me.ID}})
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	code, err := s.links.issue(a, me.Username)
	if err != nil {
		// A share nobody can sign in as is no use: take it back.
		s.Log.Error("issuing a share's link", "share", a.Username, "err", err)
		if err := s.Accounts.Delete(a.Username); err != nil {
			s.Log.Error("deleting a share without a link", "share", a.Username, "err", err)
		}
		writeError(w, http.StatusInternalServerError, "the link couldn't be saved")
		return
	}
	s.Log.Info("shared", "environment", c.Name, "share", a.Username, "level", req.Level, "by", me.Username)
	s.events.refresh()
	writeJSON(w, http.StatusCreated, map[string]any{"share": s.toShareJSON(a), "link": s.linkURL(r, code)})
}

// shareFor resolves {username} into a share that the signed-in account
// made, or any share for an admin.
func (s *Server) shareFor(w http.ResponseWriter, r *http.Request) (accounts.Account, accounts.Account, bool) {
	me, _ := s.account(r)
	a, ok := s.Accounts.Get(r.PathValue("username"))
	if !ok || !a.IsShare() {
		writeError(w, http.StatusNotFound, "no such share")
		return a, me, false
	}
	if !me.Admin && a.SharedBy.ID != me.ID {
		writeError(w, http.StatusForbidden, "only the person who shared it or an admin can do that")
		return a, me, false
	}
	return a, me, true
}

func (s *Server) handleShareLink(w http.ResponseWriter, r *http.Request) {
	if a, me, ok := s.shareFor(w, r); ok {
		s.writeNewLink(w, r, a, me)
	}
}

// handleRevokeShare deletes the share; the sweep its change runs deletes
// its link and credentials and closes its connections.
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	a, me, ok := s.shareFor(w, r)
	if !ok || !s.deleteAccount(w, a.Username) {
		return
	}
	s.Log.Info("share revoked", "share", a.Username, "by", me.Username)
	w.WriteHeader(http.StatusNoContent)
}
