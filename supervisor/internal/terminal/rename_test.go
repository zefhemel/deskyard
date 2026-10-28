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

// tmux names a group after its base session and keeps that name after a
// rename. Reusing the old name used to attach a new browser tab to the
// renamed shell instead of its own window; attachment groups now have private
// names, so every public name stays free.
func TestRenamedSessionNameIsFreeForNewSessions(t *testing.T) {
	tmux := tmuxRuntime(t)
	ctx := context.Background()
	tmux("new-session", "-d", "-s", "terminal-1", "sleep", "300")
	first := attachmentSession("0123456789abcdef0123456789abcdef")
	if err := groupWith(ctx, "terminal-1", first); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tmux("display-message", "-p", "-t", "=terminal-1:", "#{session_group}")); !strings.HasPrefix(got, "_deskyard_group_") {
		t.Fatalf("group named %q", got)
	}
	if err := Rename(ctx, "terminal-1", "codex"); err != nil {
		t.Fatal(err)
	}
	window := tmux("list-windows", "-t", "=codex", "-F", "#{window_id}")
	name, err := createNext(ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if name != "terminal-1" {
		t.Fatalf("new session %q", name)
	}
	second := attachmentSession("fedcba9876543210fedcba9876543210")
	if err := groupWith(ctx, name, second); err != nil {
		t.Fatal(err)
	}
	if got := tmux("list-windows", "-t", "="+second, "-F", "#{window_id}"); got == window {
		t.Fatal("new tab joined renamed session")
	}
	if err := Kill(ctx, name); err != nil {
		t.Fatal(err)
	}
	if err := Rename(ctx, "codex", "terminal-1"); err != nil {
		t.Fatal(err)
	}
	if got := tmux("list-windows", "-t", "=terminal-1", "-F", "#{window_id}"); got != window {
		t.Fatal("ending new session affected renamed shell")
	}
}

// A group tmux named after a public session, such as one made by plain tmux
// commands, can't capture a later session of that name.
func TestAttachIgnoresGroupNamedAfterSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	ctx := context.Background()
	tmux("new-session", "-d", "-s", "work", "sleep", "300")
	tmux("new-session", "-d", "-s", "side", "-t", "=work")
	tmux("rename-session", "-t", "=work", "old")
	window := tmux("list-windows", "-t", "=old", "-F", "#{window_id}")
	tmux("new-session", "-d", "-s", "work", "sleep", "300")
	id := attachmentSession("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := groupWith(ctx, "work", id); err != nil {
		t.Fatal(err)
	}
	if got := tmux("list-windows", "-t", "="+id, "-F", "#{window_id}"); got == window {
		t.Fatal("attachment joined the old group")
	}
	if got := tmux("list-windows", "-t", "=old", "-F", "#{window_id}"); got != window {
		t.Fatal("old group changed")
	}
	if got := strings.TrimSpace(tmux("display-message", "-p", "-t", "=work:", "#{session_name}")); got != "work" {
		t.Fatalf("base renamed to %q", got)
	}
}
