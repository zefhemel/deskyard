package terminal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestChoose(t *testing.T) {
	sessions := []Session{{Name: "main", Clients: 1, Command: "bash"}, {Name: "terminal-1", Command: "vim"}}
	for _, input := range []string{"", "\n", "n", "N\n", "\x1b", "\x1b[A\n", "0", "3", "x"} {
		if name, isNew := choose(sessions, input); !isNew || name != "" {
			t.Errorf("%q: %q %v", input, name, isNew)
		}
	}
	if name, isNew := choose(sessions, " 2\n"); isNew || name != "terminal-1" {
		t.Fatalf("%q %v", name, isNew)
	}
}

// endingInput ends a session the moment the user answers, as another client
// closing it while the menu is open would.
type endingInput struct {
	data   *strings.Reader
	before func()
}

func (e *endingInput) Read(p []byte) (int, error) {
	if e.before != nil {
		e.before()
		e.before = nil
	}
	return e.data.Read(p)
}

func TestPickSkipsEndedSession(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	tmux("new-session", "-d", "-s", "other", "sleep", "300")
	stdin := &endingInput{data: strings.NewReader("1\n"), before: func() { tmux("kill-session", "-t", "=main") }}
	var out bytes.Buffer
	// After the notice the menu shows again; EOF then closes the picker.
	if err := Pick(context.Background(), stdin, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Session main ended") || strings.Count(out.String(), "Shared terminal sessions:") != 2 {
		t.Fatalf("%q", out.String())
	}
	if got := strings.Fields(tmux("list-sessions", "-F", "#{session_name}")); strings.Join(got, " ") != "other" {
		t.Fatalf("picker revived the ended session: %v", got)
	}
}

func TestPickInterruptedAtPrompt(t *testing.T) {
	tmux := tmuxRuntime(t)
	tmux("new-session", "-d", "-s", "main", "sleep", "300")
	stdin, closeStdin := io.Pipe()
	defer closeStdin.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Pick(ctx, stdin, io.Discard, io.Discard) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("picker kept waiting for input after Ctrl-C")
	}
	if got := strings.Fields(tmux("list-sessions", "-F", "#{session_name}")); strings.Join(got, " ") != "main" {
		t.Fatal(got)
	}
}
