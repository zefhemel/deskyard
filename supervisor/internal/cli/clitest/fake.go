// Package clitest is a fake supervisor agent API for the deskyard command's
// tests: it implements the exec WebSocket from supervisor/API.md by running
// commands on the test machine, plus the status and CDP endpoints.
package clitest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Token is the fake's valid token.
const Token = "dyt_test1_secret"

// ExecRequest mirrors the first exec message.
type ExecRequest struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
	TTY  bool              `json:"tty"`
	Cols int               `json:"cols"`
	Rows int               `json:"rows"`
}

// Fake is a running fake supervisor.
type Fake struct {
	*httptest.Server
	Name    string
	Dir     string // stands in for /workspace
	Stopped bool
	// Desktop handles argv[0] == "deskyard" (the command inside a desktop);
	// the default answers size and screenshot.
	Desktop func(args []string, stdout, stderr io.Writer) int

	mu    sync.Mutex
	Execs []ExecRequest
}

// New starts a fake for a desktop called name.
func New(t testing.TB, name string) *Fake {
	f := &Fake{Name: name, Dir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/v1", f.auth(f.info))
	mux.HandleFunc("/api/agent/v1/exec", f.auth(f.exec))
	mux.HandleFunc("/api/agent/v1/cdp/", f.auth(f.cdp))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// URL of the desktop, as given to `remote add`.
func (f *Fake) DesktopURL() string { return f.Server.URL + "/" + f.Name }

// Requests returns the exec requests so far.
func (f *Fake) Requests() []ExecRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExecRequest(nil), f.Execs...)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (f *Fake) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+Token {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if f.Stopped {
			writeErr(w, http.StatusConflict, f.Name+" is stopped; someone with access can start it")
			return
		}
		h(w, r)
	}
}

func (f *Fake) info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"name":%q,"state":"running","image":"ghcr.io/x/cc:latest","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}`, f.Name)
}

// FakePNG is what the fake's `deskyard screenshot` prints.
var FakePNG = []byte("\x89PNG\r\n\x1a\nfake-image")

func (f *Fake) desktop(args []string, stdout, stderr io.Writer) int {
	if f.Desktop != nil {
		return f.Desktop(args, stdout, stderr)
	}
	switch args[0] {
	case "size":
		io.WriteString(stdout, "1280 800\n")
	case "screenshot", "zoom":
		stdout.Write(FakePNG)
	case "fail":
		io.WriteString(stderr, "boom\n")
		return 5
	}
	return 0
}

type wsWriter struct {
	ctx    context.Context
	c      *websocket.Conn
	stream byte
	mu     *sync.Mutex
}

func (w wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.c.Write(w.ctx, websocket.MessageBinary, append([]byte{w.stream}, p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *Fake) exec(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	ctx := r.Context()
	_, data, err := c.Read(ctx)
	if err != nil {
		return
	}
	var req ExecRequest
	if err := json.Unmarshal(data, &req); err != nil || len(req.Argv) == 0 {
		c.Write(ctx, websocket.MessageText, []byte(`{"error":"bad request"}`))
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	f.mu.Lock()
	f.Execs = append(f.Execs, req)
	f.mu.Unlock()

	var wmu sync.Mutex
	stdout := wsWriter{ctx, c, 1, &wmu}
	stderr := wsWriter{ctx, c, 2, &wmu}
	if req.TTY {
		stderr.stream = 1
	}
	stdinR, stdinW := io.Pipe()
	go func() {
		defer stdinW.Close()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary && len(data) > 0 && data[0] == 0 {
				if _, err := stdinW.Write(data[1:]); err != nil {
					return
				}
				continue
			}
			if typ == websocket.MessageText && strings.Contains(string(data), `"eof"`) {
				stdinW.Close()
			}
		}
	}()

	var code int
	if req.Argv[0] == "deskyard" && len(req.Argv) > 1 {
		code = f.desktop(req.Argv[1:], stdout, stderr)
	} else {
		cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
		cmd.Dir = f.Dir
		if req.Cwd != "" {
			cmd.Dir = req.Cwd
		}
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdout, stderr
		cmd.WaitDelay = time.Second
		err := cmd.Run()
		var xe *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &xe):
			code = xe.ExitCode()
		default:
			msg, _ := json.Marshal(map[string]string{"error": err.Error()})
			c.Write(ctx, websocket.MessageText, msg)
			c.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
	stdinR.Close()
	wmu.Lock()
	c.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"exit":%d}`, code)))
	wmu.Unlock()
	c.Close(websocket.StatusNormalClosure, "")
}

// cdp fakes the proxied DevTools endpoint: /json/version and /json/list
// with URLs pointing back through the agent API, and an echo WebSocket at
// /devtools/….
func (f *Fake) cdp(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/agent/v1/cdp")
	host := r.Host
	switch {
	case strings.HasPrefix(rest, "/devtools/"):
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			c.Write(r.Context(), typ, append([]byte(rest+" "), data...))
		}
	case rest == "/json/version" || rest == "/json/version/":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Browser":"Chrome/141.0","Protocol-Version":"1.3","webSocketDebuggerUrl":"ws://%s/api/agent/v1/cdp/devtools/browser/b1"}`, host)
	case rest == "/json/list" || rest == "/json" || rest == "/json/list/":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"id":"p1","type":"page","url":"about:blank","title":"t","devtoolsFrontendUrl":"/api/agent/v1/cdp/devtools/inspector.html?ws=%s/api/agent/v1/cdp/devtools/page/p1","webSocketDebuggerUrl":"ws://%s/api/agent/v1/cdp/devtools/page/p1"}]`, host, host)
	default:
		http.NotFound(w, r)
	}
}
