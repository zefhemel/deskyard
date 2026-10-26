package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/transfer"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

const agentPrefix = "/api/agent/v1"

// serveAgent is the agent API: bearer tokens only, one environment per token.
func (s *Server) serveAgent(w http.ResponseWriter, r *http.Request) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeError(w, http.StatusUnauthorized, "an access token is required (Authorization: Bearer dyt_…)")
		return
	}
	t, ok := s.tokens.check(strings.TrimSpace(bearer))
	if !ok || !s.tokenCreatorAllowed(t) {
		writeError(w, http.StatusUnauthorized, "the token is unknown, revoked or expired")
		return
	}
	c, err := s.Environments.Get(r.Context(), t.Environment)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "the token's environment no longer exists")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.tokens.track(t.ID, cancel)()
	if _, valid := s.tokens.check(strings.TrimSpace(bearer)); !valid || !s.tokenCreatorAllowed(t) || ctx.Err() != nil {
		writeError(w, http.StatusUnauthorized, "the token is unknown, revoked or expired")
		return
	}
	r = r.WithContext(ctx)
	rest, ok := strings.CutPrefix(r.URL.Path, agentPrefix)
	if !ok {
		writeError(w, http.StatusNotFound, "no such agent API route")
		return
	}
	if rest == "" || rest == "/" {
		s.agentStatus(w, r, c)
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, c.Name+" is stopped; someone with access can start it")
		return
	}
	switch {
	case rest == "/exec":
		s.agentExec(w, r, c, t)
	case rest == "/forward":
		s.agentForward(w, r, c, t)
	case rest == "/cdp" || strings.HasPrefix(rest, "/cdp/"):
		s.agentCDP(w, r, c, t, strings.TrimPrefix(rest, "/cdp"))
	default:
		writeError(w, http.StatusNotFound, "no such agent API route")
	}
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request, c environments.Environment) {
	res := map[string]any{
		"name": c.Name, "state": c.State, "image": c.Image, "api": 1,
		"imageTitle": c.ImageLabels["org.opencontainers.image.title"], "imageVersion": c.ImageLabels["org.opencontainers.image.version"],
		"screen": nil,
	}
	if c.Running() {
		ctx, cancel := timeout(r, 10*time.Second)
		defer cancel()
		if out, err := s.execOutput(ctx, c, "deskyard", "size"); err == nil {
			if f := strings.Fields(string(out)); len(f) == 2 {
				wd, _ := strconv.Atoi(f[0])
				ht, _ := strconv.Atoi(f[1])
				res["screen"] = map[string]int{"width": wd, "height": ht}
			}
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// execEnv overrides variables of the container's environment that a
// command run for an agent must not see.
// DESKYARD_ACTIVITY=off keeps the in-desktop CLI from recording what the
// supervisor already records.
var execEnv = []string{"WORKSPACE_PROXY_TOKEN=", "DESKYARD_ACTIVITY=off"}

// execEnvFor is the environment of a command run for an agent: the agent's
// variables, then execEnv, so that the agent can't override it.
func execEnvFor(agentEnv map[string]string) []string {
	forced := map[string]bool{}
	for _, kv := range execEnv {
		k, _, _ := strings.Cut(kv, "=")
		forced[k] = true
	}
	env := make([]string, 0, len(agentEnv)+len(execEnv))
	for k, v := range agentEnv {
		if !forced[k] {
			env = append(env, k+"="+v)
		}
	}
	return append(env, execEnv...)
}

type execStart struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
	Tty  bool              `json:"tty"`
	Cols uint              `json:"cols"`
	Rows uint              `json:"rows"`
}

type execControl struct {
	Resize []uint `json:"resize"`
	EOF    bool   `json:"eof"`
}

func (s *Server) agentExec(w http.ResponseWriter, r *http.Request, c environments.Environment, t token) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	ctx := r.Context()
	defer ws.CloseNow()
	fail := func(msg string) {
		b, _ := json.Marshal(map[string]string{"error": msg})
		_ = ws.Write(ctx, websocket.MessageText, b)
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}
	typ, first, err := ws.Read(ctx)
	if err != nil {
		return
	}
	var start execStart
	if typ != websocket.MessageText || json.Unmarshal(first, &start) != nil || len(start.Argv) == 0 {
		fail(`the first message must be {"argv":[…]}`)
		return
	}
	if start.Cwd == "" {
		start.Cwd = "/workspace"
	}
	s.Log.Info("agent exec", "environment", c.Name, "token", t.Name, "argv", activity.DisplayCommand(start.Argv), "tty", start.Tty)
	var p *engine.Process
	if session, shared := terminalSession(start.Argv, start.Tty); shared {
		// `deskyard terminal NAME` is an explicit open: it creates the session.
		p, err = s.terminalProcess(ctx, c.Container.ID, session, false, start.Cols, start.Rows)
	} else {
		p, err = s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{
			Cmd: start.Argv, User: "user", WorkDir: start.Cwd, Env: execEnvFor(start.Env),
			Tty: start.Tty, Stdin: true, Cols: start.Cols, Rows: start.Rows,
		})
	}
	if err != nil {
		fail(err.Error())
		return
	}
	defer p.Close()

	// The exec started: record it. Nothing here may block or fail the command.
	ref, agent := refOf(c), s.agentOf(t)
	var in, out atomic.Int64 // bytes relayed, for a copy
	copyDir, copyPath, isCopy := transfer.Classify(start.Argv)
	// ended records the end of a plain command, the first time it's called:
	// with its exit code, or nil when the WebSocket or context ended first.
	ended := func(*int) {}
	if act, ok := activity.ParseScreenArgv(start.Argv); ok {
		s.activity.Screen(ref, agent, act)
	} else if !isCopy {
		id := randomID(8)
		s.activity.CmdStart(ref, agent, id, activity.DisplayCommand(start.Argv))
		var once sync.Once
		ended = func(exit *int) { once.Do(func() { s.activity.CmdEnd(ref, agent, id, exit) }) }
	}
	defer ended(nil)

	var wmu sync.Mutex
	write := func(stream byte, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return ws.Write(ctx, websocket.MessageBinary, append([]byte{stream}, b...))
	}
	var outputs sync.WaitGroup
	pump := func(stream byte, rd io.Reader) {
		defer outputs.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := rd.Read(buf)
			if n > 0 && stream == 1 {
				out.Add(int64(n))
			}
			if n > 0 && write(stream, buf[:n]) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}
	outputs.Add(1)
	go pump(1, p.Stdout)
	if p.Stderr != nil {
		outputs.Add(1)
		go pump(2, p.Stderr)
	}
	// Client → command.
	go func() {
		for {
			typ, b, err := ws.Read(ctx)
			if err != nil {
				_ = p.Stdin.Close()
				ended(nil) // the client is gone, whether or not the command is
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if len(b) > 0 && b[0] == 0 {
					n, err := p.Stdin.Write(b[1:])
					in.Add(int64(n))
					if err != nil {
						return
					}
				}
			case websocket.MessageText:
				var ctl execControl
				if json.Unmarshal(b, &ctl) != nil {
					continue
				}
				if ctl.EOF {
					_ = p.Stdin.Close()
				}
				if len(ctl.Resize) == 2 && p.Resize != nil {
					_ = p.Resize(ctl.Resize[0], ctl.Resize[1])
				}
			}
		}
	}()
	outputs.Wait()
	code, err := p.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		fail(err.Error())
		return
	}
	if err == nil {
		ended(&code)
		if isCopy && code == 0 {
			n := in.Load()
			if copyDir == "out" {
				n = max(out.Load()-1, 0) // the download script's type byte
			}
			s.activity.Copy(ref, agent, copyDir, copyPath, n)
		}
	}
	b, _ := json.Marshal(map[string]int{"exit": code})
	wmu.Lock()
	_ = ws.Write(ctx, websocket.MessageText, b)
	wmu.Unlock()
	_ = ws.Close(websocket.StatusNormalClosure, "")
}

// agentCDP proxies the shared Chromium's DevTools protocol through the
// desktop's Caddy (/_cdp/), pointing /json URLs back through this route.
func (s *Server) agentCDP(w http.ResponseWriter, r *http.Request, c environments.Environment, t token, rest string) {
	addr, tok, err := s.Environments.Target(r.Context(), c.Name)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if rest == "" {
		rest = "/json/version"
	}
	wsScheme := "ws"
	if r.TLS != nil {
		wsScheme = "wss"
	}
	public := r.Host + agentPrefix + "/cdp/devtools/"
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = addr
			pr.Out.URL.Path = "/_cdp" + rest
			pr.Out.Host = addr
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin") // Chromium refuses DevTools WebSockets with an Origin
			pr.Out.Header.Set("X-Deskyard-Token", tok)
		},
		ModifyResponse: func(resp *http.Response) error {
			if !strings.HasPrefix(rest, "/json") {
				return nil
			}
			b, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}
			b = bytes.ReplaceAll(b, []byte("ws://localhost/devtools/"), []byte(wsScheme+"://"+public))
			b = bytes.ReplaceAll(b, []byte("ws=localhost/devtools/"), []byte("ws="+public))
			resp.Body = io.NopCloser(bytes.NewReader(b))
			resp.ContentLength = int64(len(b))
			resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				s.Log.Debug("cdp proxy", "environment", c.Name, "err", err)
			}
			writeError(w, http.StatusBadGateway, "the browser didn't answer")
		},
	}
	if strings.HasPrefix(rest, "/devtools/") {
		s.Log.Info("agent browser connect", "environment", c.Name, "token", t.Name)
		defer s.activity.BrowserOpen(refOf(c), s.agentOf(t))()
	} else {
		s.activity.BrowserTouch(refOf(c), s.agentOf(t))
	}
	rp.ServeHTTP(w, r)
}
