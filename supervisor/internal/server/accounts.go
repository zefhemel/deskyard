package server

import (
	"net/http"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

type accountJSON struct {
	Username  string     `json:"username"`
	Admin     bool       `json:"admin"`
	Desktops  []string   `json:"desktops"` // names
	Created   time.Time  `json:"created"`
	LastLogin *time.Time `json:"lastLogin"`
}

// desktopNames maps desktop IDs to names and back.
func (s *Server) desktopNames(r *http.Request) (byID, byName map[string]string) {
	byID, byName = map[string]string{}, map[string]string{}
	list, _ := s.Desktops.List(r.Context())
	for _, c := range list {
		if c.ID != "" {
			byID[c.ID], byName[c.Name] = c.Name, c.ID
		}
	}
	return
}

func toAccountJSON(a accounts.Account, byID map[string]string) accountJSON {
	j := accountJSON{Username: a.Username, Admin: a.Admin, Desktops: []string{}, Created: a.Created, LastLogin: since(a.LastLogin)}
	for _, id := range a.Desktops {
		if n, ok := byID[id]; ok {
			j.Desktops = append(j.Desktops, n)
		}
	}
	return j
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !me.Admin {
		// Everyone may know the usernames (avatars, who's viewing).
		out := []map[string]string{}
		for _, a := range s.Accounts.List() {
			out = append(out, map[string]string{"username": a.Username})
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	byID, _ := s.desktopNames(r)
	out := []accountJSON{}
	for _, a := range s.Accounts.List() {
		out = append(out, toAccountJSON(a, byID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) idsFor(r *http.Request, names []string) ([]string, string) {
	_, byName := s.desktopNames(r)
	ids := []string{}
	for _, n := range names {
		id, ok := byName[n]
		if !ok {
			return nil, n
		}
		ids = append(ids, id)
	}
	return ids, ""
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Username, Password string
		Admin              bool
		Desktops           []string
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	ids, missing := s.idsFor(r, req.Desktops)
	if missing != "" {
		writeError(w, http.StatusBadRequest, "no desktop "+missing)
		return
	}
	a, err := s.Accounts.Create(req.Username, req.Password, req.Admin, ids)
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("account created", "account", a.Username, "admin", a.Admin, "by", me.Username)
	byID, _ := s.desktopNames(r)
	s.events.refresh()
	writeJSON(w, http.StatusCreated, toAccountJSON(a, byID))
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	var req struct {
		Password *string   `json:"password"`
		Admin    *bool     `json:"admin"`
		Desktops *[]string `json:"desktops"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	var ids []string
	if req.Desktops != nil {
		var missing string
		if ids, missing = s.idsFor(r, *req.Desktops); missing != "" {
			writeError(w, http.StatusBadRequest, "no desktop "+missing)
			return
		}
	}
	if req.Password != nil && *req.Password == "" {
		writeError(w, http.StatusBadRequest, "the password can't be empty")
		return
	}
	a, err := s.Accounts.Update(r.PathValue("username"), func(a *accounts.Account) {
		if req.Password != nil {
			a.PasswordHash = auth.HashPassword(*req.Password)
		}
		if req.Admin != nil {
			a.Admin = *req.Admin
		}
		if req.Desktops != nil {
			a.Desktops = ids
		}
	})
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	// Changing one's own password or role keeps this session.
	if a.Username == me.Username {
		s.Sessions.Issue(w, a.Username, a.SessionEpoch)
	}
	byID, _ := s.desktopNames(r)
	writeJSON(w, http.StatusOK, toAccountJSON(a, byID))
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	me, _ := s.account(r)
	if !auth.Can(me, auth.ManageAccounts, "") {
		writeError(w, http.StatusForbidden, "only an admin can do that")
		return
	}
	if err := s.Accounts.Delete(r.PathValue("username")); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("account deleted", "account", r.PathValue("username"), "by", me.Username)
	w.WriteHeader(http.StatusNoContent)
}
