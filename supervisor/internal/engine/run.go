package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrOutputLimit means stdout exceeded the capture limit. Partial capture must
// not be treated as a complete response.
var ErrOutputLimit = errors.New("exec output exceeds capture limit")

// RunOptions configures bounded capture or a streaming stdout consumer. Limits
// at or below zero use 32 MiB stdout and 64 KiB diagnostic stderr. Stderr is
// truncated and continuously drained. Consume replaces stdout capture and runs
// on the calling goroutine. After it succeeds the runner drains unread stdout.
// It must return when its reader closes; external writes should use the same
// cancellation scope.
type RunOptions struct {
	StdoutLimit int64
	StderrLimit int64
	Consume     func(io.Reader) error
}

type RunResult struct {
	Stdout, Stderr []byte
	Code           int
}

// Run owns an exec attachment, concurrent draining, capture and exit handling.
// Cancellation and consumer/read failure close I/O and cancel engine waiting;
// this releases the attachment without promising to terminate the command.
// Nonzero exit codes are returned as data for callers to interpret.
func Run(ctx context.Context, e Engine, id string, spec ExecSpec, opts RunOptions) (RunResult, error) {
	result := RunResult{Code: -1}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := e.Exec(runCtx, id, spec)
	if err != nil {
		return result, err
	}
	closeIO := sync.OnceFunc(p.Close)
	defer closeIO()
	stop := context.AfterFunc(runCtx, closeIO)
	defer stop()
	fail := func(err error) {
		if err != nil {
			cancel()
			closeIO()
		}
	}
	if opts.StdoutLimit <= 0 {
		opts.StdoutLimit = 32 << 20
	}
	if opts.StderrLimit <= 0 {
		opts.StderrLimit = 64 << 10
	}
	type readResult struct {
		data []byte
		err  error
	}
	errDone := make(chan readResult, 1)
	go func() {
		var out readResult
		if p.Stderr != nil {
			out.data, out.err = io.ReadAll(io.LimitReader(p.Stderr, opts.StderrLimit))
			if out.err == nil {
				_, out.err = io.Copy(io.Discard, p.Stderr)
			}
		}
		fail(out.err)
		errDone <- out
	}()
	var out readResult
	if opts.Consume != nil {
		out.err = opts.Consume(p.Stdout)
		if out.err == nil {
			_, out.err = io.Copy(io.Discard, p.Stdout)
		}
	} else {
		out.data, out.err = io.ReadAll(io.LimitReader(p.Stdout, opts.StdoutLimit+1))
		if int64(len(out.data)) > opts.StdoutLimit {
			out.data = out.data[:opts.StdoutLimit]
			out.err = fmt.Errorf("stdout: %w", ErrOutputLimit)
		}
	}
	fail(out.err)
	diagnostics := <-errDone
	result.Stdout, result.Stderr = out.data, diagnostics.data
	result.Code, err = p.Wait()
	return result, errors.Join(ctx.Err(), out.err, diagnostics.err, err)
}
