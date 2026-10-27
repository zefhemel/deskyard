// Package clitest is a fake supervisor for the deskyard command's tests: it
// implements the remote access API from supervisor/API.md (status, exec by
// running commands on the test machine, CDP and forward, whose helper side
// runs in the fake), the CLI login and link redemption routes, and deleting
// one's own client credential. Every other /api/ route (the administrator
// CLI's) is recorded and answered from Answers.
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

	"github.com/zefhemel/deskyard/supervisor/internal/forward"
)

// Credential is a client credential the fake accepts from the start.
const Credential = "dyc_0a1b2c3d_secret"

// Link is the fake's single-use login link code.
const Link = "dyl_9f8e7d6c_linksecret"

// Username is the account every credential of the fake belongs to.
const Username = "zef"

// Grant is one of the account's environments, as login answers list them.
type Grant struct {
	Name  string `json:"name"`
	Level string `json:"level"`
}

// ExecRequest mirrors the first exec message.
type ExecRequest struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
	TTY  bool              `json:"tty"`
	Cols int               `json:"cols"`
	Rows int               `json:"rows"`
	// Environment is the environment the request was for (from the path).
	Environment string `json:"-"`
}

// Fake is a running fake supervisor.
type Fake struct {
	*httptest.Server
	Name    string // the first environment
	Dir     string // stands in for /workspace
	Stopped bool
	// Desktop handles argv[0] == "deskyard" (the command inside an environment);
	// the default answers size and screenshot.
	Desktop func(args []string, stdout, stderr io.Writer) int
	// Grants are the account's environments (default: Name at full). The
	// remote access routes serve the full ones, answer 403 for observed
	// ones and 404 for the rest.
	Grants []Grant
	// LoginPending is how many polls of a CLI login answer pending before
	// it is approved; LoginDeny denies it instead.
	LoginPending int
	LoginDeny    bool
	// LoginSaveFails is how many polls after the approval answer 500, as
	// when the server can't save the credential yet.
	LoginSaveFails int
	// Answers are the answers to the other /api/ routes, keyed by "METHOD
	// /api/path" (no query); a route without one answers 404 "no such
	// route". An answer for GET /api/environments/{name} replaces the
	// built-in one.
	Answers map[string]Answer

	mu    sync.Mutex
	Execs []ExecRequest
	// ForwardStatus are answers for the next /forward requests, in order
	// (e.g. 409, 401), before the fake accepts again.
	ForwardStatus []int
	forwardConns  map[*websocket.Conn]bool
	forwardStarts []string
	credentials   map[string]bool // valid credentials
	issued        int
	loginDone     bool
	linkUsed      bool
	clients       []string // CLI login client names and link redemption names
	revoked       []string // credential IDs deleted through DELETE /api/me/credentials/{id}
	calls         []Call
}

// Answer is a canned answer to an API route.
type Answer struct {
	Status int // default 200, or 204 without JSON and Events
	JSON   any // the body, encoded as JSON
	// Events, when set, make the answer a server-sent event stream with
	// one data: line per event, each encoded as JSON (a job's progress).
	Events []any
}

// Call is a request to a route answered from Answers.
type Call struct {
	Method, Path, Query string
	Body                string // the raw body; "" without one
}

// New starts a fake whose account has the full environment name.
func New(t testing.TB, name string) *Fake {
	f := &Fake{Name: name, Dir: t.TempDir(), Grants: []Grant{{name, "full"}}, credentials: map[string]bool{Credential: true}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/environments/{name}", f.auth(f.environment))
	mux.HandleFunc("GET /api/environments/{name}/status", f.auth(f.remote(f.info)))
	mux.HandleFunc("/api/environments/{name}/exec", f.auth(f.remote(f.exec)))
	mux.HandleFunc("/api/environments/{name}/cdp/", f.auth(f.remote(f.cdp)))
	mux.HandleFunc("/api/environments/{name}/forward", f.auth(f.remote(f.forward)))
	mux.HandleFunc("DELETE /api/me/credentials/{id}", f.auth(f.deleteCredential))
	mux.HandleFunc("POST /api/cli-login", f.startLogin)
	mux.HandleFunc("POST /api/cli-login/{id}/token", f.pollLogin)
	mux.HandleFunc("POST /api/links/redeem", f.redeem)
	mux.HandleFunc("/api/", f.auth(f.answer))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// EnvironmentURL is the environment's URL, as given to `deskyard login`.
func (f *Fake) EnvironmentURL() string { return f.Server.URL + "/" + f.Name }

// LinkURL is the login link as the web UI shows it.
func (f *Fake) LinkURL() string { return f.Server.URL + "/link#" + Link }

// Requests returns the exec requests so far.
func (f *Fake) Requests() []ExecRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExecRequest(nil), f.Execs...)
}

// Clients returns the client names CLI logins and link redemptions gave.
func (f *Fake) Clients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.clients...)
}

// Revoked returns the IDs of the credentials deleted so far.
func (f *Fake) Revoked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

// Calls returns the requests answered from Answers so far.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Answer sets the answer to the route key ("METHOD /api/path").
func (f *Fake) Answer(key string, a Answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Answers == nil {
		f.Answers = map[string]Answer{}
	}
	f.Answers[key] = a
}

// Valid reports whether the fake accepts cred.
func (f *Fake) Valid(cred string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.credentials[cred]
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// credentialID is the id part of dyc_<id>_<secret>.
func credentialID(cred string) string {
	rest, ok := strings.CutPrefix(cred, "dyc_")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, "_")
	return id
}

func (f *Fake) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cred, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, http.StatusUnauthorized, "sign in first")
			return
		}
		if !f.Valid(cred) {
			writeErr(w, http.StatusUnauthorized, "the credential is unknown, revoked or expired")
			return
		}
		h(w, r)
	}
}

func (f *Fake) level(name string) string {
	for _, g := range f.Grants {
		if g.Name == name {
			return g.Level
		}
	}
	return ""
}

// remote checks the remote access API's access rules for {name}.
func (f *Fake) remote(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		switch f.level(name) {
		case "":
			writeErr(w, http.StatusNotFound, "no such environment")
			return
		case "observe":
			writeErr(w, http.StatusForbidden, "observe-only access can't do that")
			return
		}
		if f.Stopped && !strings.HasSuffix(r.URL.Path, "/status") {
			writeErr(w, http.StatusConflict, name+" is stopped; someone with access can start it")
			return
		}
		h(w, r)
	}
}

func (f *Fake) environment(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	_, canned := f.Answers[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if canned {
		f.answer(w, r)
		return
	}
	name := r.PathValue("name")
	if f.level(name) == "" {
		writeErr(w, http.StatusNotFound, "no such environment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "state": "running", "level": f.level(name)})
}

// answer records the request and serves its canned answer.
func (f *Fake) answer(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body)})
	a, ok := f.Answers[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	switch {
	case !ok:
		writeErr(w, http.StatusNotFound, "no such route")
	case a.Events != nil:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, e := range a.Events {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	case a.JSON == nil:
		if a.Status == 0 {
			a.Status = http.StatusNoContent
		}
		w.WriteHeader(a.Status)
	default:
		if a.Status == 0 {
			a.Status = http.StatusOK
		}
		writeJSON(w, a.Status, a.JSON)
	}
}

func (f *Fake) info(w http.ResponseWriter, r *http.Request) {
	state := "running"
	if f.Stopped {
		state = "stopped"
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"name":%q,"state":%q,"image":"ghcr.io/x/cc:latest","imageTitle":"Debian XFCE","imageVersion":"2026.10.22","screen":{"width":1280,"height":800},"api":1}`, r.PathValue("name"), state)
}

func (f *Fake) deleteCredential(w http.ResponseWriter, r *http.Request) {
	cred := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	id := r.PathValue("id")
	if id != credentialID(cred) {
		writeErr(w, http.StatusNotFound, "no such credential")
		return
	}
	f.mu.Lock()
	delete(f.credentials, cred)
	f.revoked = append(f.revoked, id)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// issueLocked makes a new credential and the answer login and redemption give.
func (f *Fake) issueLocked() map[string]any {
	f.issued++
	cred := fmt.Sprintf("dyc_%08x_issued", f.issued)
	f.credentials[cred] = true
	return map[string]any{"credential": cred, "username": Username, "environments": f.Grants}
}

func (f *Fake) hasFull() bool {
	for _, g := range f.Grants {
		if g.Level == "full" {
			return true
		}
	}
	return false
}

const pollSecret = "poll-secret"

func (f *Fake) startLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Client string `json:"client"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Client == "" || len(body.Client) > 100 {
		writeErr(w, http.StatusBadRequest, "a client name is required")
		return
	}
	f.mu.Lock()
	f.clients = append(f.clients, body.Client)
	f.loginDone = false
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": "req1", "code": "BCDF-GHJK", "approveURL": f.URL + "/cli-login#req1",
		"pollSecret": pollSecret, "interval": 2, "expiresIn": 600})
}

func (f *Fake) pollLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PollSecret string `json:"pollSecret"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.PathValue("id") != "req1" || body.PollSecret != pollSecret || f.loginDone:
		writeErr(w, http.StatusGone, "this login request expired or was already used; run deskyard login again")
	case f.LoginPending > 0:
		f.LoginPending--
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
	case f.LoginDeny:
		f.loginDone = true
		writeErr(w, http.StatusForbidden, "denied")
	case f.LoginSaveFails > 0:
		f.LoginSaveFails--
		writeErr(w, http.StatusInternalServerError, "the sign-in couldn't be saved yet; the next poll tries again")
	default:
		f.loginDone = true
		writeJSON(w, http.StatusOK, f.issueLocked())
	}
}

func (f *Fake) redeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Link   string `json:"link"`
		Client string `json:"client"`
		Name   string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || (body.Client != "cli" && body.Client != "browser") || len(body.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case body.Link != Link || f.linkUsed:
		writeErr(w, http.StatusGone, "this link was already used or replaced")
	case body.Client != "cli":
		writeErr(w, http.StatusBadRequest, "the fake only redeems for the CLI")
	case !f.hasFull():
		writeErr(w, http.StatusForbidden, "observe-only access is for the browser")
	default:
		f.linkUsed = true
		f.clients = append(f.clients, body.Name)
		writeJSON(w, http.StatusOK, f.issueLocked())
	}
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
	req.Environment = r.PathValue("name")
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
// with URLs pointing back through the remote access API, and an echo
// WebSocket at /devtools/….
func (f *Fake) cdp(w http.ResponseWriter, r *http.Request) {
	api := "/api/environments/" + r.PathValue("name") + "/cdp"
	rest := strings.TrimPrefix(r.URL.Path, api)
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
		fmt.Fprintf(w, `{"Browser":"Chrome/141.0","Protocol-Version":"1.3","webSocketDebuggerUrl":"ws://%s%s/devtools/browser/b1"}`, host, api)
	case rest == "/json/list" || rest == "/json" || rest == "/json/list/":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"id":"p1","type":"page","url":"about:blank","title":"t","devtoolsFrontendUrl":"%s/devtools/inspector.html?ws=%s%s/devtools/page/p1","webSocketDebuggerUrl":"ws://%s%s/devtools/page/p1"}]`, api, host, api, host, api)
	default:
		http.NotFound(w, r)
	}
}

// ForwardStarts returns the first messages of the forward connections the
// fake accepted, in order.
func (f *Fake) ForwardStarts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.forwardStarts...)
}

// DropForwards closes the open forward connections, as a desktop restart
// would.
func (f *Fake) DropForwards() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.forwardConns {
		c.CloseNow()
	}
}

// AnswerForwards queues statuses for the next /forward requests.
func (f *Fake) AnswerForwards(status ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ForwardStatus = append(f.ForwardStatus, status...)
}

// forward plays supervisor and forward-helper: after the first message it
// serves the desktop side of the forward protocol on this machine.
func (f *Fake) forward(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if len(f.ForwardStatus) > 0 {
		st := f.ForwardStatus[0]
		f.ForwardStatus = f.ForwardStatus[1:]
		f.mu.Unlock()
		msg := map[int]string{http.StatusConflict: r.PathValue("name") + " is stopped; someone with access can start it", http.StatusUnauthorized: "the credential is unknown, revoked or expired"}[st]
		writeErr(w, st, msg)
		return
	}
	f.mu.Unlock()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	typ, data, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		return
	}
	f.mu.Lock()
	if f.forwardConns == nil {
		f.forwardConns = map[*websocket.Conn]bool{}
	}
	f.forwardConns[c] = true
	f.forwardStarts = append(f.forwardStarts, string(data))
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.forwardConns, c)
		f.mu.Unlock()
	}()
	forward.NewMux(forward.NewWSMessages(ctx, c), true, forward.Desktop).Serve(ctx)
}
