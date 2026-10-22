package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/execws"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/term"
)

func runExec(a *App, c *call) error {
	if len(c.args) == 0 {
		return usagef(c.cmd, "no command given: computer exec -- CMD [ARG...]")
	}
	env := map[string]string{}
	for _, kv := range c.flags["--env"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return usagef(c.cmd, "-e wants K=V, got %q", kv)
		}
		env[k] = v
	}
	req := execws.Request{Argv: c.args, Cwd: c.str("--cwd", ""), TTY: c.bool("--tty")}
	if len(env) > 0 {
		req.Env = env
	}
	jsonOut := c.bool("--json")
	if jsonOut && req.TTY {
		return usagef(c.cmd, "--json and --tty don't go together")
	}

	cl, err := a.target()
	if err != nil {
		return err
	}

	var stdout, stderr io.Writer = a.Stdout, a.Stderr
	var outBuf, errBuf bytes.Buffer
	if jsonOut {
		stdout, stderr = &outBuf, &errBuf
	}

	var code int
	if cl == nil {
		code, err = a.localExec(req, a.Stdin, stdout, stderr)
	} else if req.TTY {
		code, err = a.interactive(c, cl, req)
	} else {
		code, err = execws.Run(c.ctx, cl, req, execws.IO{Stdin: a.Stdin, Stdout: stdout, Stderr: stderr})
	}
	if err != nil {
		return err
	}
	if jsonOut {
		return writeExecJSON(a.Stdout, outBuf.Bytes(), errBuf.Bytes(), code)
	}
	if code != 0 {
		return exitCode(code)
	}
	return nil
}

// writeExecJSON prints {"stdout","stderr","exit"}; non-UTF-8 output goes
// in stdoutBase64/stderrBase64 instead.
func writeExecJSON(w io.Writer, stdout, stderr []byte, code int) error {
	var obj struct {
		Stdout       *string `json:"stdout,omitempty"`
		StdoutBase64 *string `json:"stdoutBase64,omitempty"`
		Stderr       *string `json:"stderr,omitempty"`
		StderrBase64 *string `json:"stderrBase64,omitempty"`
		Exit         int     `json:"exit"`
	}
	put := func(text, b64 **string, b []byte) {
		if utf8.Valid(b) {
			s := string(b)
			*text = &s
		} else {
			s := base64.StdEncoding.EncodeToString(b)
			*b64 = &s
		}
	}
	put(&obj.Stdout, &obj.StdoutBase64, stdout)
	put(&obj.Stderr, &obj.StderrBase64, stderr)
	obj.Exit = code
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(obj)
}

func (a *App) localExec(req execws.Request, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(req.Argv[0], req.Argv[1:]...)
	cmd.Dir = req.Cwd
	if cmd.Dir == "" {
		if home := a.Getenv("HOME"); home != "" {
			cmd.Dir = home
		}
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	var xe *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &xe):
		if code := xe.ExitCode(); code >= 0 {
			return code, nil
		}
		return 128 + 9, nil // killed by a signal
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprintf(stderr, "computer exec: %s: command not found\n", req.Argv[0])
		return 127, nil
	}
	return -1, err
}

func runShell(a *App, c *call) error {
	cl, err := a.target()
	if err != nil {
		return err
	}
	req := execws.Request{Argv: []string{"bash", "-l"}}
	var code int
	if cl == nil {
		code, err = a.localExec(req, a.Stdin, a.Stdout, a.Stderr)
	} else {
		f, ok := a.Stdin.(*os.File)
		if !term.Supported {
			return errors.New("an interactive shell isn't supported on this platform yet: use `computer exec -- CMD`")
		}
		if !ok || !term.IsTerminal(int(f.Fd())) {
			// Not a terminal: a plain login shell reading commands from stdin.
			code, err = execws.Run(c.ctx, cl, req, execws.IO{Stdin: a.Stdin, Stdout: a.Stdout, Stderr: a.Stderr})
		} else {
			req.TTY = true
			code, err = a.interactive(c, cl, req)
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

// interactive runs req with a TTY, the local terminal in raw mode and
// resizes forwarded.
func (a *App) interactive(c *call, cl *remote.Client, req execws.Request) (int, error) {
	req.TTY = true
	req.Cols, req.Rows = 80, 24
	if !term.Supported {
		return -1, errors.New("--tty isn't supported on this platform yet: use `computer exec -- CMD` without --tty")
	}
	f, ok := a.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return execws.Run(c.ctx, cl, req, execws.IO{Stdin: a.Stdin, Stdout: a.Stdout, Stderr: a.Stderr})
	}
	fd := int(f.Fd())
	if cols, rows, err := term.Size(fd); err == nil && cols > 0 && rows > 0 {
		req.Cols, req.Rows = cols, rows
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return -1, fmt.Errorf("can't put the terminal in raw mode: %v", err)
	}
	defer term.Restore(fd, old)

	sig := make(chan os.Signal, 1)
	stop := term.NotifyResize(sig)
	defer stop()
	resize := make(chan [2]int, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sig:
				if cols, rows, err := term.Size(fd); err == nil {
					select {
					case resize <- [2]int{cols, rows}:
					default:
					}
				}
			}
		}
	}()
	return execws.Run(c.ctx, cl, req, execws.IO{Stdin: f, Stdout: a.Stdout, Stderr: a.Stderr, Resize: resize})
}
