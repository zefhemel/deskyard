package app

import (
	"io"
	"reflect"
	"testing"
)

func TestTerminalUsage(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"terminal", "a.b"}, {"terminal", "main", "extra"}, {"terminal-helper"}, {"terminal-helper", "a:b"}, {"terminal-helper", "main", "extra"}} {
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
