package server

import (
	"context"
	"testing"
	"time"
)

func TestTerminalCredit(t *testing.T) {
	f := newTerminalFlow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := f.reserve(ctx, 256<<10); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.reserve(ctx, 1) }()
	select {
	case <-done:
		t.Fatal("window exceeded")
	case <-time.After(20 * time.Millisecond):
	}
	if !f.ack(16384) {
		t.Fatal("valid ack rejected")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("credit not released")
	}
	if f.ack(-1) || f.ack(0) || f.ack(256<<10) {
		t.Fatal("invalid ack accepted")
	}
	if err := f.reserve(ctx, 16383); err != nil {
		t.Fatal(err)
	}
	go func() { done <- f.reserve(ctx, 1) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter stuck")
	}
}
