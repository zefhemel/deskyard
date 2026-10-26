package app

import (
	"io"
	"reflect"
	"testing"
)

func TestTerminalUsage(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"terminal", "a.b"}, {"terminal", "main", "extra"}, {"terminal-helper"}, {"terminal-helper", "a:b"}, {"terminal-helper", "main", "extra"},
		{"terminal-kill"}, {"terminal-kill", "a.b"}, {"terminal-kill", "main", "extra"}, {"terminal-create", "extra"}, {"terminal-pick", "extra"},
		{"terminal-lease", "main"}, {"terminal-lease", "main", "0123456789abcdef0123456789abcdef", "create"}, {"terminal-lease", "main", "0123456789abcdef0123456789abcdef", "join", "extra"}} {
		if r := h.run(args...); r.code != 2 {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := h.run("terminal", "--help"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestTerminalRemote(t *testing.T) {
	h, f := remoteHarness(t)
	f.Desktop = func(args []string, stdout, stderr io.Writer) int { return 0 }
	for _, name := range []string{"main", "build-2"} {
		args := []string{"terminal"}
		if name != "main" {
			args = append(args, name)
		}
		if r := h.run(args...); r.code != 0 {
			t.Fatalf("%+v", r)
		}
		reqs := f.Requests()
		req := reqs[len(reqs)-1]
		if !reflect.DeepEqual(req.Argv, []string{"deskyard", "terminal-helper", name}) || !req.TTY || req.Cwd != "/workspace" {
			t.Fatalf("%+v", req)
		}
	}
}

func TestTerminalHelperCommands(t *testing.T) {
	for _, name := range []string{"terminal-kill", "terminal-create", "terminal-pick", "terminal-lease"} {
		cmd := findCommand(name)
		if cmd == nil || !cmd.Hidden || cmd.run == nil {
			t.Fatalf("%s: %+v", name, cmd)
		}
	}
	if args := findCommand("terminal-lease").Args; len(args) != 3 || args[2].Required || !reflect.DeepEqual(args[2].Enum, []string{"join"}) {
		t.Fatalf("%+v", args)
	}
}
