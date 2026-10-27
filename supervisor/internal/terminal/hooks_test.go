package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The image's tmux.conf reports session changes with deskyard terminal-changed.
func TestImageConfigReportsSessionChanges(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	conf, err := filepath.Abs("../../../images/common/rootfs/etc/deskyard/tmux.conf")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "dyhook")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "tmux.sock")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	reports := filepath.Join(dir, "reports")
	fake := "#!/bin/sh\necho \"$*\" >> " + reports + "\n"
	if err := os.WriteFile(filepath.Join(dir, "deskyard"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	tmux := func(args ...string) {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-u", "-S", socket, "-f", conf}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
	}
	count := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, _ := os.ReadFile(reports)
			lines := strings.Fields(string(data))
			if len(lines) >= want {
				for _, l := range lines {
					if l != "terminal-changed" {
						t.Fatalf("hook ran %q", l)
					}
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d reports, want %d", len(lines), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	tmux("new-session", "-d", "-s", "keep", "sleep", "300")
	count(1)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	count(2)
	tmux("kill-session", "-t", "=main")
	count(3)
	time.Sleep(200 * time.Millisecond)
	if data, _ := os.ReadFile(reports); len(strings.Fields(string(data))) != 3 {
		t.Fatalf("reports %q", data)
	}
}
