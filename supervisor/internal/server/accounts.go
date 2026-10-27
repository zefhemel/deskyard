package server

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

type accountJSON struct {
	Username     string     `json:"username"`
	Admin        bool       `json:"admin"`
	Environments []string   `json:"environments"` // environment names
	Created      time.Time  `json:"created"`
	LastLogin    *time.Time `json:"lastLogin"`
}

// toAccountJSON shows a's grants of defined environments.
func (s *Server) toAccountJSON(a accounts.Account) accountJSON {
	j := accountJSON{Username: a.Username, Admin: a.Admin, Environments: []string{}, Created: a.Created, LastLogin: since(a.LastLogin)}
	for _, name := range a.GrantNames() {
		if _, ok := s.Environments.Defs.Get(name); ok {
			j.Environments = append(j.Environments, name)
		}
	}
	return j
}

// fullGrants gives full access to each named environment.
func fullGrants(names []string) []accounts.Grant {
	out := []accounts.Grant{}
	for _, n := range names {
		if !slices.ContainsFunc(out, func(g accounts.Grant) bool { return g.Environment == n }) {
			out = append(out, accounts.Grant{Environment: n, Level: auth.LevelFull})
		}
	}
	return out
}

// accessLevels gives each of usernames its current level of the named
// environment, or full access when it has none, until the access API
// carries levels.
func (s *Server) accessLevels(name string, usernames []string) map[string]auth.Level {
	current := s.Accounts.WithAccess(name)
	out := map[string]auth.Level{}
	for _, u := range usernames {
		u = strings.ToLower(u)
		if l, ok := current[u]; ok {
			out[u] = l
		} else {
			out[u] = auth.LevelFull
		}
	}
	return out
}

// undefined is the first of names without a definition, or "".
func (s *Server) undefined(names []string) string {
	for _, n := range names {
		if _, ok := s.Environments.Defs.Get(n); !ok {
			return n
		}
	}
	return ""
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
	out := []accountJSON{}
	for _, a := range s.Accounts.List() {
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
		Username, Password string
		Admin              bool
		Environments       []string
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if missing := s.undefined(req.Environments); missing != "" {
		writeError(w, http.StatusBadRequest, "no environment "+missing)
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "a password is required")
		return
	}
	a, err := s.Accounts.Create(accounts.NewAccount{Username: req.Username, Password: req.Password, Admin: req.Admin,
		Grants: fullGrants(req.Environments)})
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("account created", "account", a.Username, "admin", a.Admin, "by", me.Username)
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
		Password     *string   `json:"password"`
		Admin        *bool     `json:"admin"`
		Environments *[]string `json:"environments"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Environments != nil {
		if missing := s.undefined(*req.Environments); missing != "" {
			writeError(w, http.StatusBadRequest, "no environment "+missing)
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
		if req.Environments != nil {
			// Kept grants keep their level until this API carries levels.
			grants := fullGrants(*req.Environments)
			for i, g := range grants {
				if l := a.Level(g.Environment); l != auth.LevelNone {
					grants[i].Level = l
				}
			}
			a.Grants = grants
		}
	})
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	// Changing one's own password or role keeps this session.
	if a.Username == me.Username {
		s.reissueSession(w, r, a)
	}
	writeJSON(w, http.StatusOK, s.toAccountJSON(a))
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
