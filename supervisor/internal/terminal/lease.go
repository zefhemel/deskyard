package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var attachmentID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidAttachment(id string) bool     { return attachmentID.MatchString(id) }
func attachmentSession(id string) string { return "_deskyard_" + id }
func tmuxCommand(ctx context.Context, args ...string) *exec.Cmd {
	base := []string{"-u", "-S", filepath.Join(RuntimeDir, "tmux.sock"), "-f", Config}
	return exec.CommandContext(ctx, "tmux", append(base, args...)...)
}

// Lease owns one private grouped session. Docker closes non-TTY stdin when the
// transport disappears; that EOF is the lifetime signal, even after supervisor
// death. The base session retains the shared windows and their shell processes.
// A join-only lease never creates the base session: when it is missing, the
// lease writes "ended" and returns ErrNoSession, so a reconnect cannot revive a
// session that someone ended.
func Lease(ctx context.Context, name, id string, join bool, stdin io.Reader, stdout io.Writer) error {
	if !ValidAttachment(id) || !ValidSession(name) {
		return fmt.Errorf("invalid terminal lease identity")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, stdin); cancel() }()
	group := attachmentSession(id)
	// Install cleanup before creation: cancellation can race a command which
	// creates the group successfully but reports an interrupted subprocess.
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = tmuxCommand(cleanup, "kill-session", "-t", "="+group).Run()
	}()
	ended := func() error {
		if _, err := io.WriteString(stdout, "ended\n"); err != nil {
			return err
		}
		return ErrNoSession
	}
	// A join resolves the existing base below and never starts a shell.
	if join {
		if _, err := runtimeArgs(ctx); err != nil {
			return err
		}
	} else if _, err := ensureSession(ctx, name); err != nil {
		return err
	}
	// Finish creation before cleanup even if EOF arrives mid-command. Otherwise
	// killing the local tmux command could leave creation queued at its server.
	prepare, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := groupWith(prepare, name, group); err != nil {
		if errors.Is(err, ErrNoSession) {
			if join {
				return ended()
			}
			return fmt.Errorf("prepare shared attachment: %w", err)
		}
		return err
	}
	if err := tmuxCommand(ctx, "set-option", "-t", "="+group+":", "detach-on-destroy", "on").Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if _, err := io.WriteString(stdout, "prepared\n"); err != nil {
		return err
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, err := tmuxCommand(ctx, "list-clients", "-t", "="+group, "-F", "#{client_pid}").Output()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if strings.TrimSpace(string(b)) != "" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if _, err := io.WriteString(stdout, "ready\n"); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

// groupWith creates the private group sharing exactly the windows of the base
// session. new-session -t prefix-matches names and, when nothing matches,
// silently starts a standalone session in a new group of that name. So group
// against the base's immutable ID, then verify the result: the base still
// exists under its name and the new session is in its group. Otherwise the
// group is removed and the base counts as ended.
func groupWith(ctx context.Context, name, group string) error {
	base, err := tmuxCommand(ctx, "display-message", "-p", "-t", "="+name+":", "#{session_id}|#{session_group}").Output()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNoSession
	}
	// display-message succeeds with empty fields when its target is missing.
	id, baseGroup, _ := strings.Cut(strings.TrimSpace(string(base)), "|")
	if !strings.HasPrefix(id, "$") {
		return ErrNoSession
	}
	// tmux names a new group after its base session, even when targeted by
	// immutable ID. A rename can leave that name owned by another group.
	if baseGroup == "" {
		reserved, err := groupNameExists(ctx, name)
		if err != nil {
			return err
		}
		if reserved {
			return fmt.Errorf("session name %q conflicts with an existing terminal attachment group; rename this session before attaching", name)
		}
	}
	if err := tmuxCommand(ctx, "new-session", "-d", "-s", group, "-t", id).Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("prepare shared attachment: %w", err)
	}
	verify := func() bool {
		b, err := tmuxCommand(ctx, "display-message", "-p", "-t", id+":", "#{session_name}|#{session_group}").Output()
		if err != nil {
			return false
		}
		g, err := tmuxCommand(ctx, "display-message", "-p", "-t", "="+group+":", "#{session_group}").Output()
		baseName, baseGroup, _ := strings.Cut(strings.TrimSpace(string(b)), "|")
		return err == nil && baseName == name && baseGroup != "" && strings.TrimSpace(string(g)) == baseGroup
	}
	if !verify() {
		_ = tmuxCommand(ctx, "kill-session", "-t", "="+group).Run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNoSession
	}
	return nil
}

// Group names survive renames. Do not reuse them for new public sessions:
// tmux otherwise joins the old group when preparing their first attachment.
func groupNameExists(ctx context.Context, name string) (bool, error) {
	out, err := queryServer(ctx, "list-sessions", "-F", "#{session_group}")
	if err != nil {
		return false, err
	}
	for _, group := range lines(out) {
		if group == name {
			return true, nil
		}
	}
	return false, nil
}

// AttachPrepared never creates a session: a canceled lease cannot be resurrected
// by a late Docker exec. The random group is outside the public name grammar.
func AttachPrepared(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer) error {
	if !ValidAttachment(id) {
		return fmt.Errorf("invalid terminal attachment identity")
	}
	cmd := tmuxCommand(ctx, "attach-session", "-t", "="+attachmentSession(id))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}
