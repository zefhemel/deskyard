package server

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"net/http"
)

func (s *Server) handleNetworkServices(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) { return }
	c, a, ok := s.computerFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		Services *[]vpn.ServiceRule `json:"services"`
	}
	if readJSON(r, &req) != nil || req.Services == nil {
		writeError(w, 400, "services must be an array; use [] to clear")
		return
	}
	rules, err := vpn.NormalizeServiceRules(*req.Services)
	if err != nil {
		vpnError(w, err)
		return
	}
	release, err := s.Computers.Reserve(c.Name, "")
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	id := s.jobs.start(a.Username, c.Name, func(ctx context.Context, progress func(string, float64)) error {
		defer release()
		defer s.publishComputer(context.WithoutCancel(ctx), c.Name)
		progress("Applying HTTP service exposure", -1)
		return s.Computers.Networking.UpdateServices(ctx, c.Name, rules)
	})
	s.publishComputer(r.Context(), c.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}
