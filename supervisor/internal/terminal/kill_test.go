package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tmuxRuntime points the package at a private tmux server for one test. The
// directory lives under /tmp because Unix socket paths are short on macOS.
func tmuxRuntime(t *testing.T) func(args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "dyterm")
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "tmux.conf")
	if err := os.WriteFile(conf, nil, 0600); err != nil {
		t.Fatal(err)
	}
	oldDir, oldConf := RuntimeDir, Config
	RuntimeDir, Config = filepath.Join(dir, "rt"), conf
	if err := os.Mkdir(RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", filepath.Join(RuntimeDir, "tmux.sock"), "kill-server").Run()
		RuntimeDir, Config = oldDir, oldConf
		_ = os.RemoveAll(dir)
	})
	return func(args ...string) string {
		out, err := exec.Command("tmux", append([]string{"-u", "-S", filepath.Join(RuntimeDir, "tmux.sock"), "-f", conf}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v %s", args, err, out)
		}
		return string(out)
	}
}

func sessionNames(t *testing.T) []string {
	t.Helper()
	var out bytes.Buffer
	if err := List(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	sessions, err := ParseSessions(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	return names
}

func TestKillEndsGroupedAttachments(t *testing.T) {
	tmux := tmuxRuntime(t)
	lease := attachmentSession("0123456789abcdef0123456789abcdef")
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	tmux("new-session", "-d", "-s", "other", "sleep", "300")
	tmux("new-session", "-d", "-s", lease, "-t", "main")
	tmux("new-window", "-d", "-t", "=main:", "sleep", "301")
	pane := strings.TrimSpace(tmux("display-message", "-p", "-t", "=main:", "#{pane_pid}"))

	if err := Kill(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(tmux("list-sessions", "-F", "#{session_name}"))
	if strings.Join(got, " ") != "other" {
		t.Fatalf("remaining sessions %v", got)
	}
	// No attachment group keeps the shared window, so its process ends.
	if err := exec.Command("kill", "-0", pane).Run(); err == nil {
		deadline := time.Now().Add(5 * time.Second)
		for exec.Command("kill", "-0", pane).Run() == nil {
			if time.Now().After(deadline) {
				t.Fatal("session process survived")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err := Kill(context.Background(), "main"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("wanted ErrNoSession, got %v", err)
	}
	if err := Kill(context.Background(), "a.b"); err == nil || errors.Is(err, ErrNoSession) {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestKillCanceledIsNotMissing(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Kill(ctx, "main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted cancellation, got %v", err)
	}
	tmux("has-session", "-t", "=main")
}

func TestKillWithoutServer(t *testing.T) {
	tmuxRuntime(t)
	if err := Kill(context.Background(), "main"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("wanted ErrNoSession, got %v", err)
	}
}

func TestLeaseJoinOnlyMissingSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "other", "sleep", "300")
	var out bytes.Buffer
	// An open stdin: EOF would end the lease before it checks the session.
	stdin, closeStdin := io.Pipe()
	defer closeStdin.Close()
	err := Lease(context.Background(), "main", "0123456789abcdef0123456789abcdef", true, stdin, &out)
	if !errors.Is(err, ErrNoSession) || out.String() != "ended\n" {
		t.Fatalf("%q %v", out.String(), err)
	}
	if got := strings.Fields(tmux("list-sessions", "-F", "#{session_name}")); strings.Join(got, " ") != "other" {
		t.Fatalf("join-only lease created sessions: %v", got)
	}
}

// new-session -t prefix-matches: terminal-1 must never join terminal-10.
func TestLeaseJoinOnlyPrefixSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "terminal-10", "sleep", "300")
	stdin, closeStdin := io.Pipe()
	defer closeStdin.Close()
	var out bytes.Buffer
	err := Lease(context.Background(), "terminal-1", "0123456789abcdef0123456789abcdef", true, stdin, &out)
	if !errors.Is(err, ErrNoSession) || out.String() != "ended\n" {
		t.Fatalf("%q %v", out.String(), err)
	}
	if got := tmux("list-sessions", "-F", "#{session_name}|#{session_group}"); got != "terminal-10|\n" {
		t.Fatalf("join-only lease touched sessions: %q", got)
	}
}

// Without a base, new-session -t would start a standalone shell in a group
// named after the target; groupWith must leave nothing behind.
func TestGroupWithMissingBase(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "other", "sleep", "300")
	if err := groupWith(context.Background(), "main", attachmentSession("0123456789abcdef0123456789abcdef")); !errors.Is(err, ErrNoSession) {
		t.Fatal(err)
	}
	if got := tmux("list-sessions", "-F", "#{session_name}|#{session_group}"); got != "other|\n" {
		t.Fatalf("%q", got)
	}
}

func TestLeaseGroupsWithExactSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "terminal-10", "sleep", "300")
	lease := attachmentSession("0123456789abcdef0123456789abcdef")
	stdin, closeStdin := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Lease(context.Background(), "terminal-1", "0123456789abcdef0123456789abcdef", false, stdin, out)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "prepared\n") {
		if time.Now().After(deadline) {
			t.Fatalf("lease not prepared: %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	format := "#{session_group}|#{window_id}"
	got := tmux("display-message", "-p", "-t", "="+lease+":", format)
	if want := tmux("display-message", "-p", "-t", "=terminal-1:", format); got != want || strings.HasPrefix(got, "|") {
		t.Fatalf("lease %q, wanted the group of terminal-1 %q", got, want)
	}
	closeStdin.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLeaseJoinOnlyExistingSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	stdin, closeStdin := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Lease(context.Background(), "main", "0123456789abcdef0123456789abcdef", true, stdin, out)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "prepared\n") {
		if time.Now().After(deadline) {
			t.Fatalf("lease not prepared: %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	closeStdin.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got := strings.Fields(tmux("list-sessions", "-F", "#{session_name}")); strings.Join(got, " ") != "main" {
		t.Fatalf("lease group not cleaned up: %v", got)
	}
}

func TestCreateNamesNextFree(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "terminal-1", "sleep", "300")
	tmux("new-session", "-d", "-s", "terminal-3", "sleep", "300")
	for _, want := range []string{"terminal-2", "terminal-4"} {
		var out bytes.Buffer
		if err := Create(context.Background(), &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != want+"\n" {
			t.Fatalf("created %q, wanted %s", out.String(), want)
		}
	}
	if got := strings.Join(sessionNames(t), " "); got != "terminal-1 terminal-2 terminal-3 terminal-4" {
		t.Fatal(got)
	}
	// Created sessions are detached and run a login shell.
	var out bytes.Buffer
	if err := List(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "terminal-2\t0\t") {
		t.Fatalf("%q", out.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }
