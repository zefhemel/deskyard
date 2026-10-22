package activity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
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

	mu   sync.Mutex
	buf  []Event // circular: n events from head
	head int
	n    int
	seq  uint64
	wake chan struct{} // closed and replaced on each Add
}

// NewRing makes a ring; the stream uses 15 minutes and 5,000 events.
func NewRing(window time.Duration, max int, now func() time.Time) *Ring {
	b := make([]byte, 8)
	rand.Read(b)
	return &Ring{window: window, max: max, now: now, epoch: hex.EncodeToString(b), wake: make(chan struct{}), buf: make([]Event, max)}
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
	r.trim()
	if r.n == len(r.buf) { // full: overwrite the oldest
		r.buf[r.head] = Event{}
		r.head = (r.head + 1) % len(r.buf)
		r.n--
	}
	r.buf[(r.head+r.n)%len(r.buf)] = e
	r.n++
	close(r.wake)
	r.wake = make(chan struct{})
	return e
}

// at is the i-th oldest event; the caller holds mu.
func (r *Ring) at(i int) *Event { return &r.buf[(r.head+i)%len(r.buf)] }

// trim drops what is too old; the caller holds mu.
func (r *Ring) trim() {
	cut := r.now().Add(-r.window)
	for r.n > 0 && r.at(0).T.Before(cut) {
		*r.at(0) = Event{}
		r.head = (r.head + 1) % len(r.buf)
		r.n--
	}
}

// Since returns the events with Seq > after, oldest first.
func (r *Ring) Since(after uint64) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.since(after)
}

// since finds the first newer event by binary search (Seq rises along the
// ring) and copies from there.
func (r *Ring) since(after uint64) []Event {
	r.trim()
	i := sort.Search(r.n, func(i int) bool { return r.at(i).Seq > after })
	if i == r.n {
		return nil
	}
	out := make([]Event, 0, r.n-i)
	for ; i < r.n; i++ {
		out = append(out, *r.at(i))
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
