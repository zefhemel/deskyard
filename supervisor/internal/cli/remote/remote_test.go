package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in, base, name string
	}{
		{"https://host/alice", "https://host", "alice"},
		{"https://host/alice/", "https://host", "alice"},
		{"http://127.0.0.1:8080/alice", "http://127.0.0.1:8080", "alice"},
		{"https://host/prefix/alice", "https://host/prefix", "alice"},
		{"https://host", "https://host", ""},
		{"https://host/", "https://host", ""},
		{"https://host/api/environments/alice", "https://host", "alice"},
		{"https://host/cc/api/environments/alice/exec", "https://host/cc", "alice"},
		{"https://host/cc/api/me", "https://host/cc", ""},
		{" https://host/alice ", "https://host", "alice"},
		// The web UI's page of an environment.
		{"https://host/environments/alice", "https://host", "alice"},
		{"https://host/cc/environments/alice/", "https://host/cc", "alice"},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if err != nil {
			t.Errorf("ParseURL(%q): %v", c.in, err)
			continue
		}
		if got.Base != c.base || got.Environment != c.name {
			t.Errorf("ParseURL(%q) = %+v, want base %q name %q", c.in, got, c.base, c.name)
		}
	}
	for _, bad := range []string{"host/alice", "ftp://host/alice", "https:///alice", "https://u:p@host/alice", "https://host/alice?x=1", "::"} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("ParseURL(%q) succeeded, want an error", bad)
		}
	}
	if u := (Target{Base: "https://h/p", Environment: "a"}).URL(); u != "https://h/p/a" {
		t.Errorf("URL() = %q", u)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "servers.json")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 0 || c.Default != "" {
		t.Fatalf("missing file should be empty: %+v", c)
	}
	c.Add(&Server{Name: "h", URL: "https://h", Username: "zef", Credential: "dyc_0000000a_s", Environment: "alice"})
	c.Add(&Server{Name: "k", URL: "https://k", Username: "zef", Credential: "dyc_0000000b_s"})
	if c.Default != "h" {
		t.Fatalf("first server should be the default, got %q", c.Default)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
	data, _ := os.ReadFile(path)
	var raw struct {
		Default string                    `json:"default"`
		Servers map[string]map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Default != "h" || raw.Servers["h"]["credential"] != "dyc_0000000a_s" ||
		raw.Servers["h"]["url"] != "https://h" || raw.Servers["h"]["username"] != "zef" || raw.Servers["h"]["environment"] != "alice" {
		t.Errorf("servers.json: %s %v", data, err)
	}
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Default != "h" || len(c2.Servers) != 2 || c2.Servers["k"].Name != "k" || c2.Servers["k"].Credential != "dyc_0000000b_s" {
		t.Fatalf("reloaded config differs: %+v", c2)
	}
	if got := c2.Sorted(); got[0].Name != "h" || got[1].Name != "k" {
		t.Errorf("Sorted: %v %v", got[0].Name, got[1].Name)
	}
	if !c2.Remove("h") || c2.Default != "" {
		t.Errorf("removing the default should clear it")
	}
	if c2.Remove("h") {
		t.Errorf("second remove should report false")
	}
}

func TestLoadBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	os.WriteFile(path, []byte("{nope"), 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "fix or delete") {
		t.Fatalf("want a helpful error, got %v", err)
	}
}

func TestDefaultPathXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/x/cfg", "deskyard", "servers.json") {
		t.Errorf("DefaultPath = %q", p)
	}
}

func TestCredentialID(t *testing.T) {
	if got := CredentialID("dyc_0a1b2c3d_secretsecret"); got != "0a1b2c3d" {
		t.Errorf("CredentialID = %q", got)
	}
	for _, bad := range []string{"", "dyt_0a1b2c3d_s", "dyc_", "dyc_nounderscore", "dyc__s"} {
		if got := CredentialID(bad); got != "" {
			t.Errorf("CredentialID(%q) = %q, want empty", bad, got)
		}
	}
	if got := MaskCredential("dyc_0a1b2c3d_secretsecret"); got != "dyc_0a1b2c3d_…" {
		t.Errorf("MaskCredential = %q", got)
	}
	if strings.Contains(MaskCredential("whateverlongsecret"), "secret") {
		t.Errorf("MaskCredential leaks the secret")
	}
}

func TestDefaultName(t *testing.T) {
	for in, want := range map[string]string{"https://deskyard.example.com": "deskyard.example.com", "http://127.0.0.1:8080/pre": "127.0.0.1", "http://[::1]:80": ""} {
		if got := DefaultName(in); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLink(t *testing.T) {
	base, code, err := ParseLink("https://host/pre/link#dyl_0a1b2c3d_s3cret")
	if err != nil || base != "https://host/pre" || code != "dyl_0a1b2c3d_s3cret" {
		t.Errorf("ParseLink = %q %q %v", base, code, err)
	}
	for _, bad := range []string{"dyl_0a1b2c3d_s", "https://host/link", "https://host/link#dyc_x_y", "https://host/other#dyl_a_b", "ftp://host/link#dyl_a_b"} {
		if _, _, err := ParseLink(bad); err == nil {
			t.Errorf("ParseLink(%q) succeeded, want an error", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"alice", "a.b-c_d", "A1"} {
		if !ValidName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a/b", "a:b"} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestClientPaths(t *testing.T) {
	c := &Client{Base: "https://h/pre", Environment: "alice"}
	if got := c.URL("/exec"); got != "https://h/pre/api/environments/alice/exec" {
		t.Errorf("URL = %q", got)
	}
	if got := c.WSURL("/exec"); got != "wss://h/pre/api/environments/alice/exec" {
		t.Errorf("WSURL = %q", got)
	}
	if got := c.API("/me/credentials/x"); got != "https://h/pre/api/me/credentials/x" {
		t.Errorf("API = %q", got)
	}
}

func TestInfoAndErrors(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &Client{Alias: "srv", Base: f.URL, Environment: "alice", Token: clitest.Credential}
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "alice" || info.Screen.Width != 1280 || info.API != 1 {
		t.Errorf("info = %+v", info)
	}

	bad := *c
	bad.Token = "dyc_0000dead_wrong"
	_, err = bad.Info(context.Background())
	if !IsStatus(err, 401) || !strings.Contains(err.Error(), "run `deskyard login "+f.URL+"/alice --name srv` again") {
		t.Errorf("401 error = %v", err)
	}

	other := *c
	other.Environment = "bob"
	if _, err := other.Info(context.Background()); !IsStatus(err, 404) || !strings.Contains(err.Error(), `no environment "bob"`) {
		t.Errorf("404 error = %v", err)
	}

	f.Stopped = true
	if info, err := c.Info(context.Background()); err != nil || info.State != "stopped" {
		t.Errorf("stopped status = %+v %v", info, err)
	}

	gone := &Client{Base: "http://127.0.0.1:1", Token: "t"}
	if _, err := gone.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Errorf("network error = %v", err)
	}
}

func TestJSON(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &Client{Base: f.URL, Token: clitest.Credential}
	var env struct {
		Name string `json:"name"`
	}
	if err := c.JSON(context.Background(), "GET", "/environments/alice", nil, &env); err != nil || env.Name != "alice" {
		t.Errorf("GET: %+v %v", env, err)
	}
	if err := c.JSON(context.Background(), "GET", "/environments/nope", nil, &env); !IsStatus(err, 404) || !strings.Contains(err.Error(), "no such environment") {
		t.Errorf("404: %v", err)
	}
	var out map[string]any
	in := map[string]string{"link": clitest.Link, "client": "cli", "name": "x"}
	if err := (&Client{Base: f.URL}).JSON(context.Background(), "POST", "/links/redeem", in, &out); err != nil || out["username"] != clitest.Username {
		t.Errorf("POST: %v %v", out, err)
	}
	if err := c.JSON(context.Background(), "DELETE", "/me/credentials/"+CredentialID(clitest.Credential), nil, nil); err != nil {
		t.Errorf("DELETE: %v", err)
	}
	if f.Valid(clitest.Credential) {
		t.Errorf("credential still valid")
	}
}

// TestDoDoesNotFollowRedirects: a redirect must never carry the credential
// to another route.
func TestDoDoesNotFollowRedirects(t *testing.T) {
	var reached bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/environments/alice/x", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/me/credentials", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/api/me/credentials", func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, hc := range []*http.Client{nil, {}} {
		c := &Client{Base: srv.URL, Environment: "alice", Token: clitest.Credential, HTTP: hc}
		req, _ := http.NewRequest("GET", c.URL("/x"), nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently || reached {
			t.Errorf("status %d, reached redirect target %v", resp.StatusCode, reached)
		}
	}
}

// A redirect answer says where the server sent the request, so the user can
// sign in with the final URL.
func TestRedirectErrorNamesTheTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://deskyard.example.com"+r.URL.Path, http.StatusPermanentRedirect)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Environment: "alice", Token: clitest.Credential}
	err := c.JSON(context.Background(), http.MethodGet, "/me", nil, nil)
	if !IsStatus(err, http.StatusPermanentRedirect) || !strings.Contains(err.Error(), "deskyard login https://deskyard.example.com") || strings.Contains(err.Error(), "<a") {
		t.Fatalf("%v", err)
	}
}
