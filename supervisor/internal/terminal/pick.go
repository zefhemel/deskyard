package terminal

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Pick is the desktop Terminal launcher: with sessions running it offers to
// join one, otherwise (and by default) it opens a new session like New.
func Pick(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	// Read byte by byte: a buffered reader could swallow input meant for tmux.
	input := bufio.NewReaderSize(oneByteReader{stdin}, 16)
	for {
		sessions, err := listSessions(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			return New(ctx, stdin, stdout, stderr)
		}
		fmt.Fprintln(stdout, "Shared terminal sessions:")
		for i, s := range sessions {
			fmt.Fprintf(stdout, "  %d) %s — %d attached — %s\n", i+1, s.Name, s.Clients, s.Command)
		}
		fmt.Fprint(stdout, "  n) New session\n\nChoose a number, or Enter for a new session [n]: ")
		line, err := readLine(ctx, input)
		if ctx.Err() != nil {
			// Ctrl-C at the prompt closes the picker.
			return nil
		}
		if err != nil && line == "" {
			// The window closed before a choice.
			if err == io.EOF {
				return nil
			}
			return err
		}
		name, isNew := choose(sessions, line)
		if isNew {
			return New(ctx, stdin, stdout, stderr)
		}
		// Joining must not revive a session ended while the menu was open.
		if tmuxCommand(ctx, "has-session", "-t", "="+name).Run() != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(stdout, "\nSession %s ended\n\n", name)
			continue
		}
		return attachAt(ctx, name, "/workspace", true, stdin, stdout, stderr)
	}
}

// readLine returns early when ctx ends. The terminal is in cooked mode, so a
// line arrives only on Enter: Esc alone waits for Enter too, then means new.
// After a cancellation the abandoned read is never resumed.
func readLine(ctx context.Context, input *bufio.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := input.ReadString('\n')
		done <- result{line, err}
	}()
	select {
	case r := <-done:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// choose maps one line of input to a session. Anything but a listed number
// (Enter, n, Esc or a typo) means a new session.
func choose(sessions []Session, input string) (session string, isNew bool) {
	n, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || n < 1 || n > len(sessions) {
		return "", true
	}
	return sessions[n-1].Name, false
}

type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return o.r.Read(p)
}
