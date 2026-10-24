package server

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type commandEngine struct {
	fakeEngine
	process *engine.Process
}

func (e *commandEngine) Exec(context.Context, string, engine.ExecSpec) (*engine.Process, error) {
	return e.process, nil
}

func TestExecStreamCancellationReleasesConsumerAndStderr(t *testing.T) {
	for _, mode := range []string{"stdout", "stderr"} {
		t.Run(mode, func(t *testing.T) {
			out, outW := io.Pipe()
			defer outW.Close()
			stderr, errW := io.Pipe()
			defer errW.Close()
			entered := make(chan struct{})
			p := &engine.Process{Stdout: out, Stderr: stderr, Close: func() { out.Close(); stderr.Close() }, Wait: func() (int, error) { return 0, nil }}
			s := &Server{Eng: &commandEngine{process: p}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- s.execStream(ctx, desktops.Desktop{}, 0, []string{"query"}, func(r io.Reader) error {
					close(entered)
					if mode == "stderr" {
						return nil
					}
					_, err := io.Copy(io.Discard, r)
					return err
				})
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v", err)
				}
			case <-time.After(time.Second):
				p.Close()
				t.Fatal("execStream retained canceled attachment")
			}
		})
	}
}

func TestExecOutputRejectsOversizedQueryButAllowsScreenshot(t *testing.T) {
	for _, cmd := range [][]string{{"ps"}, {"deskyard", "screenshot", "-"}} {
		t.Run(strings.Join(cmd, " "), func(t *testing.T) {
			s := &Server{Eng: &commandEngine{process: &engine.Process{Stdout: strings.NewReader(strings.Repeat("x", (1<<20)+1)), Wait: func() (int, error) { return 0, nil }, Close: func() {}}}}
			out, err := s.execOutput(context.Background(), desktops.Desktop{}, cmd...)
			if cmd[0] == "ps" {
				if !errors.Is(err, engine.ErrOutputLimit) {
					t.Fatalf("got %v", err)
				}
			} else if err != nil || len(out) != (1<<20)+1 {
				t.Fatalf("screenshot bytes %d: %v", len(out), err)
			}
		})
	}
}
