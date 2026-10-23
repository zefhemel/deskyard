package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"net/http"
)

type optionalVPN struct {
	Set bool
	ID  string
}

func (o *optionalVPN) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.ID = ""
		return nil
	}
	return json.Unmarshal(b, &o.ID)
}

type vpnTester interface {
	Test(context.Context, vpn.Input) (vpn.Status, error)
	TestSaved(context.Context, string, vpn.Input) (vpn.Status, error)
}

func vpnError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vpn.ErrNotFound):
		writeError(w, 400, "VPN connection not found")
	case errors.Is(err, desktops.ErrRecoveryRequired):
		writeError(w, 409, err.Error())
	case errors.Is(err, vpn.ErrConflict):
		writeError(w, 409, "VPN connection or desktop references changed; refresh and confirm again")
	case errors.Is(err, vpn.ErrValidation):
		writeError(w, 400, err.Error())
	default:
		writeError(w, 500, "VPN operation failed")
	}
}
func (s *Server) handleListVPN(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	cs, err := s.Desktops.Networking.Connections.List()
	if err != nil {
		vpnError(w, err)
		return
	}
	type entry struct {
		vpn.Connection
		Desktops []desktops.ConnectionReference `json:"desktops"`
	}
	out := []entry{}
	for _, c := range cs {
		out = append(out, entry{c, s.Desktops.Networking.References(c.ID)})
	}
	writeJSON(w, 200, map[string]any{"connections": out, "countries": vpn.Countries()})
}
func (s *Server) handleCreateVPN(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var in vpn.Input
	if readJSON(r, &in) != nil {
		writeError(w, 400, "bad request")
		return
	}
	c, err := s.Desktops.Networking.Connections.Create(in)
	if err != nil {
		vpnError(w, err)
		return
	}
	writeJSON(w, 201, c)
}
func (s *Server) handleDeleteVPN(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	id := r.PathValue("id")
	if err := s.Desktops.Networking.DeleteConnection(id); err != nil {
		if errors.Is(err, vpn.ErrConflict) {
			writeJSON(w, 409, map[string]any{"error": "VPN connection is referenced by desktops", "desktops": s.Desktops.Networking.References(id)})
			return
		}
		vpnError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) handleTestVPN(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var req struct {
		vpn.Input
		ConnectionID string `json:"connectionId"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "bad request")
		return
	}
	if req.ConnectionID != "" {
		if _, err := s.Desktops.Networking.Connections.Get(req.ConnectionID); err != nil {
			vpnError(w, err)
			return
		}
	}
	id := s.jobs.startResult(a.Username, true, func(ctx context.Context, progress func(string, float64)) (any, error) {
		progress("Testing VPN connection", -1)
		var status vpn.Status
		var err error
		if req.ConnectionID != "" {
			status, err = s.vpnTests.TestSaved(ctx, req.ConnectionID, req.Input)
		} else {
			status, err = s.vpnTests.Test(ctx, req.Input)
		}
		if err != nil {
			return nil, errors.New("VPN test failed; check service credentials, country/server availability and host internet access")
		}
		return status, nil
	})
	writeJSON(w, 202, map[string]string{"job": id})
}
func (s *Server) handleUpdateVPN(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	id := r.PathValue("id")
	var req struct {
		vpn.Input
		ExpectedRevision   uint64   `json:"expectedRevision"`
		AffectedDesktopIDs []string `json:"affectedDesktopIds"`
		ConfirmRestart     bool     `json:"confirmRestart"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "bad request")
		return
	}
	n := s.Desktops.Networking
	if err := n.CheckUpdate(id, req.ExpectedRevision, req.AffectedDesktopIDs); err != nil {
		vpnError(w, err)
		return
	}
	refs := n.References(id)
	if len(refs) > 0 && !req.ConfirmRestart {
		writeJSON(w, 409, map[string]any{"error": "confirmRestart required; affected desktop sessions will disconnect", "desktops": refs})
		return
	}
	jobID := s.jobs.startCommittedResult(a.Username, func(ctx context.Context, progress func(string, float64), commit func(func() error) error) (any, error) {
		progress("Validating candidate VPN connection", -1)
		if _, err := s.vpnTests.TestSaved(ctx, id, req.Input); err != nil {
			return nil, errors.New("VPN validation failed; saved connection is unchanged. Check service credentials, country/server availability and host internet access")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c, results, err := n.UpdateConnection(ctx, id, req.ExpectedRevision, req.AffectedDesktopIDs, req.Input, progress, func(save func() error) error {
			if err := commit(save); err != nil {
				return err
			}
			for _, ref := range refs {
				s.viewers.closeAll(ref.Name)
			}
			return nil
		})
		if c.ID != "" {
			for _, ref := range refs {
				s.publishDesktop(context.WithoutCancel(ctx), ref.Name)
			}
		}
		if err != nil {
			return nil, errors.New("VPN update failed; refresh connection and desktop status before retrying")
		}
		return map[string]any{"connection": c, "desktops": results}, nil
	})
	writeJSON(w, 202, map[string]any{"job": jobID, "desktops": refs})
}
