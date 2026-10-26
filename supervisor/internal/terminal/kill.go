package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNoSession reports that a public session does not exist (any more).
var ErrNoSession = errors.New("terminal session does not exist")

// Kill ends a public session for every client. It kills the session's windows
// rather than the session: a killed window leaves every session grouped with
// it, so no browser lease, including one that groups in concurrently, can keep
// the shell alive, and sessions left without windows are destroyed. Private
// leases that remain are removed afterwards; other public sessions are not.
func Kill(ctx context.Context, name string) error {
	if !ValidSession(name) {
		return fmt.Errorf("invalid terminal session %q", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A missing server (nil output) has no sessions either.
	// display-message succeeds with empty fields when its target is missing.
	info, err := queryServer(ctx, "display-message", "-p", "-t", "="+name+":", "#{session_id}|#{session_group}")
	id, group, _ := strings.Cut(strings.TrimSpace(string(info)), "|")
	if err != nil || !strings.HasPrefix(id, "$") {
		return missing(ctx, name, err)
	}
	windows, err := queryServer(ctx, "list-windows", "-t", "="+name, "-F", "#{window_id}")
	if err != nil {
		return missing(ctx, name, err)
	}
	for _, window := range lines(windows) {
		// A window whose shell just exited is already gone.
		if err := tmuxCommand(ctx, "kill-window", "-t", window).Run(); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if group != "" {
		sessions, err := queryServer(ctx, "list-sessions", "-F", "#{session_name}|#{session_group}")
		if err != nil {
			return err
		}
		for _, line := range lines(sessions) {
			if session, g, _ := strings.Cut(line, "|"); g == group && privateSession(session) {
				_ = tmuxCommand(ctx, "kill-session", "-t", "="+session).Run()
			}
		}
	}
	// A window opened meanwhile keeps the session; end it all the same.
	if tmuxCommand(ctx, "has-session", "-t", "="+name).Run() == nil {
		if err := tmuxCommand(ctx, "kill-session", "-t", "="+name).Run(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("end shared terminal: %w", err)
		}
	}
	return ctx.Err()
}

// missing tells a session that does not exist from a failed query. It never
// probes on a canceled context, which would make every failure look missing.
func missing(ctx context.Context, name string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil || tmuxCommand(ctx, "has-session", "-t", "="+name).Run() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNoSession
	}
	return err
}
