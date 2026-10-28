// Package terminal joins persistent, image-owned shared tmux sessions.
package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/term"
)

// RuntimeDir and Config are variables only so tests can run a private server.
var RuntimeDir = "/run/workspace/deskyard-terminal"
var Config = "/etc/deskyard/tmux.conf"

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func ValidSession(name string) bool { return sessionName.MatchString(name) }
func Command(name string) []string  { return []string{"deskyard", "terminal-helper", name} }

// Attach cancels only the tmux client: the server and its shell keep running.
// tmux serializes server startup and session creation internally; a concurrent
// duplicate creation is successful when the exact session now exists.
func Attach(ctx context.Context, name string, stdin io.Reader, stdout, stderr io.Writer) error {
	return attachAt(ctx, name, "/workspace", false, stdin, stdout, stderr)
}

// New creates an independent native shell in the emulator's working directory.
func New(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	name, err := createNext(ctx, cwd)
	if err != nil {
		return err
	}
	return attachAt(ctx, name, cwd, true, stdin, stdout, stderr)
}

// Create starts the next free native-style session detached in /workspace and
// prints its name, for clients that attach separately.
func Create(ctx context.Context, out io.Writer) error {
	name, err := createNext(ctx, "/workspace")
	if err != nil {
		return err
	}
	// The session exists now; this only applies its public title.
	if _, err := ensureSessionAt(ctx, name, "/workspace"); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, name)
	return err
}

func createNext(ctx context.Context, cwd string) (string, error) {
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	base, err := runtimeArgs(startup)
	if err != nil {
		return "", err
	}
	// new-session is atomic in tmux. A competing native window that claims the
	// same candidate makes us try the next number, never join its shell.
	for n := 1; n <= 10000; n++ {
		name := fmt.Sprintf("terminal-%d", n)
		run := func(args ...string) error {
			return exec.CommandContext(startup, "tmux", append(append([]string{}, base...), args...)...).Run()
		}
		if err := run("new-session", "-d", "-s", name, "-c", cwd, "bash", "-l"); err != nil {
			if startup.Err() != nil {
				return "", startup.Err()
			}
			if run("has-session", "-t", "="+name) == nil {
				continue
			}
			return "", fmt.Errorf("create native shared terminal: %w", err)
		}
		return name, nil
	}
	return "", fmt.Errorf("too many native terminal sessions")
}

func attachAt(ctx context.Context, name, cwd string, native bool, stdin io.Reader, stdout, stderr io.Writer) error {
	base, err := ensureSessionAt(ctx, name, cwd)
	if err != nil {
		return err
	}
	// tmux owns the native tty through a descriptor sent to its server. Save
	// attributes before attachment so the bounded fallback can restore them even
	// when that server is unresponsive. Non-native streams have no tty to restore.
	var tty *os.File
	var saved *term.State
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		tty = f
		saved, err = term.Save(int(f.Fd()))
		if err != nil {
			return fmt.Errorf("save terminal state: %w", err)
		}
	}
	target := "=" + name
	cmd := exec.CommandContext(ctx, "tmux", append(base, "attach-session", "-t", target)...)
	// SIGTERM requests tmux's MSG_EXITING/MSG_EXITED handshake, which closes
	// the server tty before the client exits. Default CommandContext SIGKILL
	// would return before asynchronous server cleanup restores native attributes.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	if native {
		cmd.Env = append(os.Environ(), "TMUX=", "TMUX_PANE=")
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	if saved != nil {
		if restoreErr := term.Restore(int(tty.Fd()), saved); restoreErr != nil {
			return fmt.Errorf("restore terminal state: %w", restoreErr)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func ensureSession(ctx context.Context, name string) ([]string, error) {
	return ensureSessionAt(ctx, name, "/workspace")
}

func ensureSessionAt(ctx context.Context, name, cwd string) ([]string, error) {
	if !ValidSession(name) {
		return nil, fmt.Errorf("invalid terminal session %q", name)
	}
	base, err := runtimeArgs(ctx)
	if err != nil {
		return nil, err
	}
	run := func(args ...string) error {
		return exec.CommandContext(ctx, "tmux", append(append([]string{}, base...), args...)...).Run()
	}
	target := "=" + name
	if err := run("has-session", "-t", target); err != nil {
		if err := run("new-session", "-d", "-s", name, "-c", cwd, "bash", "-l"); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if check := run("has-session", "-t", target); check != nil {
				return nil, fmt.Errorf("start shared terminal (upgrade the desktop image if tmux is missing): %w", err)
			}
		}
	}

	// All public base sessions, including browser/CLI-created ones, carry the
	// same literal public identity when a native client switches into them.
	// Resolve exactly once and update by immutable ID, never a name prefix.
	id, err := exec.CommandContext(ctx, "tmux", append(append([]string{}, base...), "display-message", "-p", "-t", target+":", "#{session_id}")...).Output()
	if err != nil {
		return nil, fmt.Errorf("resolve shared terminal title: %w", err)
	}
	for _, option := range [][2]string{{"set-titles-string", name}, {"set-titles", "on"}} {
		if err := run("set-option", "-t", strings.TrimSpace(string(id)), option[0], option[1]); err != nil {
			return nil, fmt.Errorf("set shared terminal title: %w", err)
		}
	}
	return base, nil
}

func runtimeArgs(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Mkdir(RuntimeDir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(RuntimeDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("terminal runtime directory must be a private directory (0700)")
	}
	return []string{"-u", "-S", filepath.Join(RuntimeDir, "tmux.sock"), "-f", Config}, nil
}
