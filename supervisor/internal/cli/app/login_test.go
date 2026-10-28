package app

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// fastLogin makes deskyard login poll without waiting.
func fastLogin(t *testing.T) {
	old := loginInterval
	loginInterval = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { loginInterval = old })
}

func TestLoginSavesCredentialAndEnvironment(t *testing.T) {
	fastLogin(t)
	f := clitest.New(t, "alice")
	f.LoginPending = 2
	h := newHarness(t)
	r := h.run("login", f.EnvironmentURL())
	if r.code != 0 {
		t.Fatalf("login: %+v", r)
	}
	if !strings.Contains(r.stderr, "BCDF-GHJK") || !strings.Contains(r.stderr, f.URL+"/cli-login#req1") {
		t.Errorf("no code or approval URL: %s", r.stderr)
	}
	if len(h.opened) != 1 || h.opened[0] != f.URL+"/cli-login#req1" {
		t.Errorf("opened %v", h.opened)
	}
	if c := f.Clients(); len(c) != 1 || !strings.HasPrefix(c[0], "deskyard CLI on ") {
		t.Errorf("client name %v", c)
	}
	if !strings.Contains(r.stdout, "Signed in to 127.0.0.1 as zef") || !strings.Contains(r.stdout, "alice") {
		t.Errorf("stdout: %s", r.stdout)
	}
	cfg := h.servers()
	s := cfg.Servers["127.0.0.1"]
	if s == nil || cfg.Default != "127.0.0.1" || s.URL != f.URL || s.Username != clitest.Username || s.Environment != "alice" || !f.Valid(s.Credential) || s.Credential == clitest.Credential {
		t.Fatalf("servers.json: %+v %+v", cfg, s)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(h.config); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("servers.json mode: %v %v", fi.Mode(), err)
		}
	}
	if r := h.run("status", "--json"); r.code != 0 || !strings.Contains(r.stdout, `"name": "alice"`) {
		t.Errorf("status after login: %+v", r)
	}

	// --no-browser doesn't open one; logging in again under the same name
	// replaces the server and revokes its old credential.
	h.opened = nil
	r = h.run("login", f.URL, "--no-browser")
	if r.code != 0 || len(h.opened) != 0 {
		t.Fatalf("login --no-browser: %+v %v", r, h.opened)
	}
	s2 := h.servers().Servers["127.0.0.1"]
	if s2.Credential == s.Credential || f.Valid(s.Credential) || s2.Environment != "alice" {
		t.Errorf("relogin: %+v (old still valid: %v)", s2, f.Valid(s.Credential))
	}

	f.LoginDeny = true
	if r := h.run("login", f.URL, "--name", "other", "--no-browser"); r.code != 1 || !strings.Contains(r.stderr, "denied") {
		t.Errorf("denied: %+v", r)
	}
	if h.servers().Servers["other"] != nil {
		t.Errorf("a denied login was saved")
	}
}

// A server that can't save the credential yet keeps the approval: the CLI
// polls on and signs in once it can.
func TestLoginRetriesWhenTheServerCantSave(t *testing.T) {
	fastLogin(t)
	f := clitest.New(t, "alice")
	f.LoginSaveFails = 2
	h := newHarness(t)
	if r := h.run("login", f.URL, "--no-browser"); r.code != 0 || !strings.Contains(r.stdout, "Signed in to 127.0.0.1 as zef") {
		t.Fatalf("login: %+v", r)
	}
}

func TestLoginRejectsUnknownEnvironment(t *testing.T) {
	fastLogin(t)
	f := clitest.New(t, "alice")
	f.Grants = append(f.Grants, clitest.Grant{Name: "seen", Level: "observe"})
	h := newHarness(t)
	for _, env := range []string{"bob", "seen"} {
		r := h.run("login", f.URL+"/"+env, "--no-browser")
		if r.code != 1 || !strings.Contains(r.stderr, env) || !strings.Contains(r.stderr, "alice") {
			t.Errorf("%s: %+v", env, r)
		}
	}
	if cfg := h.servers(); len(cfg.Servers) != 0 {
		t.Errorf("saved %+v", cfg.Servers)
	}
	if got := f.Revoked(); len(got) != 2 {
		t.Errorf("the rejected credentials weren't revoked: %v", got)
	}
}

func TestAuthLinkSavesServer(t *testing.T) {
	f := clitest.New(t, "alice")
	h := newHarness(t)
	r := h.run("auth", f.LinkURL(), "--name", "work")
	if r.code != 0 || !strings.Contains(r.stdout, "Signed in to work as zef") {
		t.Fatalf("auth: %+v", r)
	}
	cfg := h.servers()
	s := cfg.Servers["work"]
	if s == nil || cfg.Default != "work" || s.URL != f.URL || s.Environment != "alice" || !f.Valid(s.Credential) {
		t.Fatalf("servers.json: %+v %+v", cfg, s)
	}
	if c := f.Clients(); len(c) != 1 || !strings.HasPrefix(c[0], "deskyard CLI on ") {
		t.Errorf("client name %v", c)
	}
	if r := h.run("auth", f.LinkURL(), "--name", "again"); r.code != 1 || !strings.Contains(r.stderr, "already used") {
		t.Errorf("second use: %+v", r)
	}

	// An observe-only share is for the browser.
	g := clitest.New(t, "alice")
	g.Grants = []clitest.Grant{{Name: "alice", Level: "observe"}}
	if r := h.run("auth", g.LinkURL()); r.code != 1 || !strings.Contains(r.stderr, "observe-only access is for the browser") {
		t.Errorf("observe: %+v", r)
	}

	// Several environments: none is chosen.
	k := clitest.New(t, "alice")
	k.Grants = append(k.Grants, clitest.Grant{Name: "bob", Level: "full"})
	if r := h.run("auth", k.LinkURL(), "--name", "multi"); r.code != 0 || !strings.Contains(r.stdout, "env use") {
		t.Errorf("several: %+v", r)
	}
	if s := h.servers().Servers["multi"]; s == nil || s.Environment != "" {
		t.Errorf("several: %+v", s)
	}
}

// Two servers on one host get the same default name: the second login or
// link is refused before it reaches the server, keeping the first login.
func TestLoginRefusesAnotherServerUnderTheSameDefaultName(t *testing.T) {
	fastLogin(t)
	f := clitest.New(t, "alice")
	g := clitest.New(t, "alice")
	h := newHarness(t)
	if r := h.run("auth", f.LinkURL()); r.code != 0 {
		t.Fatalf("auth: %+v", r)
	}
	first := h.servers().Servers["127.0.0.1"]
	for _, args := range [][]string{{"login", g.URL, "--no-browser"}, {"auth", g.LinkURL()}} {
		r := h.run(args...)
		if r.code != 2 || !strings.Contains(r.stderr, f.URL) || !strings.Contains(r.stderr, "--name") {
			t.Errorf("%s: %+v", args[0], r)
		}
	}
	if s := h.servers().Servers["127.0.0.1"]; s == nil || s.URL != f.URL || s.Credential != first.Credential || !f.Valid(first.Credential) {
		t.Errorf("the first server's login changed: %+v (valid %v)", s, f.Valid(first.Credential))
	}
	if len(g.Calls()) != 0 || len(g.Clients()) != 0 {
		t.Errorf("the second server was asked: %+v", g.Calls())
	}
	// Under a name of its own it is saved beside the first.
	if r := h.run("auth", g.LinkURL(), "--name", "other"); r.code != 0 || h.servers().Servers["127.0.0.1"].URL != f.URL {
		t.Errorf("--name: %+v", r)
	}
}

func TestLogoutRevokesThenForgets(t *testing.T) {
	h, f := remoteHarness(t)
	g := clitest.New(t, "bob")
	h.save("gone", g, "bob")
	r := h.run("logout")
	if r.code != 0 {
		t.Fatalf("logout: %+v", r)
	}
	if got := f.Revoked(); len(got) != 1 || got[0] != remote.CredentialID(clitest.Credential) {
		t.Errorf("revoked %v", got)
	}
	if cfg := h.servers(); cfg.Servers["srv"] != nil || cfg.Servers["gone"] == nil {
		t.Errorf("servers: %+v", cfg.Servers)
	}
	if r := h.run("logout", "srv"); r.code != 1 || !strings.Contains(r.stderr, `no server named "srv"`) {
		t.Errorf("again: %+v", r)
	}
	// A credential the server no longer accepts (401) is forgotten anyway.
	cfg := h.servers()
	cfg.Servers["gone"].Credential = "dyc_0000dead_revoked"
	cfg.Save(h.config)
	if r := h.run("logout", "gone"); r.code != 0 {
		t.Errorf("401: %+v", r)
	}
	if cfg := h.servers(); len(cfg.Servers) != 0 {
		t.Errorf("servers: %+v", cfg.Servers)
	}
}

func TestServerCommands(t *testing.T) {
	h, f := remoteHarness(t)
	g := clitest.New(t, "bob")
	h.save("two", g, "")

	r := h.run("server", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "* srv") || !strings.Contains(r.stdout, "dyc_0a1b2c3d_…") || strings.Contains(r.stdout, "secret") {
		t.Errorf("list: %+v", r)
	}
	r = h.run("server", "list", "--json")
	var rows []map[string]any
	if json.Unmarshal([]byte(r.stdout), &rows); len(rows) != 2 || rows[0]["default"] != true || rows[0]["environment"] != "alice" || rows[0]["username"] != "zef" || rows[0]["credential"] != nil {
		t.Errorf("list --json: %s", r.stdout)
	}
	if r = h.run("server", "default", "two"); r.code != 0 {
		t.Errorf("default: %+v", r)
	}
	if r = h.run("server", "default"); strings.TrimSpace(r.stdout) != "two" {
		t.Errorf("default shows %q", r.stdout)
	}
	if r = h.run("server", "default", "nope"); r.code != 1 {
		t.Errorf("default nope: %+v", r)
	}

	// env use checks the environment, then saves it for the server.
	if r = h.run("env", "use", "nope"); r.code != 1 || !strings.Contains(r.stderr, `no environment "nope"`) {
		t.Errorf("env use nope: %+v", r)
	}
	if r = h.run("env", "use", "bob"); r.code != 0 || h.servers().Servers["two"].Environment != "bob" {
		t.Errorf("env use: %+v", r)
	}
	if r := h.run("status", "--json"); !strings.Contains(r.stdout, `"name": "bob"`) || !strings.Contains(r.stdout, `"server": "two"`) {
		t.Errorf("status: %+v", r)
	}
	f.Grants = append(f.Grants, clitest.Grant{Name: "work", Level: "full"})
	if r = h.run("env", "use", "srv/work"); r.code != 0 || h.servers().Servers["srv"].Environment != "work" {
		t.Errorf("env use srv/work: %+v", r)
	}
	// An environment the account can only observe is the browser's.
	f.Grants = append(f.Grants, clitest.Grant{Name: "demo", Level: "observe"})
	if r = h.run("env", "use", "srv/demo"); r.code != 1 || !strings.Contains(r.stderr, "observe") || h.servers().Servers["srv"].Environment != "work" {
		t.Errorf("env use srv/demo: %+v", r)
	}
}

// A login link, or a page of the web UI, given to login says what to do
// instead, before anyone approves anything.
func TestLoginRefusesLinksAndPages(t *testing.T) {
	f := clitest.New(t, "alice")
	h := newHarness(t)
	r := h.run("login", f.LinkURL())
	if r.code != 2 || !strings.Contains(r.stderr, "deskyard auth") {
		t.Errorf("login LINK: %+v", r)
	}
	for _, page := range []string{"accounts", "link", "volumes", "volumes/photos"} {
		r = h.run("login", f.URL+"/"+page, "--no-browser")
		if r.code != 2 || !strings.Contains(r.stderr, "isn't an environment") {
			t.Errorf("login %s: %+v", page, r)
		}
	}
	for _, c := range f.Calls() {
		if c.Path == "/api/cli-login" {
			t.Errorf("started a CLI login: %+v", c)
		}
	}
}
