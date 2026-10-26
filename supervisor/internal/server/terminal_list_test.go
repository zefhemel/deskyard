package server

import (
	"context"
	"fmt"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type terminalListEngine struct {
	*terminalEngine
	output string
	code   int
}

func (e *terminalListEngine) Exec(_ context.Context, _ string, s engine.ExecSpec) (*engine.Process, error) {
	e.calls.Add(1)
	if strings.Join(s.Cmd, " ") != "deskyard terminal-list" || s.User != "user" || s.Tty || s.Stdin || s.WorkDir != "/workspace" {
		panic("unsafe list invocation")
	}
	return &engine.Process{Stdout: strings.NewReader(e.output), Stderr: strings.NewReader(""), Wait: func() (int, error) { return e.code, nil }, Close: func() {}}, nil
}
func TestTerminalSessions(t *testing.T) {
	for _, tc := range []struct {
		name, user, state, output string
		supported                 bool
		code, want                int
	}{
		{"list", "admin", "running", "zebra\t0\tbash\nmain\t2\tvim\n", true, 0, 200},
		{"empty", "admin", "running", "", true, 0, 200},
		{"anonymous", "", "running", "", true, 0, 401},
		{"no access", "anna", "running", "", true, 0, 404},
		{"stopped", "admin", "exited", "", true, 0, 409},
		{"old image", "admin", "running", "", false, 0, 409},
		{"failed", "admin", "running", "", true, 1, 502},
		{"invalid", "admin", "running", "bad.name\n", true, 0, 502},
		{"oversize", "admin", "running", strings.Repeat("x", 65537), true, 0, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &terminalListEngine{terminalEngine: newTerminalEngine(), output: tc.output, code: tc.code}
			e.state = tc.state
			e.supported = tc.supported
			_, ts := newTestServerWith(t, e)
			c := ts.Client()
			if tc.user != "" {
				c = login(t, ts, tc.user)
			}
			resp, err := c.Get(ts.URL + "/api/environments/alice/terminal/sessions")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.want {
				t.Fatalf("%d %s", resp.StatusCode, b)
			}
			if tc.want < 500 && tc.want != 200 && e.calls.Load() != 0 {
				t.Fatal("unauthorized exec")
			}
			if tc.name == "list" && strings.TrimSpace(string(b)) != `{"sessions":[{"name":"main","clients":2,"command":"vim"},{"name":"zebra","clients":0,"command":"bash"}]}` {
				t.Fatal(string(b))
			}
			if tc.name == "empty" && strings.TrimSpace(string(b)) != `{"sessions":[]}` {
				t.Fatal(string(b))
			}
		})
	}
}

// Model an engine process whose disposal releases both attached output streams.
type terminalListPipeEngine struct {
	*terminalEngine
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stderrR  *io.PipeReader
	stderrW  *io.PipeWriter
	mode     string
	started  chan struct{}
	finished chan struct{}
}

func (e *terminalListPipeEngine) Exec(context.Context, string, engine.ExecSpec) (*engine.Process, error) {
	close(e.started)
	go func() {
		defer close(e.finished)
		defer e.stdoutW.Close()
		defer e.stderrW.Close()
		switch e.mode {
		case "stderr":
			if _, err := e.stderrW.Write([]byte(strings.Repeat("e", 2*65536))); err != nil {
				return
			}
			e.stdoutW.Write([]byte("main\t0\tbash\n"))
		case "stdout":
			e.stdoutW.Write([]byte(strings.Repeat("x", 2*65536)))
		case "cancellation", "deadline":
			for {
				if _, err := e.stderrW.Write([]byte(strings.Repeat("e", 65536))); err != nil {
					return
				}
			}
		}
	}()
	return &engine.Process{Stdout: e.stdoutR, Stderr: e.stderrR, Wait: func() (int, error) { return 0, nil }, Close: func() { e.stdoutR.Close(); e.stderrR.Close() }}, nil
}
func TestTerminalSessionsPipeCleanup(t *testing.T) {
	for _, mode := range []string{"stderr", "stdout", "cancellation", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			e := &terminalListPipeEngine{terminalEngine: newTerminalEngine(), mode: mode, started: make(chan struct{}), finished: make(chan struct{})}
			e.stdoutR, e.stdoutW = io.Pipe()
			e.stderrR, e.stderrW = io.Pipe()
			_, ts := newTestServerWith(t, e)
			// Registered after the server so failure cleanup releases its handler first.
			t.Cleanup(func() { e.stdoutR.Close(); e.stderrR.Close() })
			client := login(t, ts, "admin")
			client.Timeout = time.Second
			if mode == "deadline" {
				client.Timeout = 7 * time.Second
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/environments/alice/terminal/sessions", nil)
			result := make(chan error, 1)
			go func() {
				resp, err := client.Do(req)
				if err == nil {
					defer resp.Body.Close()
					io.Copy(io.Discard, resp.Body)
					want := 200
					if mode == "stdout" || mode == "deadline" {
						want = 502
					}
					if resp.StatusCode != want {
						err = fmt.Errorf("status %d", resp.StatusCode)
					}
				}
				result <- err
			}()
			<-e.started
			if mode == "cancellation" {
				cancel()
			}
			if err := <-result; err != nil && mode != "cancellation" {
				t.Error(err)
			}
			select {
			case <-e.finished:
			case <-time.After(200 * time.Millisecond):
				t.Error("pipe producer retained after handler completion/cancellation")
			}
		})
	}
}
