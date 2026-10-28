package remote_test

// These tests talk to the clitest fake, which itself uses this package, so
// they live in the external test package.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

func TestInfoAndErrors(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &remote.Client{Alias: "srv", Base: f.URL, Environment: "alice", Token: clitest.Credential}
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
	if !remote.IsStatus(err, 401) || !strings.Contains(err.Error(), "run `deskyard login "+f.URL+"/alice --name srv` again") {
		t.Errorf("401 error = %v", err)
	}

	other := *c
	other.Environment = "bob"
	if _, err := other.Info(context.Background()); !remote.IsStatus(err, 404) || !strings.Contains(err.Error(), `no environment "bob"`) {
		t.Errorf("404 error = %v", err)
	}

	f.Stopped = true
	if info, err := c.Info(context.Background()); err != nil || info.State != "stopped" {
		t.Errorf("stopped status = %+v %v", info, err)
	}

	gone := &remote.Client{Base: "http://127.0.0.1:1", Token: "t"}
	if _, err := gone.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Errorf("network error = %v", err)
	}
}

func TestJSON(t *testing.T) {
	f := clitest.New(t, "alice")
	c := &remote.Client{Base: f.URL, Token: clitest.Credential}
	var env struct {
		Name string `json:"name"`
	}
	if err := c.JSON(context.Background(), "GET", "/environments/alice", nil, &env); err != nil || env.Name != "alice" {
		t.Errorf("GET: %+v %v", env, err)
	}
	if err := c.JSON(context.Background(), "GET", "/environments/nope", nil, &env); !remote.IsStatus(err, 404) || !strings.Contains(err.Error(), "no such environment") {
		t.Errorf("404: %v", err)
	}
	var out map[string]any
	in := map[string]string{"link": clitest.Link, "client": "cli", "name": "x"}
	if err := (&remote.Client{Base: f.URL}).JSON(context.Background(), "POST", "/links/redeem", in, &out); err != nil || out["username"] != clitest.Username {
		t.Errorf("POST: %v %v", out, err)
	}
	if err := c.JSON(context.Background(), "DELETE", "/me/credentials/"+remote.CredentialID(clitest.Credential), nil, nil); err != nil {
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
		c := &remote.Client{Base: srv.URL, Environment: "alice", Token: clitest.Credential, HTTP: hc}
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
	c := &remote.Client{Base: srv.URL, Environment: "alice", Token: clitest.Credential}
	err := c.JSON(context.Background(), http.MethodGet, "/me", nil, nil)
	if !remote.IsStatus(err, http.StatusPermanentRedirect) || !strings.Contains(err.Error(), "deskyard login https://deskyard.example.com") || strings.Contains(err.Error(), "<a") {
		t.Fatalf("%v", err)
	}
}
