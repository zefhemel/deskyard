package app

import (
	"errors"
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
	if c.cmd.Name == "terminal-new" {
		return terminal.New(ctx, a.Stdin, a.Stdout, a.Stderr)
	}
	if c.cmd.Name == "terminal-list" {
		return terminal.List(ctx, a.Stdout)
	}
	if c.cmd.Name == "terminal-lease" {
		return terminal.Lease(ctx, c.args[0], c.args[1], a.Stdin, a.Stdout)
	}
	return terminal.AttachPrepared(ctx, c.args[0], a.Stdin, a.Stdout, a.Stderr)
}
