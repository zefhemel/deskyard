package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

type activityEngine struct{ fakeEngine }

func (activityEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	c := alice
	c.State = "running"
	c.LocalPort = 1
	return []engine.Container{c}, nil
}
func (activityEngine) Inspect(context.Context, string) (engine.Container, error) {
	c := alice
	c.State = "running"
	c.LocalPort = 1
	return c, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type delayedActivityEOF struct {
	io.Reader
	waiting, release chan struct{}
	once             sync.Once
}

func (b *delayedActivityEOF) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.once.Do(func() { close(b.waiting) })
		<-b.release
	}
	return n, err
}
func (b *delayedActivityEOF) Close() error { return nil }

func TestActivityWorkerHandsOffCursorBeforeReattach(t *testing.T) {
	eng := activityEngine{}
	s := &Server{Eng: eng, events: newHub(), activity: activity.NewTracker()}
	s.Environments = environments.New(eng, environments.Config{DataDir: t.TempDir()}, nil)
	defineEnvironments(t, s)
	ir := newInsideReader(s)
	s.inside = ir
	ir.lastUse = time.Now()
	body := &delayedActivityEOF{Reader: strings.NewReader(insideLine(1, `"agent":"a","kind":"screen","screen":{"action":"click","pos":[1,2]}`)), waiting: make(chan struct{}), release: make(chan struct{})}
	queries := make(chan string, 2)
	calls := 0
	ir.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		queries <- r.URL.Query().Get("after")
		calls++
		var source io.ReadCloser = http.NoBody
		if calls == 1 {
			source = body
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Activity-Epoch": []string{"epoch"}}, Body: source}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ir.reconcile(ctx)
	if after := <-queries; after != "0" {
		t.Fatal(after)
	}
	<-body.waiting
	ir.mu.Lock()
	old := ir.running["alice"]
	ir.mu.Unlock()
	ir.lastUse = time.Time{}
	ir.reconcile(ctx)
	ir.lastUse = time.Now()
	ir.reconcile(ctx)
	select {
	case <-queries:
		t.Fatal("new stream started before cursor handoff")
	default:
	}
	close(body.release)
	<-old.done
	ir.reconcile(ctx)
	if after := <-queries; after != "1" {
		t.Fatal("reattachment replayed already-consumed history", after)
	}
}
