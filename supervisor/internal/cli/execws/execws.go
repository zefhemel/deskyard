// Package execws is the client side of the agent API's exec WebSocket
// (GET /api/agent/v1/exec, see supervisor/API.md).
package execws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// Stream bytes at the start of binary messages.
const (
	StreamStdin  = 0
	StreamStdout = 1
	StreamStderr = 2
)

// Request is the first message: what to run.
type Request struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	TTY  bool              `json:"tty,omitempty"`
	Cols int               `json:"cols,omitempty"`
	Rows int               `json:"rows,omitempty"`
}

// IO wires a remote command to local streams. A nil Stdin closes the remote
// stdin right away; nil Stdout/Stderr discard. Resize, when set, delivers
// terminal size changes ([cols, rows]).
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Resize <-chan [2]int
}

const chunk = 32 * 1024

// Run runs one command over the exec WebSocket and returns its exit code.
// Errors (network, auth, a stopped desktop, an error message from the
// server) come back as err with the exit code -1.
func Run(ctx context.Context, c *remote.Client, req Request, sio IO) (int, error) {
	if len(req.Argv) == 0 {
		return -1, errors.New("no command given")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, c.WSURL("/exec"), &websocket.DialOptions{
		HTTPHeader: c.Header(),
		HTTPClient: c.HTTP,
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return -1, c.ResponseError(resp)
		}
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, c.NetError(err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)

	first, err := json.Marshal(req)
	if err != nil {
		return -1, err
	}
	if err := conn.Write(ctx, websocket.MessageText, first); err != nil {
		return -1, c.NetError(err)
	}

	// Writers share the connection; coder/websocket allows concurrent
	// writes, but keep message order per sender with one mutex anyway.
	var wmu sync.Mutex
	send := func(typ websocket.MessageType, p []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.Write(ctx, typ, p)
	}

	if sio.Stdin == nil {
		if err := send(websocket.MessageText, []byte(`{"eof":true}`)); err != nil {
			return -1, c.NetError(err)
		}
	} else {
		go pumpStdin(ctx, sio.Stdin, send)
	}
	if sio.Resize != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case sz, ok := <-sio.Resize:
					if !ok {
						return
					}
					msg, _ := json.Marshal(map[string][2]int{"resize": sz})
					if send(websocket.MessageText, msg) != nil {
						return
					}
				}
			}
		}()
	}

	stdout, stderr := sio.Stdout, sio.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			return -1, fmt.Errorf("the connection to %s closed before the command finished: %v", c.Label(), err)
		}
		switch typ {
		case websocket.MessageBinary:
			if len(data) == 0 {
				continue
			}
			var w io.Writer
			switch data[0] {
			case StreamStdout:
				w = stdout
			case StreamStderr:
				w = stderr
			default:
				continue
			}
			if _, err := w.Write(data[1:]); err != nil {
				return -1, fmt.Errorf("writing output: %v", err)
			}
		case websocket.MessageText:
			var m struct {
				Exit  *int    `json:"exit"`
				Error *string `json:"error"`
			}
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if m.Error != nil {
				conn.Close(websocket.StatusNormalClosure, "")
				return -1, fmt.Errorf("%s: %s", c.Label(), *m.Error)
			}
			if m.Exit != nil {
				conn.Close(websocket.StatusNormalClosure, "")
				return *m.Exit, nil
			}
		}
	}
}

func pumpStdin(ctx context.Context, r io.Reader, send func(websocket.MessageType, []byte) error) {
	buf := make([]byte, chunk+1)
	for {
		n, err := r.Read(buf[1:])
		if n > 0 {
			buf[0] = StreamStdin
			if send(websocket.MessageBinary, append([]byte(nil), buf[:n+1]...)) != nil {
				return
			}
		}
		if err != nil {
			// EOF or a read error: either way the remote stdin is done.
			_ = send(websocket.MessageText, []byte(`{"eof":true}`))
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}
