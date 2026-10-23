package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type applicationsEngine struct {
	filesEngine
	calls  []engine.ExecSpec
	output string
}

func (e *applicationsEngine) Exec(_ context.Context, _ string, spec engine.ExecSpec) (*engine.Process, error) {
	e.calls = append(e.calls, spec)
	return &engine.Process{Stdout: strings.NewReader(e.output), Stderr: strings.NewReader(""), Wait: func() (int, error) { return 0, nil }, Close: func() {}}, nil
}
func TestApplicationsAccessAndValidation(t *testing.T) {
	e := &applicationsEngine{output: `{"apps":[],"windows":[]}`}
	_, ts := newTestServerWith(t, e)
	anna := login(t, ts, "anna")
	admin := login(t, ts, "admin")
	endpoint := ts.URL + "/api/desktops/alice/applications"
	for _, tc := range []struct {
		client *http.Client
		body   string
		origin bool
		want   int
	}{
		{anna, `{"action":"activate","id":"42"}`, true, 404},
		{admin, `{"action":"activate","id":"42"}`, false, 403},
		{admin, `{"action":"activate","id":"1; touch /tmp/oops"}`, true, 400},
		{admin, `{"action":"launch","id":"../../evil.desktop"}`, true, 400},
		{admin, `{"action":"exec","id":"terminal.desktop"}`, true, 400},
	} {
		req, _ := http.NewRequest("POST", endpoint, strings.NewReader(tc.body))
		if tc.origin {
			req.Header.Set("Origin", ts.URL)
		}
		resp, err := tc.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: got %d want %d", tc.body, resp.StatusCode, tc.want)
		}
	}
	if len(e.calls) != 0 {
		t.Fatal("refused requests executed commands")
	}
	code, body, _ := fetch(t, admin, endpoint)
	if code != 200 || !strings.Contains(string(body), `"windows":[]`) {
		t.Fatalf("%d %s", code, body)
	}
	if len(e.calls) != 1 || e.calls[0].User != "user" {
		t.Fatal("inventory must run as desktop user")
	}
}
func TestApplicationsRejectsInvalidAndOversizedOutput(t *testing.T) {
	for _, output := range []string{"not json", "null", `{}`, `{"apps":[{"id":{},"name":"bad"}],"windows":[]}`, strings.Repeat(" ", (1<<20)+1)} {
		e := &applicationsEngine{output: output}
		_, ts := newTestServerWith(t, e)
		c := login(t, ts, "admin")
		resp, err := c.Get(ts.URL + "/api/desktops/alice/applications")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 502 {
			t.Errorf("got %d", resp.StatusCode)
		}
	}
}
