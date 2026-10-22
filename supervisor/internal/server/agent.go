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
	"time"

	"github.com/coder/websocket"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

const agentPrefix = "/api/agent/v1"

// serveAgent is the agent API: bearer tokens only, one computer per token.
func (s *Server) serveAgent(w http.ResponseWriter, r *http.Request) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeError(w, http.StatusUnauthorized, "an agent token is required (Authorization: Bearer dyt_…)")
		return
	}
	t, ok := s.tokens.check(strings.TrimSpace(bearer))
	if !ok {
		writeError(w, http.StatusUnauthorized, "the token is unknown, revoked or expired")
		return
	}
	c, err := s.computerByID(r.Context(), t.ComputerID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "the token's computer no longer exists")
		return
	}
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
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.tokens.track(t.ID, cancel)()
	r = r.WithContext(ctx)
	switch {
	case rest == "/exec":
		s.agentExec(w, r, c, t)
	case rest == "/cdp" || strings.HasPrefix(rest, "/cdp/"):
		s.agentCDP(w, r, c, t, strings.TrimPrefix(rest, "/cdp"))
	default:
		writeError(w, http.StatusNotFound, "no such agent API route")
	}
}

func (s *Server) computerByID(ctx context.Context, id string) (computers.Computer, error) {
	list, err := s.Computers.List(ctx)
	if err != nil {
		return computers.Computer{}, err
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return computers.Computer{}, computers.ErrNotFound
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request, c computers.Computer) {
	res := map[string]any{
		"name": c.Name, "state": c.State, "image": c.Image, "api": 1,
		"imageTitle": c.ImageLabels["org.opencontainers.image.title"], "imageVersion": c.ImageLabels["org.opencontainers.image.version"],
		"screen": nil,
	}
	if c.Running() {
		ctx, cancel := timeout(r, 10*time.Second)
		defer cancel()
		if out, err := s.execOutput(ctx, c, "computer", "size"); err == nil {
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
var execEnv = []string{"WORKSPACE_PROXY_TOKEN="}

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

func (s *Server) agentExec(w http.ResponseWriter, r *http.Request, c computers.Computer, t token) {
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
	env := append([]string{}, execEnv...)
	for k, v := range start.Env {
		env = append(env, k+"="+v)
	}
	s.Log.Info("agent exec", "computer", c.Name, "token", t.Name, "argv", start.Argv, "tty", start.Tty)
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{
		Cmd: start.Argv, User: "user", WorkDir: start.Cwd, Env: env,
		Tty: start.Tty, Stdin: true, Cols: start.Cols, Rows: start.Rows,
	})
	if err != nil {
		fail(err.Error())
		return
	}
	defer p.Close()

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
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if len(b) > 0 && b[0] == 0 {
					if _, err := p.Stdin.Write(b[1:]); err != nil {
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
	b, _ := json.Marshal(map[string]int{"exit": code})
	wmu.Lock()
	_ = ws.Write(ctx, websocket.MessageText, b)
	wmu.Unlock()
	_ = ws.Close(websocket.StatusNormalClosure, "")
}

// agentCDP proxies the shared Chromium's DevTools protocol through the
// computer's Caddy (/_cdp/), pointing /json URLs back through this route.
func (s *Server) agentCDP(w http.ResponseWriter, r *http.Request, c computers.Computer, t token, rest string) {
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
				s.Log.Debug("cdp proxy", "computer", c.Name, "err", err)
			}
			writeError(w, http.StatusBadGateway, "the browser didn't answer")
		},
	}
	if strings.HasPrefix(rest, "/devtools/") {
		s.Log.Info("agent browser connect", "computer", c.Name, "token", t.Name)
	}
	rp.ServeHTTP(w, r)
}
