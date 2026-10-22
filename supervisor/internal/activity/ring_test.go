package activity

import (
	"context"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestRing() (*Ring, *clock) {
	c := &clock{time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	return NewRing(15*time.Minute, 5000, c.now), c
}

func seqs(es []Event) []uint64 {
	var out []uint64
	for _, e := range es {
		out = append(out, e.Seq)
	}
	return out
}

func TestRingSeqAndSince(t *testing.T) {
	r, c := newTestRing()
	for i := 1; i <= 3; i++ {
		if e := r.Add(Event{Agent: "a"}); e.Seq != uint64(i) || !e.T.Equal(c.t) {
			t.Fatalf("add %d: %+v", i, e)
		}
	}
	if got := seqs(r.Since(1)); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("Since(1) = %v", got)
	}
	if r.Epoch() == "" || r.Epoch() != r.Epoch() {
		t.Fatal("epoch")
	}
}

func TestRingWindow(t *testing.T) {
	r, c := newTestRing()
	r.Add(Event{Agent: "old"})
	c.t = c.t.Add(15*time.Minute + time.Second)
	if got := r.Since(0); len(got) != 0 {
		t.Fatalf("old event kept: %v", got)
	}
	r.Add(Event{Agent: "new"})
	if got := seqs(r.Since(0)); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Since(0) = %v", got)
	}
}

func TestRingCap(t *testing.T) {
	r, _ := newTestRing()
	for range 5001 {
		r.Add(Event{Agent: "a"})
	}
	got := r.Since(0)
	if len(got) != 5000 || got[0].Seq != 2 || got[4999].Seq != 5001 {
		t.Fatalf("len %d first %d", len(got), got[0].Seq)
	}
}

func TestRingWait(t *testing.T) {
	r, _ := newTestRing()
	done := make(chan []Event)
	go func() { done <- r.Wait(context.Background(), 0) }()
	time.Sleep(20 * time.Millisecond)
	r.Add(Event{Agent: "a"})
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Seq != 1 {
			t.Fatalf("got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if got := r.Wait(ctx, 1); got != nil {
		t.Fatalf("cancelled Wait = %v", got)
	}
}
