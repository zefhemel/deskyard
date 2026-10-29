package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
)

// deskyard login is a device-authorisation flow (after RFC 8628): the CLI
// starts a request, a signed-in browser approves or denies it, and the CLI's
// poll receives a credential for the approving account, exactly once.
// Requests live in memory only; a restart drops them.

const (
	cliLoginLifetime = 10 * time.Minute
	cliLoginInterval = 2 * time.Second
	// cliLoginAlphabet has no vowels, so a code never spells a word, and
	// nothing that reads like a digit.
	cliLoginAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	maxCLILogins     = 1000 // pending requests at once, bounding the memory strangers can use
)

var (
	errCLILoginPending = errors.New("pending")
	errCLILoginDenied  = errors.New("denied")
	errCLILoginExpired = errors.New("this login request expired or was already used; run deskyard login again")
	errNoCLILogin      = errors.New("no such login request")
)

type cliLogin struct {
	client, ip, code string
	pollHash         string
	created          time.Time
	decided          bool
	account          *accounts.Account // once decided: who approved, nil when denied
}

// cliLoginView is what the approval page shows about a request.
type cliLoginView struct {
	Client  string    `json:"client"`
	IP      string    `json:"ip"`
	Code    string    `json:"code"`
	Created time.Time `json:"created"`
}

type cliLogins struct {
	mu  sync.Mutex
	now func() time.Time
	all map[string]*cliLogin
}

func newCLILogins() *cliLogins {
	return &cliLogins{now: time.Now, all: map[string]*cliLogin{}}
}

// setNow replaces the clock, for tests.
func (cl *cliLogins) setNow(now func() time.Time) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.now = now
}

// prune forgets expired requests. Called with cl.mu held.
func (cl *cliLogins) prune() {
	now := cl.now()
	for id, l := range cl.all {
		if now.Sub(l.created) >= cliLoginLifetime {
			delete(cl.all, id)
		}
	}
}

// get returns the live request id. Called with cl.mu held.
func (cl *cliLogins) get(id string) (*cliLogin, bool) {
	cl.prune()
	l, ok := cl.all[id]
	return l, ok
}

// start makes a request for the client and returns its id, the code the
// browser shows to compare with the CLI's, and the secret its poll needs.
// It returns no id when too many requests are pending.
func (cl *cliLogins) start(client, ip string) (id, code, pollSecret string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	pollSecret = base64.RawURLEncoding.EncodeToString(b)
	code = cliLoginCode()
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.prune()
	if len(cl.all) >= maxCLILogins {
		return "", "", ""
	}
	for id == "" || cl.all[id] != nil {
		id = randomID(16)
	}
	cl.all[id] = &cliLogin{client: client, ip: ip, code: code, pollHash: hashSecret(pollSecret), created: cl.now().UTC()}
	return id, code, pollSecret
}

// cliLoginCode is 8 random letters of cliLoginAlphabet as XXXX-XXXX.
func cliLoginCode() string {
	out := make([]byte, 0, 9)
	b := make([]byte, 1)
	for len(out) < 9 {
		if len(out) == 4 {
			out = append(out, '-')
			continue
		}
		_, _ = rand.Read(b)
		// Uniform: 240 is the largest multiple of the alphabet's length.
		if int(b[0]) < 256-256%len(cliLoginAlphabet) {
			out = append(out, cliLoginAlphabet[int(b[0])%len(cliLoginAlphabet)])
		}
	}
	return string(out)
}

// view returns the request while it awaits a decision.
func (cl *cliLogins) view(id string) (cliLoginView, bool) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	l, ok := cl.get(id)
	if !ok || l.decided {
		return cliLoginView{}, false
	}
	return cliLoginView{Client: l.client, IP: l.ip, Code: l.code, Created: l.created}, true
}

// decide approves the request for a, or denies it when a is nil. A request
// is decided once.
func (cl *cliLogins) decide(id string, a *accounts.Account) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	l, ok := cl.get(id)
	if !ok || l.decided {
		return errNoCLILogin
	}
	l.decided = true
	if a != nil {
		approved := *a
		l.account = &approved
	}
	return nil
}

// take answers a poll: errCLILoginPending until decided, errCLILoginDenied
// once after a denial, and once approved, issue's answer for the approving
// account and the client's name. The request is gone after its answer is
// taken; when issue fails (other than with errCLILoginExpired) it stays
// approved, for the next poll to try again.
// An unknown or expired request, or a wrong secret, is errCLILoginExpired;
// a wrong secret leaves the request for the right one.
func (cl *cliLogins) take(id, pollSecret string, issue func(a accounts.Account, client string) error) error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	l, ok := cl.get(id)
	if !ok || subtle.ConstantTimeCompare([]byte(l.pollHash), []byte(hashSecret(pollSecret))) != 1 {
		return errCLILoginExpired
	}
	if !l.decided {
		return errCLILoginPending
	}
	if l.account == nil {
		delete(cl.all, id)
		return errCLILoginDenied
	}
	err := issue(*l.account, l.client)
	if err == nil || errors.Is(err, errCLILoginExpired) {
		delete(cl.all, id)
	}
	return err
}

// --- routes ------------------------------------------------------------------------------

// handleStartCLILogin starts a deskyard login. It needs no session; starts
// are throttled per IP like failures, as each holds memory until it expires.
func (s *Server) handleStartCLILogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Client string }
	if err := readJSON(r, &req); err != nil || req.Client == "" || utf8.RuneCountInString(req.Client) > 100 {
		writeError(w, http.StatusBadRequest, "a client name of 1 to 100 characters is required")
		return
	}
	key := "cli-login:" + clientIP(r)
	if !s.Throttle.Allow(key) {
		writeError(w, http.StatusTooManyRequests, "too many attempts: wait a few seconds")
		return
	}
	s.Throttle.Fail(key)
	id, code, pollSecret := s.cliLogins.start(req.Client, clientIP(r))
	if id == "" {
		writeError(w, http.StatusTooManyRequests, "too many logins are pending: try again in a few minutes")
		return
	}
	s.Log.Info("CLI login started", "client", req.Client, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "code": code, "approveURL": s.baseURL(r) + "/cli-login#" + id,
		"pollSecret": pollSecret, "interval": int(cliLoginInterval / time.Second), "expiresIn": int(cliLoginLifetime / time.Second)})
}

// handleViewCLILogin shows the approval page what it asks about.
func (s *Server) handleViewCLILogin(w http.ResponseWriter, r *http.Request) {
	v, ok := s.cliLogins.view(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errNoCLILogin.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleApproveCLILogin(w http.ResponseWriter, r *http.Request) {
	s.decideCLILogin(w, r, true)
}

func (s *Server) handleDenyCLILogin(w http.ResponseWriter, r *http.Request) {
	s.decideCLILogin(w, r, false)
}

// decideCLILogin approves or denies a request in the signed-in browser. A
// client credential can't: approval is what a person does in the browser.
func (s *Server) decideCLILogin(w http.ResponseWriter, r *http.Request, approve bool) {
	if _, ok := s.credentialOf(r); ok {
		writeError(w, http.StatusForbidden, "approve in the browser")
		return
	}
	a, _ := s.account(r)
	var approver *accounts.Account
	if approve {
		if !s.canUseCLI(a) {
			writeError(w, http.StatusForbidden, errObserveOnly.Error())
			return
		}
		approver = &a
	}
	id := r.PathValue("id")
	if err := s.cliLogins.decide(id, approver); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.Log.Info("CLI login decided", "account", a.Username, "approved", approve, "ip", clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// handleCLILoginToken answers the CLI's poll. Once approved, the credential
// is made now, for the approving account as it still is.
func (s *Server) handleCLILoginToken(w http.ResponseWriter, r *http.Request) {
	var req struct{ PollSecret string }
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	key := "cli-login-poll:" + clientIP(r)
	if !s.Throttle.Allow(key) {
		writeError(w, http.StatusTooManyRequests, "too many attempts: wait a few seconds")
		return
	}
	id := r.PathValue("id")
	var a accounts.Account
	var client, bearer string
	err := s.cliLogins.take(id, req.PollSecret, func(approver accounts.Account, name string) error {
		var ok bool
		if a, ok = s.accountNow(approver.Username, approver.ID, time.Now()); !ok {
			return errCLILoginExpired
		}
		client = name
		var err error
		_, bearer, err = s.credentials.create(a, "cli", client)
		return err
	})
	switch {
	case errors.Is(err, errCLILoginPending):
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	case errors.Is(err, errCLILoginDenied):
		writeError(w, http.StatusForbidden, "denied")
		return
	case errors.Is(err, errCLILoginExpired):
		s.Throttle.Fail(key)
		writeError(w, http.StatusGone, errCLILoginExpired.Error())
		return
	case err != nil:
		s.Log.Error("saving a CLI login's credential", "account", a.Username, "err", err)
		writeError(w, http.StatusInternalServerError, "the sign-in couldn't be saved yet; the next poll tries again")
		return
	}
	if err := s.Accounts.TouchLogin(a.Username); err != nil {
		s.Log.Warn("recording account login", "account", a.Username, "err", err)
	}
	s.Log.Info("signed in a CLI", "account", a.Username, "client", client, "ip", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"credential": bearer, "username": a.Username, "computers": s.cliComputers(a)})
}
