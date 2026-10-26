package remote

import (
	"context"
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
		{"https://host/api/agent/v1", "https://host", ""},
		{"https://host/cc/api/agent/v1/", "https://host/cc", ""},
		{" https://host/alice ", "https://host", "alice"},
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
	path := filepath.Join(t.TempDir(), "sub", "remotes.json")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Remotes) != 0 || c.Default != "" {
		t.Fatalf("missing file should be empty: %+v", c)
	}
	c.Add(&Remote{Name: "alice", URL: "https://h/alice", Base: "https://h", Environment: "alice", Token: "dyt_a_s"})
	c.Add(&Remote{Name: "bob", URL: "https://h/bob", Base: "https://h", Environment: "bob", Token: "dyt_b_s"})
	if c.Default != "alice" {
		t.Fatalf("first remote should be the default, got %q", c.Default)
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
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Default != "alice" || len(c2.Remotes) != 2 || c2.Remotes["bob"].Name != "bob" || c2.Remotes["bob"].Token != "dyt_b_s" {
		t.Fatalf("reloaded config differs: %+v", c2)
	}
	if got := c2.Sorted(); got[0].Name != "alice" || got[1].Name != "bob" {
		t.Errorf("Sorted: %v %v", got[0].Name, got[1].Name)
	}
	if !c2.Remove("alice") || c2.Default != "" {
		t.Errorf("removing the default should clear it")
	}
	if c2.Remove("alice") {
		t.Errorf("second remove should report false")
	}
}

func TestLoadBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remotes.json")
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
	if p != filepath.Join("/x/cfg", "deskyard", "remotes.json") {
		t.Errorf("DefaultPath = %q", p)
	}
}

func TestMaskToken(t *testing.T) {
	if got := MaskToken("dyt_abc_secretsecret"); got != "dyt_abc_…" {
		t.Errorf("MaskToken = %q", got)
	}
	if strings.Contains(MaskToken("whateverlongsecret"), "secret") {
		t.Errorf("MaskToken leaks the secret")
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

func TestInfoAndErrors(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &Client{Alias: "alice", Base: f.URL, Environment: "alice", Token: clitest.Token}
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "alice" || info.Screen.Width != 1280 || info.API != 1 {
		t.Errorf("info = %+v", info)
	}

	bad := *c
	bad.Token = "dyt_x_wrong"
	_, err = bad.Info(context.Background())
	if !IsStatus(err, 401) || !strings.Contains(err.Error(), "ask someone with access to alice for a new token") {
		t.Errorf("401 error = %v", err)
	}

	f.Stopped = true
	_, err = c.Info(context.Background())
	if !IsStatus(err, 409) || !strings.Contains(err.Error(), "alice is stopped; someone with access can start it") {
		t.Errorf("409 error = %v", err)
	}

	gone := &Client{Base: "http://127.0.0.1:1", Token: "t"}
	if _, err := gone.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Errorf("network error = %v", err)
	}
}
