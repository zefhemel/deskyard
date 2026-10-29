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
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/transfer"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// remoteHandler serves a remote access route of a computer for a client
// credential with full access to it.
type remoteHandler func(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential)

// remote authenticates a remote access route (exec, forward, CDP, status):
// a client credential only, since a browser has no use for them. The
// connection is registered under the credential before access is checked,
// so a revocation at any point after the check closes it.
func (s *Server) remote(next remoteHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, ok := bearerOf(r)
		if !ok {
			if _, ok := s.withSession(w, r); ok {
				writeError(w, http.StatusUnauthorized, "use a client credential (deskyard login)")
			} else {
				writeError(w, http.StatusUnauthorized, "sign in first")
			}
			return
		}
		id, _, ok := parseBearer(credentialPrefix, bearer)
		if !ok {
			writeError(w, http.StatusUnauthorized, "the credential is unknown, revoked or expired")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer s.credentials.track(id, r.PathValue("name"), cancel)()
		rr, c, cred, ok := s.remoteTarget(w, r.WithContext(ctx), bearer)
		if !ok {
			return
		}
		if ctx.Err() != nil {
			// Revoked while the computer was looked up: answer as the
			// request would now.
			if _, _, _, ok := s.remoteTarget(w, r, bearer); ok {
				writeError(w, http.StatusUnauthorized, "the credential is unknown, revoked or expired")
			}
			return
		}
		next(w, rr, c, cred)
	})
}

// remoteTarget checks the credential and its account's full access to the
// computer, writing the error when either fails.
func (s *Server) remoteTarget(w http.ResponseWriter, r *http.Request, bearer string) (*http.Request, computers.Computer, credential, bool) {
	r, ok := s.withCredential(r, bearer)
	if !ok {
		writeError(w, http.StatusUnauthorized, "the credential is unknown, revoked or expired")
		return r, computers.Computer{}, credential{}, false
	}
	c, _, ok := s.computerFor(w, r, auth.View)
	if !ok {
		return r, c, credential{}, false
	}
	cred, _ := s.credentialOf(r)
	return r, c, cred, true
}

// running writes 409 unless the computer runs.
func running(w http.ResponseWriter, c computers.Computer) bool {
	if !c.Running() {
		writeError(w, http.StatusConflict, c.Name+" is stopped; someone with access can start it")
		return false
	}
	return true
}

func (s *Server) handleRemoteExec(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
	if running(w, c) {
		s.remoteExec(w, r, c, cred)
	}
}

func (s *Server) handleRemoteForward(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
	if running(w, c) {
		s.remoteForward(w, r, c, cred)
	}
}

// handleRemoteCDP serves /cdp/{rest...}, and handleRemoteCDPRoot /cdp
// (Chromium's /json/version).
func (s *Server) handleRemoteCDP(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
	if running(w, c) {
		s.remoteCDP(w, r, c, cred, "/"+r.PathValue("rest"))
	}
}

func (s *Server) handleRemoteCDPRoot(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
	if running(w, c) {
		s.remoteCDP(w, r, c, cred, "")
	}
}

func (s *Server) handleRemoteStatus(w http.ResponseWriter, r *http.Request, c computers.Computer, _ credential) {
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

// execEnv overrides variables of the container's computer that a
// command run for a client must not see.
// DESKYARD_ACTIVITY=off keeps the in-desktop CLI from recording what the
// supervisor already records.
var execEnv = []string{"WORKSPACE_PROXY_TOKEN=", "DESKYARD_ACTIVITY=off"}

// execEnvFor is the computer of a command run for a client: the client's
// variables, then execEnv, so that the client can't override it.
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

func (s *Server) remoteExec(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential) {
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
	s.Log.Info("remote exec", "computer", c.Name, "credential", cred.Name, "account", cred.Username, "argv", activity.DisplayCommand(start.Argv), "tty", start.Tty)
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
	ref, agent := refOf(c), s.agentOf(cred)
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

// remoteCDP proxies the shared Chromium's DevTools protocol through the
// desktop's Caddy (/_cdp/), pointing /json URLs back through this route.
func (s *Server) remoteCDP(w http.ResponseWriter, r *http.Request, c computers.Computer, cred credential, rest string) {
	addr, tok, err := s.Computers.Target(r.Context(), c.Name)
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
	public := r.Host + "/api/computers/" + c.Name + "/cdp/devtools/"
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
				s.Log.Debug("cdp proxy", "computer", c.Name, "err", err)
			}
			writeError(w, http.StatusBadGateway, "the browser didn't answer")
		},
	}
	if strings.HasPrefix(rest, "/devtools/") {
		s.Log.Info("remote browser connect", "computer", c.Name, "credential", cred.Name, "account", cred.Username)
		defer s.activity.BrowserOpen(refOf(c), s.agentOf(cred))()
	} else {
		s.activity.BrowserTouch(refOf(c), s.agentOf(cred))
	}
	rp.ServeHTTP(w, r)
}
