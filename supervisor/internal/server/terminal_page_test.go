package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestTerminalPageRouting(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/alice/terminal/", "/alice/terminal/?session=build-2"} {
		resp, body := get(t, admin, ts.URL+path)
		if resp.StatusCode != 200 || !strings.Contains(body, "<!doctype html>") {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
		if got := status(t, anna, "GET", ts.URL+path, ""); got != 404 {
			t.Errorf("invisible %s: %d", path, got)
		}
		if got := status(t, anon, "GET", ts.URL+path, ""); got != 302 {
			t.Errorf("anonymous %s: %d", path, got)
		}
	}
	admin.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := admin.Get(ts.URL + "/alice/terminal?session=build-2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/alice/terminal/?session=build-2" {
		t.Errorf("canonical route: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := status(t, admin, "GET", ts.URL+"/alice/terminal/no-such", ""); got != 404 {
		t.Errorf("nested route: %d", got)
	}
}
