package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type result struct {
	code           int
	stdout, stderr string
}

type harness struct {
	t      *testing.T
	env    map[string]string
	inside bool
	config string
	stdin  string
	// events collects what the App records (Record/Caller are faked).
	events []activity.Event
	// opened collects the URLs the App opened in a browser.
	opened []string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, env: map[string]string{}, config: filepath.Join(t.TempDir(), "servers.json")}
}

// save signs the harness in to f as the server name (the first is the
// default), with the fake's credential and environment env.
func (h *harness) save(name string, f *clitest.Fake, env string) {
	h.t.Helper()
	cfg, err := remote.Load(h.config)
	if err != nil {
		h.t.Fatal(err)
	}
	cfg.Add(&remote.Server{Name: name, URL: f.URL, Username: clitest.Username, Credential: clitest.Credential, Environment: env})
	if err := cfg.Save(h.config); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) servers() *remote.Config {
	h.t.Helper()
	cfg, err := remote.Load(h.config)
	if err != nil {
		h.t.Fatal(err)
	}
	return cfg
}

func (h *harness) run(args ...string) result {
	var out, errb bytes.Buffer
	a := &App{
		Stdin: strings.NewReader(h.stdin), Stdout: &out, Stderr: &errb,
		Getenv:     func(k string) string { return h.env[k] },
		ConfigPath: h.config,
		Inside:     func() bool { return h.inside },
		Version:    "test",
		Record:     func(e activity.Event) { h.events = append(h.events, e) },
		Caller:     func() string { return "a terminal" },
		OpenBrowser: func(u string) error {
			h.opened = append(h.opened, u)
			return nil
		},
	}
	code := a.Run(context.Background(), args)
	return result{code, out.String(), errb.String()}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file (run go test ./internal/cli/app -update to accept):\n--- got\n%s", path, got)
	}
}

func TestGoldenHelp(t *testing.T) {
	h := newHarness(t)
	r := h.run("--help")
	if r.code != 0 {
		t.Fatalf("--help exit %d", r.code)
	}
	golden(t, "help/overview.txt", r.stdout)
	for _, c := range visibleCommands() {
		args := append(strings.Fields(c.Name), "--help")
		r := h.run(args...)
		if r.code != 0 || r.stderr != "" {
			t.Errorf("%s --help: exit %d stderr %q", c.Name, r.code, r.stderr)
		}
		golden(t, "help/"+strings.ReplaceAll(c.Name, " ", "_")+".txt", r.stdout)
		// help CMD is the same.
		if r2 := h.run(append([]string{"help"}, strings.Fields(c.Name)...)...); r2.stdout != r.stdout {
			t.Errorf("help %s differs from %s --help", c.Name, c.Name)
		}
	}
	if r := h.run("server", "--help"); r.code != 0 || !strings.Contains(r.stdout, "server list") {
		t.Errorf("server --help: %+v", r)
	}
}

func TestGoldenGuide(t *testing.T) {
	r := newHarness(t).run("guide")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if n := strings.Count(r.stdout, "\n"); n > 120 {
		t.Errorf("the guide has %d lines; keep it under 120", n)
	}
	golden(t, "guide.txt", r.stdout)
}

func TestGoldenDescribe(t *testing.T) {
	r := newHarness(t).run("describe", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	var doc struct {
		Commands []struct {
			Name string `json:"name"`
			Args []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"arguments"`
			Output struct{ Type string } `json:"output"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Commands) != len(visibleCommands()) {
		t.Errorf("describe lists %d commands, want %d", len(doc.Commands), len(visibleCommands()))
	}
	for _, c := range doc.Commands {
		if c.Output.Type == "" {
			t.Errorf("%s has no output type", c.Name)
		}
	}
	golden(t, "describe.json", r.stdout)
}

func TestHiddenCommands(t *testing.T) {
	h := newHarness(t)
	var hidden []string
	for _, c := range commands {
		if c.Hidden {
			hidden = append(hidden, c.Name)
		}
	}
	if len(hidden) == 0 {
		t.Fatal("no hidden commands")
	}
	for _, args := range [][]string{{"--help"}, {"guide"}, {"describe", "--json"}} {
		out := h.run(args...).stdout
		for _, name := range hidden {
			if strings.Contains(out, name) {
				t.Errorf("%v lists hidden %s", args, name)
			}
		}
	}
	// Still runnable and documented on request.
	if r := h.run("forward-helper", "--help"); r.code != 0 || !strings.Contains(r.stdout, "forward protocol") {
		t.Errorf("forward-helper --help: %+v", r)
	}
}

func TestSpecIsConsistent(t *testing.T) {
	for _, c := range commands {
		if c.run == nil || c.Summary == "" || c.Desc == "" || len(c.Examples) == 0 || c.Where == "" || c.Exit == "" || c.Output.Type == "" {
			t.Errorf("%s: incomplete spec", c.Name)
		}
		if !strings.HasPrefix(c.Usage, c.Name) {
			t.Errorf("%s: usage %q should start with the name", c.Name, c.Usage)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	h.inside = true
	for _, args := range [][]string{{}, {"bogus"}, {"click", "1"}, {"zoom", "50", "50", "10", "10"}, {"server"}, {"server", "bogus"},
		{"exec"}, {"cp", "a"}, {"login"}, {"auth"}, {"auth", "https://h/notalink"}, {"login", "ftp://h"}, {"env", "use"}, {"-e", "a/"}, {"-e", "/x"}, {"--nope"}, {"-e"}, {"status", "--bogus"}, {"exec", "--cwd"}} {
		r := h.run(args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
		if r.stderr == "" {
			t.Errorf("%v: no message", args)
		}
	}
}

func TestNoServerChosen(t *testing.T) {
	h := newHarness(t)
	r := h.run("screenshot")
	if r.code != 1 || !strings.Contains(r.stderr, "not signed in to any server: run `deskyard login https://host/ENV`") {
		t.Errorf("%+v", r)
	}
	r = h.run("-e", "nope/x", "size")
	if r.code != 1 || !strings.Contains(r.stderr, `no server named "nope"`) {
		t.Errorf("%+v", r)
	}
	h.env["DESKYARD_URL"] = "https://h/alice"
	r = h.run("size")
	if r.code != 1 || !strings.Contains(r.stderr, "DESKYARD_CREDENTIAL") {
		t.Errorf("%+v", r)
	}
	delete(h.env, "DESKYARD_URL")

	f := clitest.New(t, "alice")
	h.save("srv", f, "")
	r = h.run("size")
	if r.code != 1 || !strings.Contains(r.stderr, "no environment chosen on srv: use -e ENV, or choose a default with `deskyard env use ENV`") {
		t.Errorf("no environment: %+v", r)
	}
	// Inside an environment, no environment chosen means here.
	h.inside = true
	if r := h.run("browser", "connect"); r.code != 0 || r.stdout != "http://127.0.0.1:9221\n" {
		t.Errorf("inside: %+v", r)
	}
}

func remoteHarness(t *testing.T) (*harness, *clitest.Fake) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runs commands with sh")
	}
	f := clitest.New(t, "alice")
	h := newHarness(t)
	h.save("srv", f, "alice")
	return h, f
}

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

func TestRemoteCommandsRemoved(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"remote", "add", "x", "https://h/x"}, {"remote", "list"}, {"remote"}} {
		r := h.run(args...)
		if r.code != 2 || !strings.Contains(r.stderr, `unknown command "remote"`) || !strings.Contains(r.stderr, "deskyard login") {
			t.Errorf("%v: %+v", args, r)
		}
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

func TestStatusRemote(t *testing.T) {
	h, _ := remoteHarness(t)
	r := h.run("status")
	if r.code != 0 || !strings.Contains(r.stdout, "name:   alice") || !strings.Contains(r.stdout, "screen: 1280x800") {
		t.Errorf("%+v", r)
	}
	r = h.run("status", "--json")
	var st map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil || st["state"] != "running" || st["server"] != "srv" {
		t.Errorf("%s %v", r.stdout, err)
	}
}

func TestEnvRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	f := clitest.New(t, "alice")
	h := newHarness(t)
	h.env["DESKYARD_URL"] = f.EnvironmentURL()
	h.env["DESKYARD_CREDENTIAL"] = clitest.Credential
	if r := h.run("size"); r.code != 0 || r.stdout != "1280 800\n" {
		t.Errorf("%+v", r)
	}
	// DESKYARD_URL is the default server: -e ENV picks among its environments.
	f.Grants = append(f.Grants, clitest.Grant{Name: "bob", Level: "full"})
	if r := h.run("-e", "bob", "status", "--json"); !strings.Contains(r.stdout, `"name": "bob"`) {
		t.Errorf("-e bob: %+v", r)
	}
	h.env["DESKYARD_URL"] = f.URL
	if r := h.run("size"); r.code != 1 || !strings.Contains(r.stderr, "no environment chosen") {
		t.Errorf("no environment: %+v", r)
	}
}

func TestExecRemote(t *testing.T) {
	h, f := remoteHarness(t)
	r := h.run("exec", "--", "sh", "-c", "echo hi; echo oops >&2; exit 7")
	if r.code != 7 || r.stdout != "hi\n" || r.stderr != "oops\n" {
		t.Errorf("%+v", r)
	}
	r = h.run("exec", "--json", "-e", "A=1", "--cwd", f.Dir, "sh", "-c", "echo $A; printf '\\377' >&2")
	var obj map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &obj); err != nil || r.code != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if obj["stdout"] != "1\n" || obj["exit"] != float64(0) || obj["stderrBase64"] != "/w==" {
		t.Errorf("json: %v", obj)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if last.Cwd != f.Dir || last.Env["A"] != "1" {
		t.Errorf("request: %+v", last)
	}
	h.stdin = "piped input"
	if r := h.run("exec", "--", "cat"); r.stdout != "piped input" {
		t.Errorf("stdin: %+v", r)
	}
}

func TestDesktopRemote(t *testing.T) {
	h, f := remoteHarness(t)
	if r := h.run("size"); r.code != 0 || r.stdout != "1280 800\n" {
		t.Errorf("size: %+v", r)
	}
	if r := h.run("screenshot"); r.stdout != string(clitest.FakePNG) {
		t.Errorf("screenshot to stdout: %q", r.stdout)
	}
	file := filepath.Join(t.TempDir(), "shot.png")
	h.env["DESKYARD_SETTLE"] = "0"
	if r := h.run("zoom", "0", "0", "10", "10", file); r.code != 0 || r.stdout != "" {
		t.Errorf("zoom to file: %+v", r)
	}
	if b, _ := os.ReadFile(file); string(b) != string(clitest.FakePNG) {
		t.Errorf("file = %q", b)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if strings.Join(last.Argv, " ") != "deskyard zoom 0 0 10 10 -" || last.Env["DESKYARD_SETTLE"] != "0" {
		t.Errorf("zoom request: %+v", last)
	}
	// Usage errors are caught before going remote.
	n := len(f.Requests())
	if r := h.run("click", "1"); r.code != 2 || len(f.Requests()) != n {
		t.Errorf("click 1: %+v", r)
	}
	// The remote's exit code passes through.
	f.Desktop = func(args []string, stdout, stderr io.Writer) int {
		io.WriteString(stderr, "no display\n")
		return 5
	}
	if r := h.run("key", "Return"); r.code != 5 || r.stderr != "no display\n" {
		t.Errorf("key: %+v", r)
	}
}

func TestCpRemote(t *testing.T) {
	h, f := remoteHarness(t)
	local := t.TempDir()
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("A"), 0o644)
	if r := h.run("cp", filepath.Join(local, "a.txt"), "alice:"+f.Dir+"/"); r.code != 0 {
		t.Fatalf("upload: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Dir, "a.txt")); string(b) != "A" {
		t.Errorf("uploaded %q", b)
	}
	if r := h.run("cp", ":"+f.Dir+"/a.txt", filepath.Join(local, "b.txt")); r.code != 0 {
		t.Fatalf("download: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "b.txt")); string(b) != "A" {
		t.Errorf("downloaded %q", b)
	}
	if r := h.run("cp", "x", "y"); r.code != 2 || !strings.Contains(r.stderr, "ENV:/path") {
		t.Errorf("no remote side: %+v", r)
	}
	if r := h.run("cp", "alice:/a", "alice:/b"); r.code != 2 {
		t.Errorf("two remotes: %+v", r)
	}
	// NAME: is an environment on -e's server.
	g := clitest.New(t, "bob")
	h.save("other", g, "")
	if r := h.run("-e", "other/bob", "cp", filepath.Join(local, "a.txt"), "bob:"+g.Dir+"/"); r.code != 0 {
		t.Fatalf("upload to other: %+v", r)
	}
	if reqs := g.Requests(); len(reqs) == 0 || reqs[len(reqs)-1].Environment != "bob" {
		t.Errorf("went elsewhere: %+v", reqs)
	}
}

func TestBrowserConnectInside(t *testing.T) {
	h := newHarness(t)
	h.inside = true
	r := h.run("browser", "connect")
	if r.code != 0 || r.stdout != "http://127.0.0.1:9221\n" {
		t.Errorf("%+v", r)
	}
}

func TestExecLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	h := newHarness(t)
	h.inside = true
	r := h.run("exec", "--json", "--", "sh", "-c", "echo local; exit 4")
	var obj map[string]any
	json.Unmarshal([]byte(r.stdout), &obj)
	if obj["stdout"] != "local\n" || obj["exit"] != float64(4) {
		t.Errorf("%+v", r)
	}
	if r := h.run("exec", "--", "sh", "-c", "exit 3"); r.code != 3 {
		t.Errorf("exit code: %+v", r)
	}
	if r := h.run("exec", "--", "no-such-command-xyz"); r.code != 127 {
		t.Errorf("not found: %+v", r)
	}
}

func TestForwardArgs(t *testing.T) {
	cmd := findCommand("forward")
	if cmd == nil {
		t.Fatal("no forward command")
	}
	got, err := parseForwardArgs(cmd, []string{"3000", "3000:8000", "-R", "5173:5174", "--reverse", "9229"})
	want := []*portMap{{local: 3000, env: 3000}, {local: 3000, env: 8000}, {reverse: true, env: 5173, local: 5174}, {reverse: true, env: 9229, local: 9229}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %v %v", got, err)
	}
	for i := range want {
		if got[i].reverse != want[i].reverse || got[i].local != want[i].local || got[i].env != want[i].env {
			t.Errorf("mapping %d: got %+v, want %+v", i, *got[i], *want[i])
		}
	}
	h := newHarness(t)
	for _, args := range [][]string{{}, {"abc"}, {"0"}, {"70000"}, {"1:2:3"}, {"3000:"}, {":3000"}, {"-R"}, {"3000", "-R"}, {"-R", "-R", "1"}, {"-X", "3000"}, {"30.5"}, {"3000", "4000:3000"}, {"-R", "1:9000", "-R", "2:9000"}} {
		r := h.run(append([]string{"-e", "x", "forward"}, args...)...)
		if r.code != 2 || !strings.Contains(r.stderr, "usage: deskyard forward") {
			t.Errorf("forward %v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
	}
	h.inside = true
	r := h.run("forward", "3000")
	if r.code != 2 || !strings.Contains(r.stderr, "forward maps ports between this machine and an environment; choose one with -e ENV") {
		t.Errorf("inside: %+v", r)
	}
	if r := h.run("forward", "--help"); r.code != 0 || !strings.Contains(r.stdout, "-R") {
		t.Errorf("--help: %+v", r)
	}
}

// syncBuf is a bytes.Buffer safe to read while a command writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// start runs a long-running command; it ends when ctx does.
func (h *harness) start(ctx context.Context, args ...string) (stdout, stderr *syncBuf, code <-chan int) {
	stdout, stderr = &syncBuf{}, &syncBuf{}
	a := &App{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
		Getenv:     func(k string) string { return h.env[k] },
		ConfigPath: h.config,
		Inside:     func() bool { return h.inside },
		Version:    "test",
	}
	ch := make(chan int, 1)
	go func() { ch <- a.Run(ctx, args) }()
	return stdout, stderr, ch
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// echoServer echoes each line back with a prefix.
func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					fmt.Fprintf(c, "echo %s\n", sc.Text())
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func roundTrip(port int, msg string) (string, error) {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(c, "%s\n", msg)
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestForwardReconnects(t *testing.T) {
	h, f := remoteHarness(t)
	old := forwardBackoff
	forwardBackoff = func(n int) time.Duration { return time.Duration(20<<n) * time.Millisecond }
	t.Cleanup(func() { forwardBackoff = old })

	outDesktop := echoServer(t) // "in the desktop": the fake's helper side dials this machine
	outLocal := freePort(t)
	inLocal := echoServer(t)
	inDesktop := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, stderr, code := h.start(ctx, "forward", "-R", fmt.Sprintf("%d:%d", outDesktop, outLocal), fmt.Sprintf("%d:%d", inLocal, inDesktop))

	live := func(n int) func() bool {
		return func() bool {
			out := stdout.String()
			return strings.Count(out, fmt.Sprintf("out %d 127.0.0.1:%d\n", outDesktop, outLocal)) >= n &&
				strings.Count(out, fmt.Sprintf("in %d %d\n", inLocal, inDesktop)) >= n
		}
	}
	waitFor(t, "the mappings", 5*time.Second, live(1))
	if got := f.ForwardStarts()[0]; got != fmt.Sprintf(`{"forwards":[{"direction":"out","port":%d},{"direction":"in","port":%d}]}`, outDesktop, inDesktop) {
		t.Errorf("first message %s", got)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("Linked to alice: alice:%d → 127.0.0.1:%d, %d → alice:%d", outDesktop, outLocal, inLocal, inDesktop)) {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(outLocal, "a"); got != "echo a" {
		t.Errorf("out: %q %v", got, err)
	}
	if got, err := roundTrip(inDesktop, "b"); got != "echo b" {
		t.Errorf("in: %q %v", got, err)
	}

	// Dropped, then stopped twice: the local listener stays bound throughout.
	f.AnswerForwards(409, 409)
	f.DropForwards()
	for !live(2)() {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(outLocal)); err == nil {
			l.Close()
			t.Fatal("the local listener was released while reconnecting")
		}
		select {
		case c := <-code:
			t.Fatalf("exited %d: %s", c, stderr.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if n := len(f.ForwardStarts()); n != 2 {
		t.Errorf("%d connections, want 2", n)
	}
	if !strings.Contains(stderr.String(), "Lost alice (environment stopped), retrying in") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(outLocal, "c"); got != "echo c" {
		t.Errorf("out after reconnect: %q %v", got, err)
	}
	if got, err := roundTrip(inDesktop, "d"); got != "echo d" {
		t.Errorf("in after reconnect: %q %v", got, err)
	}

	// A refused credential ends it.
	f.AnswerForwards(http.StatusUnauthorized)
	f.DropForwards()
	select {
	case c := <-code:
		if c != 1 || !strings.Contains(stderr.String(), "(401): run `deskyard login") {
			t.Errorf("exit %d, stderr %s", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still running after a 401")
	}
}

func TestForwardPortInUse(t *testing.T) {
	h, _ := remoteHarness(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	q := taken.Addr().(*net.TCPAddr).Port
	inLocal := echoServer(t)
	inDesktop := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, stderr, code := h.start(ctx, "forward", "-R", fmt.Sprintf("%d:%d", echoServer(t), q), fmt.Sprintf("%d:%d", inLocal, inDesktop))
	waitFor(t, "the in mapping", 5*time.Second, func() bool { return strings.Contains(stdout.String(), fmt.Sprintf("in %d %d\n", inLocal, inDesktop)) })
	if !strings.Contains(stderr.String(), fmt.Sprintf("127.0.0.1:%d: port in use", q)) {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(inDesktop, "x"); got != "echo x" {
		t.Errorf("in: %q %v", got, err)
	}
	cancel()
	if c := <-code; c != 0 {
		t.Errorf("exit %d after Ctrl-C", c)
	}
}

func TestForwardArgsCaps(t *testing.T) {
	cmd := findCommand("forward")
	build := func(ins, outs int) []string {
		var args []string
		for i := 0; i < ins; i++ {
			args = append(args, strconv.Itoa(2000+i))
		}
		for i := 0; i < outs; i++ {
			args = append(args, "-R", strconv.Itoa(5000+i))
		}
		return args
	}
	for _, c := range []struct {
		ins, outs int
		ok        bool
	}{{64, 0, true}, {65, 0, false}, {64, 192, true}, {64, 193, false}, {0, 256, true}, {0, 257, false}} {
		_, err := parseForwardArgs(cmd, build(c.ins, c.outs))
		if (err == nil) != c.ok {
			t.Errorf("%d in, %d out: err %v, want ok=%v", c.ins, c.outs, err, c.ok)
		}
	}
	h := newHarness(t)
	r := h.run(append([]string{"-e", "x", "forward"}, build(65, 0)...)...)
	if r.code != 2 || !strings.Contains(r.stderr, "at most 64 mappings into the environment") {
		t.Errorf("65 ins: exit %d, stderr %q", r.code, r.stderr)
	}
}

// twoEnvironments signs in to two servers: a (the default, environment
// alice, also granting work) and b (environment bob), each a fake.
func twoEnvironments(t *testing.T) (*harness, *clitest.Fake, *clitest.Fake) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runs commands with sh")
	}
	alice, bob := clitest.New(t, "alice"), clitest.New(t, "bob")
	alice.Grants = append(alice.Grants, clitest.Grant{Name: "work", Level: "full"})
	h := newHarness(t)
	h.save("a", alice, "alice")
	h.save("b", bob, "bob")
	return h, alice, bob
}

func TestEnvironmentFlagServerSlashEnv(t *testing.T) {
	h, alice, bob := twoEnvironments(t)
	last := func(f *clitest.Fake) string {
		reqs := f.Requests()
		if len(reqs) == 0 {
			return ""
		}
		return reqs[len(reqs)-1].Environment
	}
	for _, args := range [][]string{
		{"-e", "b/bob", "screenshot"},
		{"--environment", "b/bob", "screenshot"},
		{"-e=b/bob", "screenshot"},
		{"--environment=b/bob", "screenshot"},
	} {
		na, nb := len(alice.Requests()), len(bob.Requests())
		r := h.run(args...)
		if r.code != 0 || r.stdout != string(clitest.FakePNG) {
			t.Errorf("%v: %+v", args, r)
		}
		if len(alice.Requests()) != na || len(bob.Requests()) != nb+1 || last(bob) != "bob" {
			t.Errorf("%v: went to the wrong environment (alice %d→%d, bob %d→%d)", args, na, len(alice.Requests()), nb, len(bob.Requests()))
		}
	}
	// -e ENV picks among the default server's environments.
	if r := h.run("-e", "work", "screenshot"); r.code != 0 || last(alice) != "work" {
		t.Errorf("-e work: %+v", r)
	}
	// Without -e the default server's environment is used.
	if r := h.run("screenshot"); r.code != 0 || last(alice) != "alice" {
		t.Errorf("default: %+v", r)
	}
	if r := h.run("-e", "bob", "size"); r.code != 1 || !strings.Contains(r.stderr, `no environment "bob"`) {
		t.Errorf("-e bob on a: %+v", r)
	}
	// DESKYARD_ENVIRONMENT picks one with the same syntax; -e wins over it.
	h.env["DESKYARD_ENVIRONMENT"] = "b/bob"
	if r := h.run("status", "--json"); !strings.Contains(r.stdout, `"name": "bob"`) {
		t.Errorf("DESKYARD_ENVIRONMENT: %+v", r)
	}
	if r := h.run("-e", "a/alice", "status", "--json"); !strings.Contains(r.stdout, `"name": "alice"`) {
		t.Errorf("-e over DESKYARD_ENVIRONMENT: %+v", r)
	}
	h.env["DESKYARD_ENVIRONMENT"] = "b/"
	if r := h.run("status"); r.code != 1 || !strings.Contains(r.stderr, "DESKYARD_ENVIRONMENT") {
		t.Errorf("bad DESKYARD_ENVIRONMENT: %+v", r)
	}
}

func TestOldTargetFlagsAreUnknown(t *testing.T) {
	h, alice, bob := twoEnvironments(t)
	for _, args := range [][]string{
		{"-d", "bob", "screenshot"},
		{"--desktop", "bob", "screenshot"},
		{"--desktop=bob", "screenshot"},
		{"-c=bob", "screenshot"},
		{"-e"},
		{"--environment"},
		{"-e="},
		{"status", "-e", "bob"},
		{"status", "--environment=bob"},
	} {
		r := h.run(args...)
		if r.code != 2 || !strings.HasPrefix(r.stderr, "deskyard") {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
	}
	if r := h.run("-d", "bob", "size"); !strings.Contains(r.stderr, `deskyard: unknown option "-d"`) {
		t.Errorf("-d message: %q", r.stderr)
	}
	if len(alice.Requests())+len(bob.Requests()) != 0 {
		t.Errorf("a refused command reached an environment")
	}
}

func TestGlobalEnvironmentAndExecEnv(t *testing.T) {
	h, alice, bob := twoEnvironments(t)
	r := h.run("-e", "b/bob", "exec", "-e", "CI=1", "--", "true")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(alice.Requests()) != 0 {
		t.Errorf("went to alice")
	}
	reqs := bob.Requests()
	if len(reqs) != 1 || reqs[0].Env["CI"] != "1" || strings.Join(reqs[0].Argv, " ") != "true" {
		t.Errorf("request: %+v", reqs)
	}
}

func TestErrorPrefix(t *testing.T) {
	h := newHarness(t)
	if r := h.run("bogus"); r.code != 2 || !strings.HasPrefix(r.stderr, "deskyard: ") {
		t.Errorf("usage error: %q", r.stderr)
	}
	if r := h.run("screenshot"); r.code != 1 || !strings.HasPrefix(r.stderr, "deskyard: not signed in") {
		t.Errorf("error: %q", r.stderr)
	}
}

func TestHelpNamesEnvironment(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"--help"}, {"guide"}, {"describe", "--json"}, {"exec", "--help"}} {
		out := h.run(args...).stdout
		if strings.Contains(out, "-d NAME") || strings.Contains(out, "--desktop") {
			t.Errorf("%v still mentions -d/--desktop", args)
		}
		if !strings.Contains(out, "-e ENV") {
			t.Errorf("%v doesn't mention -e ENV", args)
		}
	}
	var doc struct {
		GlobalFlags []Flag `json:"globalFlags"`
		Commands    []struct {
			Name  string `json:"name"`
			Flags []Flag `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(h.run("describe", "--json").stdout), &doc); err != nil {
		t.Fatal(err)
	}
	var global, execEnv string
	for _, f := range doc.GlobalFlags {
		if f.Name == "--environment" && f.Short == "-e" {
			global = f.Desc
		}
	}
	for _, c := range doc.Commands {
		for _, f := range c.Flags {
			if c.Name == "exec" && f.Name == "--env" && f.Short == "-e" {
				execEnv = f.Desc
			}
		}
	}
	if !strings.Contains(global, "before the command") || !strings.Contains(global, "exec -e") {
		t.Errorf("global -e description doesn't tell it apart from exec -e: %q", global)
	}
	if !strings.Contains(execEnv, "after exec") || !strings.Contains(execEnv, "environment variable") {
		t.Errorf("exec -e description doesn't tell it apart from the global -e: %q", execEnv)
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
