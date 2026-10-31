package server

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"net/http"
)

func (s *Server) handleNetworkServices(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Manage)
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
	release, err := s.Environments.Reserve(c.Name, "")
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	id := s.jobs.start(a.Username, c.Name, func(ctx context.Context, progress func(string, float64)) error {
		defer release()
		defer s.publishEnvironment(context.WithoutCancel(ctx), c.Name)
		progress("Applying HTTP service exposure", -1)
		return s.Environments.Networking.UpdateServices(ctx, c.Name, rules)
	})
	s.publishEnvironment(r.Context(), c.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}
