package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// loginInterval is how long deskyard login waits between polls, given the
// server's interval in seconds (tests shorten it).
var loginInterval = func(seconds int) time.Duration {
	if seconds < 1 {
		seconds = 2
	}
	return time.Duration(seconds) * time.Second
}

var errNoDisplay = errors.New("no display to open a browser on")

// openBrowser opens URLs with open (macOS) or xdg-open, when there is a
// display to open them on.
func openBrowser(getenv func(string) string) func(string) error {
	return func(u string) error {
		var name string
		switch runtime.GOOS {
		case "darwin":
			if getenv("SSH_CONNECTION") != "" {
				return errNoDisplay
			}
			name = "open"
		case "windows":
			return errNoDisplay
		default:
			if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
				return errNoDisplay
			}
			name = "xdg-open"
		}
		cmd := exec.Command(name, u)
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
}

// clientName names this CLI's credential on the server.
func clientName() string {
	host, _ := os.Hostname()
	if host == "" {
		return "deskyard CLI"
	}
	if len(host) > 80 {
		host = host[:80]
	}
	return "deskyard CLI on " + host
}

// loginAnswer is what CLI login and link redemption return.
type loginAnswer struct {
	Status       string `json:"status"` // "pending" while a CLI login waits
	Credential   string `json:"credential"`
	Username     string `json:"username"`
	Environments []struct {
		Name  string `json:"name"`
		Level string `json:"level"`
	} `json:"environments"`
}

// serverName is --name, else the server's host name.
func serverName(c *call, base string) (string, error) {
	name := c.str("--name", "")
	if name == "" {
		if name = remote.DefaultName(base); name == "" {
			return "", usagef(c.cmd, "can't name the server after its address %s: pass --name NAME", base)
		}
	}
	if !remote.ValidName(name) {
		return "", usagef(c.cmd, "%q isn't a usable server name: use letters, digits, '.', '_' and '-'", name)
	}
	return name, nil
}

// ---- login

func runLogin(a *App, c *call) error {
	t, err := remote.ParseURL(c.args[0])
	if err != nil {
		return usagef(c.cmd, "%v", err)
	}
	name, err := serverName(c, t.Base)
	if err != nil {
		return err
	}
	cl := &remote.Client{Alias: name, Base: t.Base, HTTP: a.HTTP}
	var start struct {
		ID         string `json:"id"`
		Code       string `json:"code"`
		ApproveURL string `json:"approveURL"`
		PollSecret string `json:"pollSecret"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expiresIn"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/cli-login", map[string]string{"client": clientName()}, &start); err != nil {
		return err
	}
	if start.ID == "" || start.ApproveURL == "" {
		return fmt.Errorf("%s answered with something that isn't the Deskyard API: check the URL", t.Base)
	}
	fmt.Fprintf(a.Stderr, "To sign in, open %s in a browser where you are signed in to Deskyard,\ncheck that it shows the code %s and approve.\n", start.ApproveURL, start.Code)
	if !c.bool("--no-browser") && a.OpenBrowser != nil && a.OpenBrowser(start.ApproveURL) == nil {
		fmt.Fprintln(a.Stderr, "Opened it in your browser.")
	}
	fmt.Fprintln(a.Stderr, "Waiting for approval (Ctrl-C to stop)...")
	expires := start.ExpiresIn
	if expires <= 0 {
		expires = 600
	}
	deadline := time.Now().Add(time.Duration(expires) * time.Second)
	interval := loginInterval(start.Interval)
	again := fmt.Sprintf("run `deskyard login %s` again", c.args[0])
	for {
		if !sleepCtx(c.ctx, interval) {
			return context.Canceled
		}
		if time.Now().After(deadline) {
			return errors.New("the login request expired before it was approved: " + again)
		}
		var ans loginAnswer
		err := cl.JSON(c.ctx, http.MethodPost, "/cli-login/"+url.PathEscape(start.ID)+"/token", map[string]string{"pollSecret": start.PollSecret}, &ans)
		switch {
		case remote.IsStatus(err, http.StatusTooManyRequests):
			interval *= 2
			continue
		case remote.IsStatus(err, http.StatusForbidden):
			return errors.New("the login was denied in the browser: " + again + " if that was a mistake")
		case remote.IsStatus(err, http.StatusInternalServerError):
			continue // approved, but not saved yet: the server keeps it for the next poll
		case err != nil:
			return err
		case ans.Credential == "":
			continue // pending
		}
		return a.saveLogin(c, cl, ans, t.Environment)
	}
}

// ---- auth

func runAuth(a *App, c *call) error {
	base, code, err := remote.ParseLink(c.args[0])
	if err != nil {
		return usagef(c.cmd, "%v", err)
	}
	name, err := serverName(c, base)
	if err != nil {
		return err
	}
	cl := &remote.Client{Alias: name, Base: base, HTTP: a.HTTP}
	var ans loginAnswer
	err = cl.JSON(c.ctx, http.MethodPost, "/links/redeem", map[string]string{"link": code, "client": "cli", "name": clientName()}, &ans)
	switch {
	case remote.IsStatus(err, http.StatusGone):
		return fmt.Errorf("%v: ask whoever gave you the link for a new one", err)
	case remote.IsStatus(err, http.StatusForbidden):
		return fmt.Errorf("%v: open the link in a web browser instead", err)
	case err != nil:
		return err
	case ans.Credential == "":
		return fmt.Errorf("%s answered with something that isn't the Deskyard API: check the link", base)
	}
	return a.saveLogin(c, cl, ans, "")
}

// saveLogin saves the server cl signed in to with ans. want is the
// environment the login URL named: the account must have full access to it,
// or the new credential is revoked again and nothing is saved. Without one,
// the account's only full environment becomes the default, or the server's
// previous default when it is still allowed.
func (a *App) saveLogin(c *call, cl *remote.Client, ans loginAnswer, want string) error {
	var full, observed []string
	for _, e := range ans.Environments {
		if e.Level == "full" {
			full = append(full, e.Name)
		} else {
			observed = append(observed, e.Name)
		}
	}
	has := func(env string) bool {
		for _, n := range full {
			if n == env {
				return true
			}
		}
		return false
	}
	cfg, path, err := a.loadConfig()
	if err != nil {
		a.revoke(c.ctx, cl.Base, ans.Credential)
		return err
	}
	old := cfg.Servers[cl.Alias]
	env := want
	switch {
	case want != "" && !has(want):
		a.revoke(c.ctx, cl.Base, ans.Credential)
		yours := "none"
		if len(full) > 0 {
			yours = strings.Join(full, ", ")
		}
		return fmt.Errorf("%s can't use the environment %q from the CLI (environments with full access: %s): run `deskyard login %s` again with one of them, or without one",
			ans.Username, want, yours, cl.Base)
	case want != "":
	case len(full) == 1:
		env = full[0]
	case old != nil && old.URL == cl.Base && has(old.Environment):
		env = old.Environment
	}
	cfg.Add(&remote.Server{Name: cl.Alias, URL: cl.Base, Username: ans.Username, Credential: ans.Credential, Environment: env})
	if err := cfg.Save(path); err != nil {
		a.revoke(c.ctx, cl.Base, ans.Credential)
		return fmt.Errorf("saving %s: %v", path, err)
	}
	if old != nil && old.Credential != ans.Credential {
		// The replaced login's credential is of no use any more.
		a.revoke(c.ctx, old.URL, old.Credential)
	}
	fmt.Fprintf(a.Stdout, "Signed in to %s as %s (%s).\n", cl.Alias, ans.Username, cl.Base)
	switch {
	case env != "":
		fmt.Fprintf(a.Stdout, "Commands use the environment %s; try `deskyard status`.\n", env)
	case len(full) > 0:
		fmt.Fprintf(a.Stdout, "Your environments: %s. Choose one with `deskyard env use ENV`, or -e ENV per command.\n", strings.Join(full, ", "))
	default:
		fmt.Fprintln(a.Stdout, "This account has no environment it can use from the CLI yet.")
	}
	if len(observed) > 0 {
		fmt.Fprintf(a.Stdout, "Observe only, in the browser: %s.\n", strings.Join(observed, ", "))
	}
	if cfg.Default != cl.Alias {
		fmt.Fprintf(a.Stdout, "Use it with -e %s/ENV, or make it the default server with `deskyard server default %s`.\n", cl.Alias, cl.Alias)
	}
	return nil
}

// revoke deletes a credential on its server, best effort.
func (a *App) revoke(ctx context.Context, base, cred string) error {
	id := remote.CredentialID(cred)
	if id == "" {
		return nil
	}
	cl := &remote.Client{Base: base, Token: cred, HTTP: a.HTTP}
	return cl.JSON(ctx, http.MethodDelete, "/me/credentials/"+url.PathEscape(id), nil, nil)
}

// ---- logout

func runLogout(a *App, c *call) error {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	name := cfg.Default
	if len(c.args) > 0 {
		name = c.args[0]
	}
	srv := cfg.Servers[name]
	if srv == nil {
		if name == "" {
			return errors.New("no server chosen: run `deskyard server list`, then `deskyard logout NAME`")
		}
		return fmt.Errorf("no server named %q: run `deskyard server list`", name)
	}
	note := "its credential is revoked"
	err = a.revoke(c.ctx, srv.URL, srv.Credential)
	switch {
	case remote.IsStatus(err, http.StatusUnauthorized), remote.IsStatus(err, http.StatusNotFound):
		note = "the server no longer accepted its credential"
	case err != nil:
		return fmt.Errorf("%v; still signed in to %s: try again, or revoke the device in the web UI", err, name)
	}
	cfg.Remove(name)
	if err := cfg.Save(path); err != nil {
		return fmt.Errorf("saving %s: %v", path, err)
	}
	fmt.Fprintf(a.Stdout, "Signed out of %s (%s).\n", name, note)
	return nil
}

// ---- server list, server default

func runServerList(a *App, c *call) error {
	cfg, _, err := a.loadConfig()
	if err != nil {
		return err
	}
	if c.bool("--json") {
		type row struct {
			Name        string `json:"name"`
			URL         string `json:"url"`
			Username    string `json:"username"`
			Environment string `json:"environment"`
			Default     bool   `json:"default"`
		}
		rows := []row{}
		for _, s := range cfg.Sorted() {
			rows = append(rows, row{s.Name, s.URL, s.Username, s.Environment, s.Name == cfg.Default})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(cfg.Servers) == 0 {
		fmt.Fprintln(a.Stdout, "Not signed in to any server: "+loginHint+".")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tURL\tUSER\tENVIRONMENT\tCREDENTIAL")
	for _, s := range cfg.Sorted() {
		mark := " "
		if s.Name == cfg.Default {
			mark = "*"
		}
		env := s.Environment
		if env == "" {
			env = "-"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t%s\n", mark, s.Name, s.URL, s.Username, env, remote.MaskCredential(s.Credential))
	}
	return tw.Flush()
}

func runServerDefault(a *App, c *call) error {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	if len(c.args) == 0 {
		if cfg.Default != "" {
			fmt.Fprintln(a.Stdout, cfg.Default)
		}
		return nil
	}
	name := c.args[0]
	if cfg.Servers[name] == nil {
		return fmt.Errorf("no server named %q: run `deskyard server list`", name)
	}
	cfg.Default = name
	return cfg.Save(path)
}

// ---- env use

func runEnvUse(a *App, c *call) error {
	name, env, err := splitSelector(c.args[0])
	if err != nil {
		return usagef(c.cmd, "%v", err)
	}
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	if name == "" {
		name = cfg.Default
	}
	srv := cfg.Servers[name]
	if srv == nil {
		if name == "" {
			return a.noServer()
		}
		return fmt.Errorf("no server named %q: run `deskyard server list`", name)
	}
	cl := remote.FromServer(srv, env)
	cl.HTTP = a.HTTP
	if err := cl.JSON(c.ctx, http.MethodGet, "/environments/"+url.PathEscape(env), nil, nil); err != nil {
		return err
	}
	srv.Environment = env
	if err := cfg.Save(path); err != nil {
		return fmt.Errorf("saving %s: %v", path, err)
	}
	fmt.Fprintf(a.Stdout, "Commands on %s now use the environment %s.\n", srv.Name, env)
	return nil
}
