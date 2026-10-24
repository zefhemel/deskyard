package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type statsRequest struct {
	ctx     context.Context
	release chan struct{}
}
type collectorEngine struct {
	fakeEngine
	mu        sync.Mutex
	state     string
	requested chan statsRequest
}

func (e *collectorEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := alice
	c.State = e.state
	c.StartedAt = time.Unix(1, 0)
	return []engine.Container{c}, nil
}
func (e *collectorEngine) Stats(ctx context.Context, _ string) (<-chan engine.Stats, error) {
	request := statsRequest{ctx: ctx, release: make(chan struct{})}
	e.requested <- request
	<-request.release
	return nil, io.EOF
}
func (e *collectorEngine) setState(state string) { e.mu.Lock(); e.state = state; e.mu.Unlock() }

// Cancellation can finish after a new runtime worker occupies the same name.
func TestStatsOldWorkerCannotCancelReplacement(t *testing.T) {
	eng := &collectorEngine{state: "running", requested: make(chan statsRequest, 2)}
	s := &Server{Eng: eng, events: newHub()}
	s.Desktops = desktops.New(eng, desktops.Config{}, nil)
	sc := newStatsCollector(s)
	sc.lastUse = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc.reconcile(ctx)
	old := <-eng.requested
	sc.mu.Lock()
	oldWorker := sc.running["alice"]
	sc.mu.Unlock()
	eng.setState("exited")
	sc.reconcile(ctx)
	eng.setState("running")
	sc.reconcile(ctx)
	select {
	case <-eng.requested:
		t.Fatal("replacement started before canceled worker handed off")
	default:
	}
	close(old.release)
	<-oldWorker.done
	sc.reconcile(ctx)
	next := <-eng.requested
	defer close(next.release)
	select {
	case <-next.ctx.Done():
		t.Fatal("old worker cancelled replacement")
	default:
	}
	sc.mu.Lock()
	_, ok := sc.running["alice"]
	sc.mu.Unlock()
	if !ok {
		t.Fatal("old worker removed replacement")
	}
}

func TestHubOverflowRequestsAuthoritativeRefresh(t *testing.T) {
	h := newHub()
	sub := &subscriber{c: make(chan event, 1), refresh: make(chan struct{}, 1), overflow: make(chan struct{}, 1)}
	h.subs[sub] = true
	h.publish(event{kind: "stats"})
	h.publish(event{kind: "removed", data: map[string]string{"name": "alice"}})
	select {
	case <-sub.overflow:
	default:
		t.Fatal("lost removal did not force snapshot reconnect")
	}
}

// The epoch reset itself is a mutation; cancellation must guard it as well
// as individual events, or a late response can erase replacement commands.
func TestCancelledInsideResponseCannotResetTracker(t *testing.T) {
	tr := activity.NewTracker()
	tr.Inside(insideRef, activity.Event{Agent: "a", Kind: "cmd-start", ID: "work", Cmd: "sleep", T: time.Now(), Seq: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cur := insideCursor{Epoch: "old", Seq: 1}
	resp := &http.Response{Header: http.Header{"X-Activity-Epoch": []string{"new"}}, Body: http.NoBody}
	if err := consumeInside(ctx, resp, insideRef, tr, &cur); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sums := tr.Summary(insideRef.Name)
	if len(sums) != 1 || len(sums[0].Now) != 1 || cur.Epoch != "old" {
		t.Fatal("cancelled response changed current activity")
	}
}
