package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zefhemel/container-computer/supervisor/internal/cli/desktop"
	"github.com/zefhemel/container-computer/supervisor/internal/cli/execws"
)

func runDesktop(a *App, c *call) error {
	action := c.cmd.Name
	if err := desktop.Validate(action, c.args); err != nil {
		var ue *desktop.UsageError
		if errors.As(err, &ue) {
			return usagef(c.cmd, "%s", ue.Msg)
		}
		return err
	}
	cl, err := a.target()
	if err != nil {
		return err
	}
	if cl == nil {
		env := desktop.Env{Stdout: a.Stdout, Stderr: a.Stderr, Getenv: a.Getenv}
		err := env.RunLocal(c.ctx, action, c.args)
		var xe *desktop.ExitError
		if errors.As(err, &xe) {
			return exitCode(xe.Code)
		}
		return err
	}

	// Remotely: run the computer's own `computer` through exec. A FILE
	// argument is a path on this machine, so the PNG comes back on stdout.
	file, rest := desktop.OutputFile(action, c.args)
	argv := append([]string{"computer", action}, rest...)
	out := a.Stdout
	var tmp *os.File
	switch action {
	case "screenshot", "zoom":
		argv = append(argv, "-")
		if file != "" && file != "-" {
			tmp, err = os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+".*")
			if err != nil {
				return err
			}
			defer os.Remove(tmp.Name())
			out = tmp
		}
	}
	req := execws.Request{Argv: argv}
	if s := a.Getenv("COMPUTER_SETTLE"); s != "" {
		req.Env = map[string]string{"COMPUTER_SETTLE": s}
	}
	code, err := execws.Run(c.ctx, cl, req, execws.IO{Stdout: out, Stderr: a.Stderr})
	if tmp != nil {
		cerr := tmp.Close()
		if err == nil && code == 0 {
			if cerr != nil {
				return cerr
			}
			_ = os.Chmod(tmp.Name(), 0o644)
			if err := os.Rename(tmp.Name(), file); err != nil {
				return fmt.Errorf("writing %s: %v", file, err)
			}
		}
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return exitCode(code)
	}
	return nil
}
