package vpn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type blockedSetupEngine struct {
	runtimeEngine
	stage       string
	release     chan struct{}
	sawDeadline bool
}

func (e *blockedSetupEngine) block(ctx context.Context) error {
	_, e.sawDeadline = ctx.Deadline()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		return context.Canceled
	}
}
func (e *blockedSetupEngine) Networks(ctx context.Context, labels map[string]string) ([]engine.Network, error) {
	if e.stage == "networks" {
		return nil, e.block(ctx)
	}
	return e.runtimeEngine.Networks(ctx, labels)
}
func (e *blockedSetupEngine) Start(ctx context.Context, id string) error {
	if e.stage == "start" {
		return e.block(ctx)
	}
	return e.runtimeEngine.Start(ctx, id)
}
func TestEnsureBoundsSetupWithoutCallerDeadline(t *testing.T) {
	for _, stage := range []string{"networks", "start"} {
		t.Run(stage, func(t *testing.T) {
			m, _, c := managerFixture(t)
			e := &blockedSetupEngine{stage: stage, release: make(chan struct{})}
			m.eng = e
			m.setupTimeout = 25 * time.Millisecond
			done := make(chan error, 1)
			go func() {
				_, err := m.Ensure(context.Background(), strings.Repeat("8", 32), "desktop", c.ID, nil, false)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !e.sawDeadline {
					t.Errorf("unbounded setup stage: %v deadline=%v", err, e.sawDeadline)
				}
			case <-time.After(250 * time.Millisecond):
				close(e.release)
				<-done
				t.Error("Ensure did not bound engine setup without a caller deadline")
			}
			if stage == "start" && len(e.containers) != 0 {
				t.Fatal("deadline leaked created runtime")
			}
		})
	}
}
