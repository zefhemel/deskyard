package app

import (
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

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
	h.run("-c", "al", "click", "1", "2")
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

func TestCallerFromIgnoresClaudeCodeEnvWhenTreeReadable(t *testing.T) {
	proc := fstest.MapFS{
		"50/stat":    {Data: []byte("50 (bash) S 1 1 1 0")},
		"50/cmdline": {Data: []byte("bash\x00")},
		"50/comm":    {Data: []byte("bash\n")},
		"50/fd/0":    {Data: []byte("/dev/pts/3"), Mode: fs.ModeSymlink},
	}
	env := map[string]string{"CLAUDECODE": "1"}
	getenv := func(k string) string { return env[k] }
	if got := callerFrom(proc, 50, getenv); got != "a terminal" {
		t.Errorf("terminal with CLAUDECODE=1: %q", got)
	}
	// Tree out of sight: the env names Claude Code; without it, a terminal.
	if got := callerFrom(fstest.MapFS{}, 50, getenv); got != "Claude Code" {
		t.Errorf("unreadable tree, CLAUDECODE=1: %q", got)
	}
	env["CLAUDECODE"] = ""
	if got := callerFrom(fstest.MapFS{}, 50, getenv); got != "a terminal" {
		t.Errorf("unreadable tree: %q", got)
	}
}

func TestRecordSurvivesPanickingCaller(t *testing.T) {
	h := insideHarness(t)
	a := &App{Getenv: func(string) string { return "" }, Caller: func() string { panic("boom") },
		Record: func(e activity.Event) { h.events = append(h.events, e) }}
	a.record(activity.Event{Kind: "screen"}) // must not panic
	if len(h.events) != 0 {
		t.Errorf("recorded: %+v", h.events)
	}
}
