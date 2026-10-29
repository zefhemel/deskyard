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

// Renaming leaves the old tmux group name reserved. Reusing it used to attach
// a new browser tab to the renamed shell instead of its own window.
func TestRenameReservesAttachmentGroupName(t *testing.T) {
	tmux := tmuxRuntime(t)
	ctx := context.Background()
	tmux("new-session", "-d", "-s", "terminal-1", "sleep", "300")
	first := attachmentSession("0123456789abcdef0123456789abcdef")
	if err := groupWith(ctx, "terminal-1", first); err != nil {
		t.Fatal(err)
	}
	if err := Rename(ctx, "terminal-1", "codex"); err != nil {
		t.Fatal(err)
	}
	name, err := createNext(ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if name != "terminal-2" {
		t.Fatalf("reused reserved group name: %q", name)
	}
	second := attachmentSession("fedcba9876543210fedcba9876543210")
	if err := groupWith(ctx, name, second); err != nil {
		t.Fatal(err)
	}
	window := tmux("list-windows", "-t", "=codex", "-F", "#{window_id}")
	if got := tmux("list-windows", "-t", "="+second, "-F", "#{window_id}"); got == window {
		t.Fatal("new tab joined renamed session")
	}
	if err := Rename(ctx, name, "terminal-1"); err == nil {
		t.Fatal("allowed rename to reserved group name")
	}
	if _, err := ensureSession(ctx, "terminal-1"); err == nil {
		t.Fatal("allowed explicit creation using reserved group name")
	}
	tmux("new-session", "-d", "-s", "terminal-1", "sleep", "300")
	if err := groupWith(ctx, "terminal-1", attachmentSession("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")); err == nil {
		t.Fatal("attached colliding legacy session")
	}
	if got := tmux("display-message", "-p", "-t", "=terminal-1:", "#{session_group}"); strings.TrimSpace(got) != "" {
		t.Fatalf("rejected attachment changed base group: %q", got)
	}
	if err := Kill(ctx, name); err != nil {
		t.Fatal(err)
	}
	if got := tmux("list-windows", "-t", "=codex", "-F", "#{window_id}"); got != window {
		t.Fatal("ending new session affected renamed shell")
	}
}
