package activity

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeProc struct {
	pid, ppid int
	argv      []string
	comm      string
	fd0       string
}

func procFS(ps ...fakeProc) fstest.MapFS {
	m := fstest.MapFS{}
	for _, p := range ps {
		d := fmt.Sprint(p.pid)
		m[d+"/stat"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("%d (%s) S %d 1 1 0 -1", p.pid, p.comm, p.ppid))}
		m[d+"/cmdline"] = &fstest.MapFile{Data: []byte(strings.Join(p.argv, "\x00") + "\x00")}
		m[d+"/comm"] = &fstest.MapFile{Data: []byte(p.comm + "\n")}
		if p.fd0 != "" {
			m[d+"/fd/0"] = &fstest.MapFile{Data: []byte(p.fd0), Mode: fs.ModeSymlink}
		}
	}
	return m
}

func TestCallerName(t *testing.T) {
	long := strings.Repeat("é", 100)
	cases := []struct {
		name string
		fs   fstest.MapFS
		pid  int
		want string
	}{
		{"claude code", procFS(
			fakeProc{50, 40, []string{"bash"}, "bash", "/dev/pts/0"},
			fakeProc{40, 1, []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, "claude", ""}), 50, "Claude Code"},
		{"claude code by path only", procFS(
			fakeProc{50, 40, []string{"sh", "-c", "x"}, "sh", ""},
			fakeProc{40, 1, []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, "node", ""}), 50, "Claude Code"},
		{"codex", procFS(
			fakeProc{50, 40, []string{"sh", "-c", "x"}, "sh", ""},
			fakeProc{40, 1, []string{"codex"}, "codex", ""}), 50, "Codex"},
		{"terminal", procFS(
			fakeProc{50, 40, []string{"bash"}, "bash", "/dev/pts/1"},
			fakeProc{40, 1, []string{"xfce4-terminal"}, "xfce4-terminal", ""}), 50, "a terminal"},
		{"login shell on a tty", procFS(
			fakeProc{50, 1, []string{"-bash"}, "bash", "/dev/tty1"}), 50, "a terminal"},
		{"make", procFS(
			fakeProc{50, 40, []string{"sh"}, "sh", "pipe:[123]"},
			fakeProc{40, 1, []string{"make"}, "make", ""}), 50, "make"},
		{"python", procFS(
			fakeProc{50, 1, []string{"python3", "agent.py"}, "python3", ""}), 50, "python3"},
		{"only shells", procFS(
			fakeProc{50, 1, []string{"sh"}, "sh", ""},
			fakeProc{1, 0, []string{"sh"}, "sh", ""}), 50, "sh"},
		{"long comm cut", procFS(
			fakeProc{50, 1, nil, long, ""}), 50, strings.Repeat("é", 40)},
		{"missing pid", procFS(), 50, "a terminal"},
	}
	for _, c := range cases {
		if got := CallerName(c.fs, c.pid); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCallerNameCycle(t *testing.T) {
	// 50 -> 51 -> 50 ...: must end, and name the outermost shell.
	m := procFS(
		fakeProc{50, 51, []string{"sh"}, "sh", ""},
		fakeProc{51, 50, []string{"dash"}, "dash", ""})
	if got := CallerName(m, 50); got != "dash" {
		t.Errorf("cycle: %q", got)
	}
	// A chain longer than 32 stops at 32 steps: the agent at step 40 is not seen.
	var ps []fakeProc
	for i := 1; i <= 40; i++ {
		ps = append(ps, fakeProc{i, i + 1, []string{"sh"}, "sh", ""})
	}
	ps = append(ps, fakeProc{41, 0, []string{"codex"}, "codex", ""})
	if got := CallerName(procFS(ps...), 1); got != "sh" {
		t.Errorf("long chain: %q", got)
	}
}
