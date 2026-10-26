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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxListOutput = 64 << 10

type Session struct {
	Name    string `json:"name"`
	Clients int    `json:"clients"`
	Command string `json:"command"`
}

// tmuxListFormat is the raw query behind List. tmux replaces tabs in format
// output, so fields are separated by '|', which session names cannot contain;
// the program name comes last and may contain anything printable.
const tmuxListFormat = "#{session_name}|#{session_group}|#{session_attached}|#{pane_current_command}"

func privateSession(name string) bool {
	return strings.HasPrefix(name, "_deskyard_") && ValidAttachment(strings.TrimPrefix(name, "_deskyard_"))
}

func validCommand(command string) bool {
	return len(command) <= 256 && utf8.ValidString(command) && !strings.ContainsFunc(command, unicode.IsControl)
}

// ParseSessions validates the owned helper output, one `name\tclients\tcommand`
// line per session; private attachment groups never become public identities.
func ParseSessions(data []byte) ([]Session, error) {
	if len(data) > MaxListOutput {
		return nil, fmt.Errorf("terminal session list too large")
	}
	sessions := []Session{}
	seen := map[string]bool{}
	for _, line := range lines(data) {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) == 3 && privateSession(fields[0]) {
			continue
		}
		var clients int
		var err error
		if len(fields) == 3 {
			clients, err = strconv.Atoi(fields[1])
		}
		if len(fields) != 3 || err != nil || clients < 0 || !ValidSession(fields[0]) || seen[fields[0]] || !validCommand(fields[2]) {
			return nil, fmt.Errorf("invalid terminal session list")
		}
		seen[fields[0]] = true
		sessions = append(sessions, Session{Name: fields[0], Clients: clients, Command: fields[2]})
	}
	sortSessions(sessions)
	return sessions, nil
}

// parseTmuxSessions folds private attachment groups into the public sessions
// they share windows with: a session's clients are those of its whole group.
// Rows it cannot use, such as a session a user renamed to "my work", are left
// out rather than hiding every other session.
func parseTmuxSessions(data []byte) []Session {
	type row struct {
		name, group, command string
		attached             int
	}
	rows := []row{}
	groupClients := map[string]int{}
	seen := map[string]bool{}
	for _, line := range lines(data) {
		fields := strings.SplitN(line, "|", 4)
		if len(fields) != 4 {
			continue
		}
		attached, err := strconv.Atoi(fields[2])
		if err != nil || attached < 0 || seen[fields[0]] || (!ValidSession(fields[0]) && !privateSession(fields[0])) {
			continue
		}
		seen[fields[0]] = true
		rows = append(rows, row{fields[0], fields[1], fields[3], attached})
		if fields[1] != "" {
			groupClients[fields[1]] += attached
		}
	}
	sessions := []Session{}
	for _, r := range rows {
		if privateSession(r.name) {
			continue
		}
		clients := r.attached
		if r.group != "" {
			clients = groupClients[r.group]
		}
		// The helper output must stay one line per session.
		command := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '?'
			}
			return r
		}, strings.ToValidUTF8(r.command, "?"))
		if len(command) > 256 {
			command = strings.ToValidUTF8(command[:256], "")
		}
		sessions = append(sessions, Session{Name: r.name, Clients: clients, Command: command})
	}
	sortSessions(sessions)
	return sessions
}

func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func sortSessions(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
}

// List does not start a tmux server or a shell. Its own deadline also bounds the
// helper process after a lost Docker exec connection.
func List(ctx context.Context, out io.Writer) error {
	sessions, err := listSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if _, err := fmt.Fprintf(out, "%s\t%d\t%s\n", session.Name, session.Clients, session.Command); err != nil {
			return err
		}
	}
	return nil
}

func listSessions(ctx context.Context) ([]Session, error) {
	out, err := queryServer(ctx, "list-sessions", "-F", tmuxListFormat)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return []Session{}, nil
	}
	return parseTmuxSessions(out), nil
}

// queryServer runs a bounded query against an existing server. A missing
// server returns nil output and no error.
func queryServer(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	socket := filepath.Join(RuntimeDir, "tmux.sock")
	if _, err := os.Stat(socket); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-u", "-S", socket}, args...)...)
	cmd.WaitDelay = time.Second
	var stdout, stderr boundedListBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// tmux leaves the socket behind when the final shell exits. This is an
		// empty list, while unrelated executable/socket failures remain errors.
		if strings.TrimSpace(stderr.String()) == "no server running on "+socket {
			return nil, nil
		}
		return nil, fmt.Errorf("query shared terminals: %w", err)
	}
	return stdout.Bytes(), nil
}

type boundedListBuffer struct{ bytes.Buffer }

func (b *boundedListBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxListOutput {
		return 0, fmt.Errorf("terminal list output too large")
	}
	return b.Buffer.Write(p)
}
