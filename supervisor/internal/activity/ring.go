package activity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Ring keeps the most recent events of one desktop: those from the last
// window, at most max of them. Memory only.
type Ring struct {
	window time.Duration
	max    int
	now    func() time.Time
	epoch  string

	mu     sync.Mutex
	events []Event
	seq    uint64
	wake   chan struct{} // closed and replaced on each Add
}

// NewRing makes a ring; the stream uses 15 minutes and 5,000 events.
func NewRing(window time.Duration, max int, now func() time.Time) *Ring {
	b := make([]byte, 8)
	rand.Read(b)
	return &Ring{window: window, max: max, now: now, epoch: hex.EncodeToString(b), wake: make(chan struct{})}
}

// Epoch is random and fixed for the ring's life: a reader that sees it change
// knows the sequence numbers started over.
func (r *Ring) Epoch() string { return r.epoch }

// Add stamps the event with the next Seq (from 1) and the current time, keeps
// it, and wakes the waiting readers. It returns the stamped event.
func (r *Ring) Add(e Event) Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq, e.T = r.seq, r.now()
	r.events = append(r.events, e)
	r.trim()
	close(r.wake)
	r.wake = make(chan struct{})
	return e
}

// trim drops what is too old or over the cap; the caller holds mu.
func (r *Ring) trim() {
	cut := r.now().Add(-r.window)
	i := 0
	for i < len(r.events) && r.events[i].T.Before(cut) {
		i++
	}
	i = max(i, len(r.events)-r.max)
	if i > 0 {
		r.events = append([]Event(nil), r.events[i:]...)
	}
}

// Since returns the events with Seq > after, oldest first.
func (r *Ring) Since(after uint64) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.since(after)
}

func (r *Ring) since(after uint64) []Event {
	r.trim()
	var out []Event
	for _, e := range r.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	return out
}

// Wait blocks until there is an event newer than after, and returns those;
// it returns nil when ctx ends first.
func (r *Ring) Wait(ctx context.Context, after uint64) []Event {
	for {
		r.mu.Lock()
		out, wake := r.since(after), r.wake
		r.mu.Unlock()
		if len(out) > 0 {
			return out
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return nil
		}
	}
}
