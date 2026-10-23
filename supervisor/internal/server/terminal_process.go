package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
)

// terminalProcess pairs a TTY attachment with a non-TTY lease. Docker preserves
// TTY execs after socket disconnect, whereas non-TTY stdin EOF reaches the helper.
// The lease removes only its random grouped session, preserving all shared panes.
func (s *Server) terminalProcess(parent context.Context, containerID, session string, cols, rows uint) (*engine.Process, error) {
	ctx, cancel := context.WithCancel(parent)
	startup := time.AfterFunc(10*time.Second, cancel)
	defer startup.Stop()
	transferred := false
	defer func() {
		if !transferred {
			cancel()
		}
	}()
	id := randomID(32)
	env := execEnvFor(map[string]string{"TERM": "xterm-256color"})
	lease, err := s.Eng.Exec(ctx, containerID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-lease", session, id}, User: "user", WorkDir: "/workspace", Env: env, Stdin: true})
	if err != nil {
		return nil, err
	}
	closeLease := sync.OnceFunc(func() { _ = lease.Stdin.Close(); lease.Close() })
	stopLease := context.AfterFunc(ctx, closeLease)
	defer func() {
		if !transferred {
			stopLease()
			closeLease()
		}
	}()
	if lease.Stderr != nil {
		go io.Copy(io.Discard, lease.Stderr)
	}
	control := bufio.NewReader(io.LimitReader(lease.Stdout, 64))
	line, err := control.ReadString('\n')
	if err != nil || line != "prepared\n" {
		return nil, fmt.Errorf("shared terminal lease did not prepare")
	}
	attach, err := s.Eng.Exec(ctx, containerID, engine.ExecSpec{Cmd: []string{"deskyard", "terminal-attach", id}, User: "user", WorkDir: "/workspace", Env: env, Tty: true, Stdin: true, Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	closeAttach := sync.OnceFunc(attach.Close)
	stopAttach := context.AfterFunc(ctx, closeAttach)
	defer func() {
		if !transferred {
			stopAttach()
			closeAttach()
		}
	}()
	line, err = control.ReadString('\n')
	if err != nil || line != "ready\n" {
		return nil, fmt.Errorf("shared terminal attachment did not become ready")
	}
	// tmux's first terminal output follows raw-mode setup. Keep it for the
	// renderer, and don't forward early input while the outer PTY can still flush it.
	first := make([]byte, 1)
	if _, err := io.ReadFull(attach.Stdout, first); err != nil {
		return nil, fmt.Errorf("shared terminal attachment produced no output: %w", err)
	}
	closeAll := sync.OnceFunc(func() { cancel(); stopAttach(); stopLease(); closeAttach(); closeLease() })
	p := *attach
	p.Stdout = io.MultiReader(bytes.NewReader(first), attach.Stdout)
	p.Close = closeAll
	p.Wait = func() (int, error) {
		code, err := attach.Wait()
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return 0, nil
		}
		return code, err
	}
	p.Stdin = terminalInputWriter{Writer: attach.Stdin, close: closeAll}
	transferred = true
	return &p, nil
}

type terminalInputWriter struct {
	io.Writer
	close func()
}

func (w terminalInputWriter) Close() error { w.close(); return nil }

// Only the owned shared-terminal command gets this lifecycle; arbitrary agent
// execs retain their existing semantics and are never killed by terminal cleanup.
func terminalSession(argv []string, tty bool) (string, bool) {
	if tty && len(argv) == 3 && argv[0] == "deskyard" && argv[1] == "terminal-helper" && terminal.ValidSession(argv[2]) {
		return argv[2], true
	}
	return "", false
}
