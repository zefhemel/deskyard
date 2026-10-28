package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRenamePreservesWindowsAndGroups(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	lease := "_deskyard_0123456789abcdef0123456789abcdef"
	if err := groupWith(context.Background(), "main", lease); err != nil {
		t.Fatal(err)
	}
	window := tmux("list-windows", "-t", "=main", "-F", "#{window_id}|#{pane_pid}")
	if err := Rename(context.Background(), "main", "build"); err != nil {
		t.Fatal(err)
	}
	if got := tmux("list-windows", "-t", "=build", "-F", "#{window_id}|#{pane_pid}"); got != window {
		t.Fatalf("window changed: %q != %q", got, window)
	}
	if got := tmux("list-windows", "-t", "="+lease, "-F", "#{window_id}|#{pane_pid}"); got != window {
		t.Fatal("lease lost its window")
	}
	tmux("new-session", "-d", "-s", "taken", "sleep", "300")
	if err := Rename(context.Background(), "build", "taken"); err == nil {
		t.Fatal("allowed duplicate")
	}
	if err := Rename(context.Background(), "build", "bad.name"); err == nil {
		t.Fatal("allowed invalid name")
	}
	if err := Rename(context.Background(), "missing", "next"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("missing: %v", err)
	}
	if err := Rename(context.Background(), "build", "build"); err != nil {
		t.Fatal(err)
	}
	if err := Kill(context.Background(), "build"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tmux("list-sessions", "-F", "#{session_name}")); got != "taken" {
		t.Fatalf("remaining sessions: %q", got)
	}
}
