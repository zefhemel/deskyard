package server

import (
	"context"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

// A worker owns one desktop runtime. The pointer identifies this particular
// attachment, even when a cancelled attachment is still unwinding.
type desktopWorker struct {
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

func desktopGeneration(c desktops.Desktop) string { return c.ID + ":" + terminalGeneration(c) }

func startDesktopWorker(ctx context.Context, c desktops.Desktop) *desktopWorker {
	ctx, cancel := context.WithCancel(ctx)
	return &desktopWorker{generation: desktopGeneration(c), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
