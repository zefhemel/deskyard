package server

import (
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

const defaultCredentialName = "deskyard CLI"

// linkURL is the address of the link page with the code in its fragment,
// which browsers never send: fetching the URL consumes nothing.
func (s *Server) linkURL(r *http.Request, code string) string {
	return s.baseURL(r) + "/link#" + code
}

// baseURL is the scheme and host the request came to.
func (s *Server) baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || s.Sessions.Secure {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// checkCLI is auth.Check of a CLI for a, counting its grants of defined
// environments only.
func (s *Server) checkCLI(a accounts.Account) error {
	a.Grants = s.definedGrants(a)
	return auth.Check(a, auth.UseCLI, "")
}

type environmentLevel struct {
	Name  string     `json:"name"`
	Level auth.Level `json:"level"`
}

// cliEnvironments lists what a CLI signed in as a may use: its grants of
// defined environments, or every environment at full for an admin, sorted
// by name.
func (s *Server) cliEnvironments(a accounts.Account) []environmentLevel {
	out := []environmentLevel{}
	if a.Admin {
		for _, d := range s.Environments.Defs.List() {
			out = append(out, environmentLevel{Name: d.Name, Level: auth.LevelFull})
		}
		return out
	}
	for _, g := range s.definedGrants(a) {
		out = append(out, environmentLevel{Name: g.Environment, Level: g.Level})
	}
	return out
}

type linkInfoJSON struct {
	Username    string           `json:"username"`
	Description string           `json:"description"`
	Grants      []accounts.Grant `json:"grants"`
	SharedBy    *accounts.Sharer `json:"sharedBy"`
	Expires     *time.Time       `json:"expires"`
}

// linkThrottled writes 429 while the client's IP has failed with too many
// link codes recently.
func (s *Server) linkThrottled(w http.ResponseWriter, r *http.Request) bool {
	if !s.Throttle.Allow("link:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts: wait a few seconds")
		return true
	}
	return false
}

// linkGone answers a used, replaced or unknown code and counts the failure.
func (s *Server) linkGone(w http.ResponseWriter, r *http.Request) {
	s.Throttle.Fail("link:" + clientIP(r))
	writeError(w, http.StatusGone, errLinkUsed.Error())
}

// linkAccount is the account a link signs in as, while it is the account
// the link was issued to and may use anything.
func (s *Server) linkAccount(l link) (accounts.Account, bool) {
	return s.Accounts.Usable(l.Username, l.AccountID, time.Now())
}

// handleInspectLink shows what a link signs in as, without using it.
func (s *Server) handleInspectLink(w http.ResponseWriter, r *http.Request) {
	var req struct{ Link string }
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if s.linkThrottled(w, r) {
		return
	}
	l, ok := s.links.peek(req.Link)
	if !ok {
		s.linkGone(w, r)
		return
	}
	a, ok := s.linkAccount(l)
	if !ok {
		s.linkGone(w, r)
		return
	}
	writeJSON(w, http.StatusOK, linkInfoJSON{Username: a.Username, Description: a.Description, Grants: s.definedGrants(a),
		SharedBy: a.SharedBy, Expires: a.Expires})
}

// handleRedeemLink uses a link: a browser gets a session, a CLI a client
// credential. The link is deleted, and that saved, before either is
// handed out; of concurrent redemptions exactly one succeeds.
func (s *Server) handleRedeemLink(w http.ResponseWriter, r *http.Request) {
	var req struct{ Link, Client, Name string }
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	switch req.Client {
	case "browser":
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
	case "cli":
		if utf8.RuneCountInString(req.Name) > 100 {
			writeError(w, http.StatusBadRequest, "a client name is at most 100 characters")
			return
		}
		if req.Name == "" {
			req.Name = defaultCredentialName
		}
	default:
		writeError(w, http.StatusBadRequest, `the client is "browser" or "cli"`)
		return
	}
	if s.linkThrottled(w, r) {
		return
	}
	var a accounts.Account
	var cli cliSignIn
	var cred credential
	_, err := s.links.redeem(req.Link, func(l link) error {
		var ok bool
		if a, ok = s.linkAccount(l); !ok {
			return errLinkUsed
		}
		// An earlier redemption of the link that failed, and couldn't
		// delete its credential either, left one whose bearer it never
		// handed out.
		if err := s.credentials.remove(func(c credential) bool { return c.Link == l.ID }); err != nil {
			return err
		}
		if req.Client != "cli" {
			return nil
		}
		// Made before the link is deleted, so a failure keeps the link; it
		// is handed out only once the deletion is saved.
		var err error
		cli, cred, err = s.issueCLICredential(a, req.Name, l.ID)
		return err
	})
	if err != nil && cred.ID != "" {
		if err := s.credentials.remove(func(c credential) bool { return c.ID == cred.ID }); err != nil {
			s.Log.Error("deleting the credential of a failed redemption", "credential", cred.ID, "err", err)
		}
	}
	switch {
	case errors.Is(err, errLinkUsed):
		s.linkGone(w, r)
		return
	case errors.Is(err, auth.ErrBrowserOnly):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		s.Log.Error("redeeming a login link", "account", a.Username, "err", err)
		writeError(w, http.StatusInternalServerError, "the sign-in couldn't be saved; try the link again")
		return
	}
	s.signedIn(a, "link, "+req.Client, r)
	if req.Client == "cli" {
		writeJSON(w, http.StatusOK, cli)
		return
	}
	s.replaceSession(w, r, a)
	next := "/"
	if grants := s.definedGrants(a); !a.Admin && len(grants) == 1 {
		next = "/" + grants[0].Environment + "/"
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": a.Username, "next": next})
}

// cliSignIn is what a CLI receives when it signs in: its credential's
// bearer, this once, and what it may use.
type cliSignIn struct {
	Credential   string             `json:"credential"`
	Username     string             `json:"username"`
	Environments []environmentLevel `json:"environments"`
}

// issueCLICredential makes a CLI credential named client for a as it is
// now, refusing (auth.ErrBrowserOnly) an account the CLI is of no use to. link is
// the login link being redeemed, if one is.
func (s *Server) issueCLICredential(a accounts.Account, client, link string) (cliSignIn, credential, error) {
	if err := s.checkCLI(a); err != nil {
		return cliSignIn{}, credential{}, err
	}
	c, bearer, err := s.credentials.add(credential{AccountID: a.ID, Username: a.Username, Kind: "cli", Name: client, Link: link})
	if err != nil {
		return cliSignIn{}, credential{}, err
	}
	return cliSignIn{Credential: bearer, Username: a.Username, Environments: s.cliEnvironments(a)}, c, nil
}

// signedIn records a sign-in to a, made via a password, a link or a CLI
// login.
func (s *Server) signedIn(a accounts.Account, via string, r *http.Request) {
	if err := s.Accounts.TouchLogin(a.Username); err != nil {
		s.Log.Warn("recording account login", "account", a.Username, "err", err)
	}
	s.Log.Info("signed in", "account", a.Username, "via", via, "ip", clientIP(r))
}
