package server

import (
	"context"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
)

// A worker owns one desktop runtime. The pointer identifies this particular
// attachment, even when a cancelled attachment is still unwinding.
type computerWorker struct {
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

func computerGeneration(c computers.Computer) string {
	return c.ID + ":" + terminalGeneration(c)
}

func startComputerWorker(ctx context.Context, c computers.Computer) *computerWorker {
	ctx, cancel := context.WithCancel(ctx)
	return &computerWorker{generation: computerGeneration(c), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
