package app

import (
	"errors"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/execws"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func runTerminal(a *App, c *call) error {
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	callCopy := *c
	callCopy.ctx = ctx
	c = &callCopy
	name := "main"
	if len(c.args) > 0 {
		name = c.args[0]
	}
	if !terminal.ValidSession(name) {
		return usagef(c.cmd, "invalid session name: use 1–64 letters, digits, underscores or dashes, starting with a letter or digit")
	}
	var err error
	if c.cmd.Name == "terminal-helper" {
		err = terminal.Attach(ctx, name, a.Stdin, a.Stdout, a.Stderr)
	} else {
		cl, e := a.target()
		if e != nil {
			return e
		}
		if cl == nil {
			err = terminal.Attach(ctx, name, a.Stdin, a.Stdout, a.Stderr)
		} else {
			code, e := a.interactive(c, cl, execws.Request{Argv: terminal.Command(name), Cwd: "/workspace", TTY: true})
			if e != nil {
				return e
			}
			if code != 0 {
				return exitCode(code)
			}
		}
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return exitCode(xe.ExitCode())
	}
	return err
}

func runTerminalRuntime(a *App, c *call) error {
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch c.cmd.Name {
	case "terminal-new":
		return terminal.New(ctx, a.Stdin, a.Stdout, a.Stderr)
	case "terminal-pick":
		return terminal.Pick(ctx, a.Stdin, a.Stdout, a.Stderr)
	case "terminal-list":
		return terminal.List(ctx, a.Stdout)
	case "terminal-create":
		return terminal.Create(ctx, a.Stdout)
	case "terminal-kill":
		if !terminal.ValidSession(c.args[0]) {
			return usagef(c.cmd, "invalid session name")
		}
		return noSession(a, terminal.Kill(ctx, c.args[0]))
	case "terminal-lease":
		join := len(c.args) > 2
		if join && c.args[2] != "join" {
			return usagef(c.cmd, "the third argument must be join")
		}
		return noSession(a, terminal.Lease(ctx, c.args[0], c.args[1], join, a.Stdin, a.Stdout))
	}
	return terminal.AttachPrepared(ctx, c.args[0], a.Stdin, a.Stdout, a.Stderr)
}

// runTerminalChanged is the shared tmux server's hook for session and client
// changes. The supervisor lists the sessions again when it sees the event.
func runTerminalChanged(a *App, c *call) error {
	activity.Send(activity.Event{Agent: "tmux", Kind: "terminals"})
	return nil
}

// noSession gives a missing session its own exit code, so the supervisor can
// tell it from a failure.
func noSession(a *App, err error) error {
	if errors.Is(err, terminal.ErrNoSession) {
		fmt.Fprintf(a.Stderr, "deskyard: %v\n", err)
		return exitCode(3)
	}
	return err
}
