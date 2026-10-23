package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// These guards must run before any host helper is started.
func TestReclaimMemoryGuards(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("id-alice", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		client *http.Client
		origin string
		want   int
	}{
		{"regular account", anna, ts.URL, http.StatusForbidden},
		{"cross origin", admin, "https://elsewhere.example", http.StatusForbidden},
		{"stopped desktop", admin, ts.URL, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := status(t, tc.client, "POST", ts.URL+"/api/desktops/alice/reclaim-memory", tc.origin); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

type reclaimEngine struct {
	calls atomic.Int32
	fakeEngine
	err      error
	probeErr error
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (e *reclaimEngine) ProbeMemoryReclaim(context.Context) error { return e.probeErr }

func TestMemoryReclaimBootLogs(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			var logs bytes.Buffer
			e := runningReclaimEngine()
			if !enabled {
				e.probeErr = errors.New("the Linux host has no swap")
			}
			s := &Server{Eng: e, Log: slog.New(slog.NewJSONHandler(&logs, nil))}
			s.detectMemoryReclaim(context.Background())
			var record struct {
				Enabled  bool
				Reason   string
				Verified []string
			}
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record.Enabled != enabled {
				t.Fatalf("wrong log: %s", logs.String())
			}
			if enabled && len(record.Verified) == 0 {
				t.Fatal("startup log omitted verified requirements")
			}
			if !enabled && record.Reason != "the Linux host has no swap" {
				t.Fatal("startup log omitted reason", logs.String())
			}
		})
	}
}

func TestMemoryReclaimBootStatusGatesAPI(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			e := runningReclaimEngine()
			if !enabled {
				e.probeErr = errors.New("the Linux host has no swap")
			}
			_, ts := newTestServerWith(t, e)
			admin := login(t, ts, "admin")
			res, err := admin.Get(ts.URL + "/api/host")
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var host struct {
				MemoryReclaim *struct {
					Enabled bool   `json:"enabled"`
					Reason  string `json:"reason"`
				} `json:"memoryReclaim"`
			}
			if err = json.NewDecoder(res.Body).Decode(&host); err != nil {
				t.Fatal(err)
			}
			if host.MemoryReclaim == nil || host.MemoryReclaim.Enabled != enabled {
				t.Fatalf("host status %+v", host.MemoryReclaim)
			}
			if !enabled {
				if !strings.Contains(host.MemoryReclaim.Reason, "no swap") {
					t.Fatal(host.MemoryReclaim.Reason)
				}
				if got := status(t, admin, "POST", ts.URL+"/api/desktops/bob/reclaim-memory", ts.URL); got != 501 {
					t.Fatalf("disabled endpoint: %d", got)
				}
			}
		})
	}
}

func (e *reclaimEngine) ReclaimMemory(ctx context.Context, id string) (engine.MemoryReclaim, error) {
	e.calls.Add(1)
	if id != "bob-container" {
		return engine.MemoryReclaim{}, errors.New("wrong target")
	}
	if e.entered != nil {
		e.once.Do(func() { close(e.entered) })
		select {
		case <-e.release:
		case <-ctx.Done():
			return engine.MemoryReclaim{}, ctx.Err()
		}
	}
	return engine.MemoryReclaim{RequestedBytes: 128 << 20, BeforeBytes: 300 << 20, AfterBytes: 200 << 20, Partial: true}, e.err
}

func runningReclaimEngine() *reclaimEngine {
	return &reclaimEngine{fakeEngine: fakeEngine{others: []engine.Container{{
		ID: "bob-container", Name: "deskyard-bob", State: "running",
		Labels: map[string]string{"deskyard.managed": "true", "deskyard.name": "bob", "deskyard.id": "id-bob"},
	}}}}
}

func TestReclaimMemoryResultAndFailures(t *testing.T) {
	e := runningReclaimEngine()
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	post := func() *http.Response {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/api/desktops/bob/reclaim-memory", nil)
		req.Header.Set("Origin", ts.URL)
		res, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post()
	defer res.Body.Close()
	var got engine.MemoryReclaim
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || got.BeforeBytes != 300<<20 || got.AfterBytes != 200<<20 || !got.Partial {
		t.Fatalf("status %d result %+v", res.StatusCode, got)
	}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{engine.ErrReclaimUnsupported, 501}, {errors.New("helper failed"), 502},
	} {
		e.err = tc.err
		r := post()
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Fatalf("got %d want %d", r.StatusCode, tc.want)
		}
		if len(s.Desktops.Busy()) != 0 {
			t.Fatal("failure left desktop busy")
		}
		if !s.memoryReclaim.Enabled {
			t.Fatal("a per-desktop failure disabled the host capability")
		}
	}
}

func TestReclaimMemoryExcludesConcurrentOperations(t *testing.T) {
	e := runningReclaimEngine()
	e.entered = make(chan struct{})
	e.release = make(chan struct{})
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	defer close(e.release)
	finished := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("POST", ts.URL+"/api/desktops/bob/reclaim-memory", nil)
		req.Header.Set("Origin", ts.URL)
		res, err := admin.Do(req)
		if err != nil {
			finished <- 0
			return
		}
		res.Body.Close()
		finished <- res.StatusCode
	}()
	select {
	case <-e.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reclaim did not start")
	}
	for _, action := range []string{"reclaim-memory", "stop", "restart"} {
		if got := status(t, admin, "POST", ts.URL+"/api/desktops/bob/"+action, ts.URL); got != 409 {
			t.Errorf("%s: %d", action, got)
		}
	}
	if c, err := s.Desktops.Get(context.Background(), "bob"); err != nil || c.State != "running" {
		t.Fatal("reclaim changed the running state", c.State, err)
	}
	// release before test-server cleanup waits for the handler.
	e.release <- struct{}{}
	if got := <-finished; got != 200 {
		t.Fatalf("first request: %d", got)
	}
}

type powerStateEngine struct {
	*reclaimEngine
	running atomic.Bool
}

func (e *powerStateEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.fakeEngine.Inspect(ctx, id)
	if c.ID == "bob-container" && !e.running.Load() {
		c.State = "exited"
	}
	return c, err
}
func (e *powerStateEngine) Start(context.Context, string) error { e.running.Store(true); return nil }
func (e *powerStateEngine) Stop(context.Context, string, time.Duration) error {
	e.running.Store(false)
	return nil
}

func TestPowerPublishesFinalStateWithReservation(t *testing.T) {
	e := &powerStateEngine{reclaimEngine: runningReclaimEngine()}
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	events := subscribe(t, s)
	for _, tc := range []struct{ action, state string }{{"start", "running"}, {"stop", "stopped"}} {
		if got := status(t, admin, "POST", ts.URL+"/api/desktops/bob/"+tc.action, ts.URL); got != 204 {
			t.Fatalf("%s: %d", tc.action, got)
		}
		select {
		case ev := <-events:
			c, ok := ev.data.(desktops.Desktop)
			if !ok || c.State != tc.state {
				t.Fatalf("%s published %+v", tc.action, ev.data)
			}
		case <-time.After(time.Second):
			t.Fatal("missing final state event")
		}
	}
}

type blockingPowerEngine struct {
	*reclaimEngine
	powerEntered, powerRelease chan struct{}
}

func (e *blockingPowerEngine) Stop(ctx context.Context, _ string, _ time.Duration) error {
	close(e.powerEntered)
	select {
	case <-e.powerRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *blockingPowerEngine) Restart(ctx context.Context, id string, timeout time.Duration) error {
	return e.Stop(ctx, id, timeout)
}

func TestPowerInFlightExcludesReclaimMemory(t *testing.T) {
	for _, action := range []string{"stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			e := &blockingPowerEngine{reclaimEngine: runningReclaimEngine(), powerEntered: make(chan struct{}), powerRelease: make(chan struct{})}
			_, ts := newTestServerWith(t, e)
			admin := login(t, ts, "admin")
			defer close(e.powerRelease)
			finished := make(chan int, 1)
			go func() {
				req, _ := http.NewRequest("POST", ts.URL+"/api/desktops/bob/"+action, nil)
				req.Header.Set("Origin", ts.URL)
				res, err := admin.Do(req)
				if err != nil {
					finished <- 0
					return
				}
				res.Body.Close()
				finished <- res.StatusCode
			}()
			select {
			case <-e.powerEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("power operation did not start")
			}
			if got := status(t, admin, "POST", ts.URL+"/api/desktops/bob/reclaim-memory", ts.URL); got != 409 {
				t.Errorf("reclaim while %s in flight: %d", action, got)
			}
			e.powerRelease <- struct{}{}
			if got := <-finished; got != 204 {
				t.Fatalf("power: %d", got)
			}
		})
	}
}
