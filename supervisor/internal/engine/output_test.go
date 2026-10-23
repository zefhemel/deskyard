package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type processEngine struct {
	Engine
	p *Process
}

func (e processEngine) Exec(context.Context, string, ExecSpec) (*Process, error) { return e.p, nil }
func TestOutputCancellationClosesOpenStreamsAndWait(t *testing.T) {
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	closed := make(chan struct{})
	waitExited := make(chan struct{})
	var closeOnce sync.Once
	var closes atomic.Int32
	closeProcess := func() { closeOnce.Do(func() { closes.Add(1); _ = outW.Close(); _ = errW.Close(); close(closed) }) }
	p := &Process{Stdout: outR, Stderr: errR, Close: closeProcess, Wait: func() (int, error) { <-closed; close(waitExited); return -1, context.Canceled }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, _, err := Output(ctx, processEngine{p: p}, "fixture", ExecSpec{}); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation lost: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Error("Output kept waiting for open streams after cancellation")
		closeProcess()
		<-done
	}
	select {
	case <-waitExited:
	case <-time.After(time.Second):
		t.Error("process wait goroutine did not exit")
	}
	if closes.Load() != 1 {
		t.Fatalf("attachment closes=%d", closes.Load())
	}
}
func TestOutputPreservesStreamsAndExitCode(t *testing.T) {
	p := &Process{Stdout: strings.NewReader("out"), Stderr: strings.NewReader("err"), Wait: func() (int, error) { return 7, nil }, Close: func() {}}
	out, stderr, code, err := Output(context.Background(), processEngine{p: p}, "fixture", ExecSpec{})
	if string(out) != "out" || string(stderr) != "err" || code != 7 || err != nil {
		t.Fatalf("output=%s/%s code=%d err=%v", out, stderr, code, err)
	}
}
