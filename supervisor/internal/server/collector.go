package server

import (
	"context"

	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// A worker owns one desktop runtime. The pointer identifies this particular
// attachment, even when a cancelled attachment is still unwinding.
type environmentWorker struct {
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

func environmentGeneration(c environments.Environment) string {
	return c.ID + ":" + terminalGeneration(c)
}

func startEnvironmentWorker(ctx context.Context, c environments.Environment) *environmentWorker {
	ctx, cancel := context.WithCancel(ctx)
	return &environmentWorker{generation: environmentGeneration(c), ctx: ctx, cancel: cancel, done: make(chan struct{})}
}
