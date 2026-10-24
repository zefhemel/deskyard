package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func readEvent(t *testing.T, br *bufio.Reader) activity.Event {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e activity.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		return e
	}
}

func TestActivityServe(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	a := &App{}
	go func() {
		done <- runActivityServe(a, &call{ctx: ctx, flags: map[string][]string{"--listen": {addr}}})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("activity-serve did not stop")
		}
	}()

	// Wait for the HTTP side to be up.
	for i := 0; ; i++ {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		if i > 100 {
			t.Fatal("server never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	u, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	send := func(s string) { u.Write([]byte(s)) }
	valid := func(agent string) string {
		return `{"agent":"` + agent + `","kind":"screen","screen":{"action":"click","pos":[1,2]}}`
	}
	send(valid("claude"))
	send("not json")
	send(`{"agent":"big","kind":"cmd-start","id":"x","cmd":"` + strings.Repeat("a", 5000) + `"}`)

	get := func(query string) (*http.Response, *bufio.Reader) {
		t.Helper()
		resp, err := http.Get("http://" + addr + "/events?" + query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp, bufio.NewReader(resp.Body)
	}

	// The datagrams may take a moment to be read; retry the backlog read.
	var resp *http.Response
	var br *bufio.Reader
	var first activity.Event
	resp, br = get("after=0")
	first = readEvent(t, br)
	if first.Seq != 1 || first.Agent != "claude" || first.T.IsZero() || first.Kind != "screen" {
		t.Fatalf("first = %+v", first)
	}
	epoch := resp.Header.Get("X-Activity-Epoch")
	if epoch == "" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("headers %v", resp.Header)
	}

	send(valid("codex"))
	second := readEvent(t, br)
	if second.Seq != 2 || second.Agent != "codex" {
		t.Fatalf("second = %+v (invalid or oversized datagrams must be dropped)", second)
	}

	// A reader with the right epoch resumes after N; a wrong epoch gets everything.
	_, br2 := get("epoch=" + epoch + "&after=1")
	if e := readEvent(t, br2); e.Seq != 2 {
		t.Fatalf("resume got %+v", e)
	}
	_, br3 := get("epoch=other&after=99")
	if e := readEvent(t, br3); e.Seq != 1 {
		t.Fatalf("backlog got %+v", e)
	}

	if r, err := http.Get("http://" + addr + "/nope"); err != nil || r.StatusCode != 404 {
		t.Fatalf("/nope: %v %v", r, err)
	}
}

func TestActivityStatusIncludesOldRunningCommands(t *testing.T) {
	now := time.Now()
	ring := activity.NewRing(time.Minute, 10, func() time.Time { return now })
	ring.Add(activity.Event{Agent: "shell", Kind: "cmd-start", ID: "work", Cmd: "sleep"})
	now = now.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveEvents(w, r, ring) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status struct {
		Kind            string
		CommandsRunning *bool
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Kind != "status" || status.CommandsRunning == nil || !*status.CommandsRunning {
		t.Fatalf("%+v", status)
	}
}
