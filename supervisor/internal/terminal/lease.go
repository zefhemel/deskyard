package terminal

import (
	"context"
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
func Lease(ctx context.Context, name, id string, stdin io.Reader, stdout io.Writer) error {
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
	if _, err := ensureSession(ctx, name); err != nil {
		return err
	}
	// -t takes a literal existing session/group name (not target-prefix syntax).
	// Finish creation before cleanup even if EOF arrives mid-command. Otherwise
	// killing the local tmux command could leave creation queued at its server.
	prepare, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tmuxCommand(prepare, "new-session", "-d", "-s", group, "-t", name).Run(); err != nil {
		return fmt.Errorf("prepare shared attachment: %w", err)
	}
	if err := tmuxCommand(ctx, "set-option", "-t", "="+group+":", "detach-on-destroy", "on").Run(); err != nil {
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
