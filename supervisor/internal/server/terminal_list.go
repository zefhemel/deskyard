package server

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
	"net/http"
	"time"
)

func (s *Server) handleTerminalSessions(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	if c.State != "running" {
		writeError(w, 409, "start the environment before listing terminals")
		return
	}
	if c.ImageLabels["deskyard.terminal"] != "tmux-v1" {
		writeError(w, 409, "upgrade this desktop image to use shared terminals (requires deskyard.terminal=tmux-v1)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := engine.Run(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-list"}, User: "user", WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: terminal.MaxListOutput, StderrLimit: 4096})
	if err != nil || result.Code != 0 {
		writeError(w, 502, "could not list shared terminals; upgrade the desktop image if its helper is unavailable")
		return
	}
	sessions, err := terminal.ParseSessions(result.Stdout)
	if err != nil {
		writeError(w, 502, "invalid shared terminal session list")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, struct {
		Sessions []terminal.Session `json:"sessions"`
	}{sessions})
}
