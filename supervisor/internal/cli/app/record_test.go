package app

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

func insideHarness(t *testing.T) *harness {
	if runtime.GOOS == "windows" {
		t.Skip("runs commands with sh")
	}
	h := newHarness(t)
	h.inside = true
	return h
}

func TestRecordsLocalScreenAction(t *testing.T) {
	h := insideHarness(t)
	h.run("click", "10", "20") // xdotool isn't here: the action fails, the mark is still recorded
	if len(h.events) != 1 {
		t.Fatalf("events: %+v", h.events)
	}
	e := h.events[0]
	if e.Agent != "a terminal" || e.Kind != "screen" || e.Screen == nil ||
		e.Screen.Action != "click" || !reflect.DeepEqual(e.Screen.Pos, []int{10, 20}) {
		t.Errorf("event: %+v %+v", e, e.Screen)
	}

	h.events = nil
	h.run("type", "secret", "words")
	if len(h.events) != 1 || h.events[0].Screen.Chars != 12 {
		t.Fatalf("type: %+v", h.events)
	}
	b, _ := json.Marshal(h.events[0])
	if strings.Contains(string(b), "secret") {
		t.Errorf("typed text leaked: %s", b)
	}

	// Invalid arguments are a usage error and record nothing.
	h.events = nil
	if r := h.run("click", "x", "y"); r.code != 2 || len(h.events) != 0 {
		t.Errorf("%+v %+v", r, h.events)
	}
}

func TestRecordsLocalExec(t *testing.T) {
	h := insideHarness(t)
	h.run("exec", "--", "sh", "-c", "exit 3")
	if len(h.events) != 2 {
		t.Fatalf("events: %+v", h.events)
	}
	s, e := h.events[0], h.events[1]
	if s.Kind != "cmd-start" || s.Cmd != "sh -c 'exit 3'" || s.ID == "" || len(s.ID) != 8 {
		t.Errorf("start: %+v", s)
	}
	if e.Kind != "cmd-end" || e.ID != s.ID || e.Exit == nil || *e.Exit != 3 {
		t.Errorf("end: %+v", e)
	}

	h.events = nil
	h.run("exec", "--", "deskyard-no-such-program", "--token", "abc")
	if len(h.events) != 2 || h.events[0].Cmd != "deskyard-no-such-program --token •••" || *h.events[1].Exit != 127 {
		t.Errorf("masked: %+v", h.events)
	}
}

func TestRecordsShellAndCp(t *testing.T) {
	h := insideHarness(t)
	h.stdin = "exit 0\n"
	h.run("shell")
	if len(h.events) != 2 || h.events[0].Kind != "cmd-start" || h.events[0].Cmd != "shell" || h.events[1].Kind != "cmd-end" || h.events[1].ID != h.events[0].ID {
		t.Fatalf("shell: %+v", h.events)
	}

	h.events = nil
	t.Chdir(t.TempDir())
	os.WriteFile("a", []byte("x"), 0o644)
	h.run("cp", "a", "b")
	if len(h.events) != 2 || h.events[0].Cmd != "cp a b" || h.events[1].Kind != "cmd-end" {
		t.Fatalf("cp: %+v", h.events)
	}
}

func TestNothingRecordedWhenOff(t *testing.T) {
	h := insideHarness(t)
	h.env["DESKYARD_ACTIVITY"] = "off"
	h.run("click", "1", "2")
	h.run("exec", "--", "true")
	if len(h.events) != 0 {
		t.Errorf("recorded: %+v", h.events)
	}
}

func TestNothingRecordedRemotely(t *testing.T) {
	h, _ := remoteHarness(t)
	h.run("-d", "al", "click", "1", "2")
	h.run("exec", "--", "true")
	if len(h.events) != 0 {
		t.Errorf("recorded remotely: %+v", h.events)
	}
	l := insideHarness(t)
	l.run("size")
	l.run("cursor")
	l.run("wait", "0")
	if len(l.events) != 0 {
		t.Errorf("recorded: %+v", l.events)
	}
}

func TestDefaultCaller(t *testing.T) {
	// The process tree here is go test under a shell or CI: never empty, and
	// CLAUDECODE=1 names Claude Code when the tree doesn't.
	env := map[string]string{}
	c := defaultCaller(func(k string) string { return env[k] })
	if c() == "" {
		t.Error("empty caller name")
	}
	env["CLAUDECODE"] = "1"
	if got := c(); got != "Claude Code" {
		t.Errorf("CLAUDECODE=1: %q", got)
	}
	_ = activity.Event{}
}
