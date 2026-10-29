package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type snapshotEngine struct {
	fakeEngine
	list func() ([]engine.Container, error)
}

func (e *snapshotEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	return e.list()
}

func TestEventsQueuedLifecycleCannotOverwriteSnapshot(t *testing.T) {
	eng := &snapshotEngine{list: func() ([]engine.Container, error) { return []engine.Container{alice}, nil }}
	s, ts := newTestServerWith(t, eng)
	client := login(t, ts, "admin")
	eng.list = func() ([]engine.Container, error) {
		s.events.publish(event{kind: "computer", computer: "alice", data: computers.Computer{Name: "alice", ID: "alice", State: "running"}})
		return []engine.Container{alice}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	count := 0
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var c computerJSON
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &c); err != nil {
			t.Fatal(err)
		}
		if c.State != "stopped" {
			t.Fatalf("queued state overwrote current snapshot: %s", c.State)
		}
		count++
		if count == 2 {
			return
		}
	}
	t.Fatal("did not receive snapshot and queued lifecycle update", scan.Err())
}
func TestEventsFailedSnapshotClosesForReconnect(t *testing.T) {
	eng := &snapshotEngine{list: func() ([]engine.Container, error) { return nil, errors.New("engine temporarily unavailable") }}
	_, ts := newTestServerWith(t, eng)
	client := login(t, ts, "admin")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("failed snapshot left healthy-looking stream open: %v", err)
	}
}
