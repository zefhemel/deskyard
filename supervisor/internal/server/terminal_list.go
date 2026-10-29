package server

import (
	"errors"
	"net/http"

	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

// handleTerminalSessions lists the computer's shared terminal sessions
// now, refreshing the cache that the computer JSON carries.
func (s *Server) handleTerminalSessions(w http.ResponseWriter, r *http.Request) {
	c, ok := s.terminalSessionsComputer(w, r, "listing", nil)
	if !ok {
		return
	}
	sessions, err := s.terminalSessions.refresh(r.Context(), c)
	switch {
	case errors.Is(err, errTerminalHelper):
		writeError(w, 502, "could not list shared terminals; upgrade the desktop image if its helper is unavailable")
		return
	case err != nil:
		writeError(w, 502, "invalid shared terminal session list")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, struct {
		Sessions []terminal.Session `json:"sessions"`
	}{sessions})
}
