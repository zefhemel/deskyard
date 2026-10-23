package server

import (
	"context"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
	"io"
	"net/http"
	"sync"
	"time"
)

func (s *Server) handleTerminalSessions(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	if c.State != "running" {
		writeError(w, 409, "start the desktop before listing terminals")
		return
	}
	if c.ImageLabels["deskyard.terminal"] != "tmux-v1" {
		writeError(w, 409, "upgrade this desktop image to use shared terminals (requires deskyard.terminal=tmux-v1)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-list"}, User: "user", WorkDir: "/workspace", Env: execEnv})
	if err != nil {
		writeError(w, 502, "could not list shared terminals; upgrade the desktop image if its helper is unavailable")
		return
	}
	closeProcess := sync.OnceFunc(func() {
		// Docker demultiplexes into synchronous pipes. Socket closure alone
		// cannot release a writer already blocked on a pipe after overflow.
		for _, stream := range []io.Reader{p.Stdout, p.Stderr} {
			if closer, ok := stream.(io.Closer); ok {
				_ = closer.Close()
			}
		}
		p.Close()
	})
	defer closeProcess()
	stop := context.AfterFunc(ctx, closeProcess)
	defer stop()
	if p.Stderr != nil {
		// Discard continuously with bounded memory until EOF or deadline cleanup.
		go io.Copy(io.Discard, p.Stderr)
	}
	b, err := io.ReadAll(io.LimitReader(p.Stdout, terminal.MaxListOutput+1))
	if err != nil || len(b) > terminal.MaxListOutput {
		writeError(w, 502, "could not read shared terminal sessions")
		return
	}
	code, err := p.Wait()
	if err != nil || code != 0 || ctx.Err() != nil {
		writeError(w, 502, "could not list shared terminals; upgrade the desktop image if its helper is unavailable")
		return
	}
	sessions, err := terminal.ParseSessions(b)
	if err != nil {
		writeError(w, 502, "invalid shared terminal session list")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, struct {
		Sessions []terminal.Session `json:"sessions"`
	}{sessions})
}
