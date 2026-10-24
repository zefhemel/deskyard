package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type runEngine struct {
	Engine
	exec func(context.Context) (*Process, error)
}

func (e runEngine) Exec(ctx context.Context, _ string, _ ExecSpec) (*Process, error) {
	return e.exec(ctx)
}

func TestOutputCancellationReleasesBlockedStreams(t *testing.T) {
	out, outW := io.Pipe()
	defer outW.Close()
	stderr, errW := io.Pipe()
	defer errW.Close()
	entered := make(chan struct{})
	e := runEngine{exec: func(ctx context.Context) (*Process, error) {
		close(entered)
		return &Process{Stdout: out, Stderr: stderr, Close: sync.OnceFunc(func() { out.Close(); stderr.Close() }), Wait: func() (int, error) { return -1, ctx.Err() }}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, _, err := Output(ctx, e, "desktop", ExecSpec{}); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		out.Close()
		stderr.Close()
		t.Fatal("canceled exec retained blocked streams")
	}
}

func TestRunCaptureAndStream(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr string
		limit                int64
		consume              func(io.Reader) error
		code                 int
		wantErr              error
		wantOut, wantErrOut  string
	}{
		{name: "success", stdout: "hello", stderr: "note", limit: 5, wantOut: "hello", wantErrOut: "note"},
		{name: "stdout overflow", stdout: "123456", limit: 5, wantErr: ErrOutputLimit, wantOut: "12345"},
		{name: "stderr bounded and drained", stdout: "ok", stderr: strings.Repeat("e", 10000), limit: 5, wantOut: "ok", wantErrOut: "eeee"},
		{name: "nonzero exit preserved", stdout: "partial", limit: 7, code: 3, wantOut: "partial"},
		{name: "early consumer success drains rest", stdout: "more output", limit: 1, consume: func(r io.Reader) error { b := make([]byte, 1); _, err := r.Read(b); return err }},
		{name: "consumer failure", stdout: "payload", limit: 1, consume: func(io.Reader) error { return io.ErrUnexpectedEOF }, wantErr: io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := runEngine{exec: func(context.Context) (*Process, error) {
				return &Process{Stdout: strings.NewReader(tc.stdout), Stderr: strings.NewReader(tc.stderr), Close: func() {}, Wait: func() (int, error) { return tc.code, nil }}, nil
			}}
			result, err := Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{StdoutLimit: tc.limit, StderrLimit: 4, Consume: tc.consume})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v; want %v", err, tc.wantErr)
			}
			if string(result.Stdout) != tc.wantOut || string(result.Stderr) != tc.wantErrOut {
				t.Fatalf("stdout %q stderr %q", result.Stdout, result.Stderr)
			}
			if result.Code != tc.code {
				t.Fatalf("exit %d; want %d", result.Code, tc.code)
			}
		})
	}
}

func TestRunConsumerFailureCancelsWaitAndBlockedStderr(t *testing.T) {
	stderr, errW := io.Pipe()
	defer errW.Close()
	e := runEngine{exec: func(ctx context.Context) (*Process, error) {
		return &Process{Stdout: strings.NewReader("data"), Stderr: stderr, Close: func() { stderr.Close() }, Wait: func() (int, error) { <-ctx.Done(); return -1, ctx.Err() }}, nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{Consume: func(io.Reader) error { return io.ErrUnexpectedEOF }})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		stderr.Close()
		t.Fatal("consumer failure retained process I/O or wait")
	}
}

// A demultiplexer may publish stderr before stdout. Sequential drains deadlock.
func TestRunDrainsStderrBeforeStdoutArrives(t *testing.T) {
	out, outW := io.Pipe()
	stderr, errW := io.Pipe()
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer outW.Close()
		defer errW.Close()
		if _, err := errW.Write([]byte(strings.Repeat("e", 10000))); err == nil {
			outW.Write([]byte("answer"))
		}
	}()
	e := runEngine{exec: func(context.Context) (*Process, error) {
		return &Process{Stdout: out, Stderr: stderr, Close: func() { out.Close(); stderr.Close() }, Wait: func() (int, error) { return 0, nil }}, nil
	}}
	type response struct {
		result RunResult
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{StderrLimit: 4})
		done <- response{result, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || string(got.result.Stdout) != "answer" || string(got.result.Stderr) != "eeee" {
			t.Fatalf("result %+v error %v", got.result, got.err)
		}
	case <-time.After(time.Second):
		out.Close()
		stderr.Close()
		t.Fatal("stderr blocked stdout production")
	}
	<-producerDone
}

func TestRunCaptureOverflowReleasesOtherStreamAndWait(t *testing.T) {
	stderr, errW := io.Pipe()
	defer errW.Close()
	e := runEngine{exec: func(ctx context.Context) (*Process, error) {
		return &Process{Stdout: strings.NewReader("too much"), Stderr: stderr, Close: func() { stderr.Close() }, Wait: func() (int, error) { <-ctx.Done(); return -1, ctx.Err() }}, nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{StdoutLimit: 3})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrOutputLimit) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		stderr.Close()
		t.Fatal("capture overflow retained stderr or wait")
	}
}

func TestRunReportsEngineAndWaitErrors(t *testing.T) {
	for _, stage := range []string{"exec", "wait"} {
		t.Run(stage, func(t *testing.T) {
			want := errors.New(stage + " failed")
			e := runEngine{exec: func(context.Context) (*Process, error) {
				if stage == "exec" {
					return nil, want
				}
				return &Process{Stdout: strings.NewReader(""), Close: func() {}, Wait: func() (int, error) { return -1, want }}, nil
			}}
			result, err := Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{})
			if !errors.Is(err, want) || result.Code != -1 {
				t.Fatalf("result %+v error %v", result, err)
			}
		})
	}
}

func TestRunConsumerPanicReleasesAttachmentInCallingGoroutine(t *testing.T) {
	out, outW := io.Pipe()
	defer outW.Close()
	t.Cleanup(func() { out.Close() })
	defer func() {
		if got := recover(); got != "consumer panic" {
			t.Errorf("panic %v", got)
		}
		done := make(chan error, 1)
		go func() { _, err := outW.Write([]byte("unread")); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("attachment remained open: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("attachment remained open after panic")
		}

	}()
	e := runEngine{exec: func(context.Context) (*Process, error) {
		return &Process{Stdout: out, Close: func() { out.Close() }, Wait: func() (int, error) { return 0, nil }}, nil
	}}
	Run(context.Background(), e, "desktop", ExecSpec{}, RunOptions{Consume: func(io.Reader) error { panic("consumer panic") }})
	t.Fatal("consumer panic was swallowed")
}
