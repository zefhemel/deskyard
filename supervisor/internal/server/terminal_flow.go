package server

import (
	"context"
	"sync"
)

const terminalWindow = 256 << 10

// terminalFlow bounds output awaiting the renderer's acknowledgement.
type terminalFlow struct {
	mu          sync.Mutex
	outstanding int
	changed     chan struct{}
}

func newTerminalFlow() *terminalFlow { return &terminalFlow{changed: make(chan struct{})} }
func (f *terminalFlow) reserve(ctx context.Context, n int) error {
	for {
		f.mu.Lock()
		if ctx.Err() != nil {
			f.mu.Unlock()
			return ctx.Err()
		}
		if f.outstanding+n <= terminalWindow {
			f.outstanding += n
			f.mu.Unlock()
			return nil
		}
		changed := f.changed
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
func (f *terminalFlow) ack(n int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n <= 0 || n > f.outstanding {
		return false
	}
	f.outstanding -= n
	close(f.changed)
	f.changed = make(chan struct{})
	return true
}
