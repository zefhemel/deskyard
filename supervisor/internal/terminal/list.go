package terminal

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const MaxListOutput = 64 << 10

type Session struct {
	Name string `json:"name"`
}

// ParseSessions validates the owned helper output; private attachment groups
// never become public identities, including when their base session is detached.
func ParseSessions(data []byte) ([]Session, error) {
	if len(data) > MaxListOutput {
		return nil, fmt.Errorf("terminal session list too large")
	}
	sessions := []Session{}
	seen := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if name == "" && len(data) == 0 {
			continue
		}
		if strings.HasPrefix(name, "_deskyard_") && ValidAttachment(strings.TrimPrefix(name, "_deskyard_")) {
			continue
		}
		if !ValidSession(name) || seen[name] {
			return nil, fmt.Errorf("invalid terminal session list")
		}
		seen[name] = true
		sessions = append(sessions, Session{Name: name})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	return sessions, nil
}

// List does not start a tmux server or a shell. Its own deadline also bounds the
// helper process after a lost Docker exec connection.
func List(ctx context.Context, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	socket := filepath.Join(RuntimeDir, "tmux.sock")
	if _, err := os.Stat(socket); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "tmux", "-u", "-S", socket, "list-sessions", "-F", "#{session_name}")
	cmd.WaitDelay = time.Second
	var stdout, stderr boundedListBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		// tmux leaves the socket behind when the final shell exits. This is an
		// empty list, while unrelated executable/socket failures remain errors.
		if strings.TrimSpace(stderr.String()) == "no server running on "+socket {
			return nil
		}
		return fmt.Errorf("list shared terminals: %w", err)
	}
	sessions, err := ParseSessions(stdout.Bytes())
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if _, err := fmt.Fprintln(out, session.Name); err != nil {
			return err
		}
	}
	return nil
}

type boundedListBuffer struct{ bytes.Buffer }

func (b *boundedListBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxListOutput {
		return 0, fmt.Errorf("terminal list output too large")
	}
	return b.Buffer.Write(p)
}
