package environments

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestVPNPollRestoresDesiredPowerWithoutReplacingGateway(t *testing.T) {
	for _, stop := range []bool{false, true} {
		name := "unexpected-exit"
		if stop {
			name = "failed-stop"
		}
		t.Run(name, func(t *testing.T) {
			s, e, _, conn := pairSetup(t)
			ctx := context.Background()
			d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
			if err != nil {
				t.Fatal(err)
			}
			if stop {
				s.Eng = &phaseEngine{e, "consumer-stop"}
				if err = s.Stop(ctx, "alice"); err == nil {
					t.Fatal("stop must fail")
				}
				s.Eng = e
			} else if err = e.Stop(ctx, d.Container.ID, 0); err != nil {
				t.Fatal(err)
			}
			if def, _ := s.Defs.Get("alice"); (def.Power == PowerRunning) == stop {
				t.Fatal("durable desired power lost")
			}
			calls := len(e.calls)
			s.Networking.Poll(ctx, nil)
			got, err := s.Get(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			if got.Container.Running() == stop {
				t.Errorf("power mismatch remains: %s", got.Container.State)
			}
			if got.Container.ID != d.Container.ID || got.Container.NetworkContainer != d.Container.NetworkContainer {
				t.Fatal("power repair replaced consumer or namespace")
			}
			for _, call := range e.calls[calls:] {
				if call == "remove-runtime" || call == "remove-desktop" || call == "create" {
					t.Fatalf("power repair replaced healthy pair: %s", call)
				}
			}
			// A subsequent ordinary health poll must preserve both desired stopped
			// state and applications running during an ordinary tunnel reconnect.
			s.Networking.Poll(ctx, nil)
			got, _ = s.Get(ctx, "alice")
			if got.Container.Running() == stop {
				t.Fatal("health observation changed desired power")
			}
		})
	}
}

// A failed disable is shown, left alone by the poll (no retry loop), and
// retried by a start.
func TestVPNFailedDisableShowsDiagnosticAndRetriesOnStart(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	s.Eng = &phaseEngine{e, "consumer-create"}
	set := d.Settings
	set.VPNConnectionID = ""
	if _, err = s.Recreate(ctx, "alice", set, noProgress); err == nil {
		t.Fatal("disable must fail")
	}
	intent, _ := s.Networking.record("alice")
	if def, _ := s.Defs.Get("alice"); !intent.Pending || def.Network.Kind != NetworkIsolated {
		t.Fatal("explicit disable intent lost")
	}
	got, err := s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.VPN.State != "blocked" || !strings.Contains(got.VPN.Reason, "consumer-create") {
		t.Errorf("failed disable hidden: %+v", got.VPN)
	}
	if strings.Contains(got.VPN.Reason, "private-token") || strings.Contains(got.VPN.Reason, "/secret/path") {
		t.Fatal("raw transition cause exposed")
	}
	s.Eng = e
	calls := len(e.calls)
	for range 3 {
		s.Networking.Poll(ctx, nil)
	}
	for _, call := range e.calls[calls:] {
		if call == "create" || call == "ensure" {
			t.Fatalf("poll retried a failed transition: %s", call)
		}
	}
	if intent, _ = s.Networking.record("alice"); intent.Error == "" {
		t.Fatal("poll cleared the failure")
	}
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	intent, _ = s.Networking.record("alice")
	if !got.Container.Running() || got.ID != d.ID || got.Container.NetworkContainer != "" || got.Container.Restart != "unless-stopped" || got.Settings.VPNConnectionID != "" || intent.Pending || intent.Error != "" {
		t.Errorf("explicit direct intent not recovered: environment=%+v intent=%+v", got, intent)
	}
	if got.VPN.State != "disabled" || got.VPN.Reason != "" {
		t.Errorf("completed disable not published: %+v", got.VPN)
	}
}

type removeFailureEngine struct{ *pairEngine }

func (e *removeFailureEngine) Remove(context.Context, string) error { return errors.New("unavailable") }

func TestVPNPollFinishesPendingRemoval(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	s.Removed = func(name string) error { removed = append(removed, name); return nil }
	s.Eng = &removeFailureEngine{e}
	if err = s.Remove(ctx, "alice", false); err == nil {
		t.Fatal("removal must fail")
	}
	if len(removed) != 0 {
		t.Fatalf("removal reported before the definition was deleted: %v", removed)
	}
	intent, _ := s.Networking.record("alice")
	if !intent.Removing {
		t.Fatal("removal intent lost")
	}
	s.Eng = e
	s.Networking.Poll(ctx, nil)
	if _, err = e.Inspect(ctx, d.Container.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Errorf("pending removal kept consumer: %v", err)
	}
	if _, ok := s.Networking.record("alice"); ok {
		t.Error("pending removal kept its transition")
	}
	if _, ok := s.Defs.Get("alice"); ok {
		t.Error("pending removal kept the definition")
	}
	// The server purges grants, tokens and activity on hearing it.
	if !slices.Equal(removed, []string{"alice"}) {
		t.Errorf("poll's removal not reported: %v", removed)
	}
}

// Fail only the final journal write, after resource removal has succeeded.
type removalPersistenceEngine struct {
	*pairEngine
	beforeNetworkRemoval func()
}

func (e *removalPersistenceEngine) RemoveNetwork(ctx context.Context, name string) error {
	e.beforeNetworkRemoval()
	return e.pairEngine.RemoveNetwork(ctx, name)
}
func TestVPNRemovalPersistenceFailureRetainsSafeRetry(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := s.Networking.path
	blocked := filepath.Join(t.TempDir(), "private-state-sentinel")
	if err = os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	s.Eng = &removalPersistenceEngine{e, func() { s.Networking.path = filepath.Join(blocked, "transitions.json") }}
	err = s.Remove(ctx, "alice", false)
	if err == nil {
		t.Fatal("failed persistence reported successful removal")
	}
	for _, private := range []string{blocked, "transitions.json", "not a directory"} {
		if strings.Contains(err.Error(), private) {
			t.Errorf("removal exposed private persistence cause: %v", err)
		}
	}
	intent, ok := s.Networking.record("alice")
	if !ok || !intent.Removing {
		t.Fatal("failed final persistence forgot retry intent")
	}
	if _, err = e.Inspect(ctx, d.Container.ID); !errors.Is(err, engine.ErrNotFound) {
		t.Fatal("test did not reach final persistence")
	}
	s.Eng = e
	s.Networking.path = journalPath
	s.Networking.Poll(ctx, nil)
	if _, ok = s.Networking.record("alice"); ok {
		t.Fatal("final persistence retry retained removed environment")
	}
}

// orderEngine records consumer starts and stops among the runtime's calls.
type orderEngine struct{ *pairEngine }

func (e *orderEngine) Start(ctx context.Context, id string) error {
	e.calls = append(e.calls, "start:"+id)
	return e.pairEngine.Start(ctx, id)
}
func (e *orderEngine) Stop(ctx context.Context, id string, d time.Duration) error {
	e.calls = append(e.calls, "stop:"+id)
	return e.pairEngine.Stop(ctx, id, d)
}

// A gateway stopped by a host or engine restart is started again; its
// consumer is kept, stopped meanwhile, and rejoins it.
func TestVPNPollRestartsStoppedGatewayWithoutRecreatingConsumer(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	r.gateway.State = "exited"
	s.Eng = &orderEngine{e}
	calls := len(e.calls)
	s.Networking.Poll(ctx, nil)
	got, _ := s.Get(ctx, "alice")
	if got.Container.ID != d.Container.ID || !got.Container.Running() || got.Container.NetworkContainer != r.gateway.ID {
		t.Fatalf("consumer not kept running on its gateway: %+v", got.Container)
	}
	want := []string{"stop:" + d.Container.ID, "ensure", "start:" + d.Container.ID}
	if strings.Join(e.calls[calls:], " ") != strings.Join(want, " ") {
		t.Fatalf("calls %v, want %v", e.calls[calls:], want)
	}
}

// A stopped environment's stopped or failing gateway is left for its next
// start: nothing is recreated, and nothing is retried by the poll.
func TestVPNPollLeavesStoppedEnvironmentsGatewayAlone(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	r.gateway.State = "exited"
	r.fail = true
	calls := len(e.calls)
	for range 3 {
		s.Networking.Poll(ctx, nil)
	}
	if len(e.calls) != calls {
		t.Fatalf("stopped environment touched: %v", e.calls[calls:])
	}
	got, _ := s.Get(ctx, "alice")
	if got.Container.ID != d.Container.ID || got.Container.Running() || r.gateway.State != "exited" || got.Error != "" {
		t.Fatalf("stopped environment changed: %+v", got)
	}
	r.fail = false
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Get(ctx, "alice"); got.Container.ID != d.Container.ID || !got.Container.Running() || r.gateway.State != "running" {
		t.Fatalf("start did not bring the gateway back: %+v", got.Container)
	}
}

// A consumer that keeps exiting is started again by the poll with a growing
// backoff; a start by the user is immediate.
func TestVPNPollBacksOffRestartingExitingConsumer(t *testing.T) {
	s, e, _, conn := pairSetup(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.Networking.now = func() time.Time { return now }
	crashThenPoll := func(after time.Duration) bool {
		t.Helper()
		_ = e.Stop(ctx, d.Container.ID, 0)
		now = now.Add(after)
		s.Networking.Poll(ctx, nil)
		got, _ := s.Get(ctx, "alice")
		return got.Container.Running()
	}
	if !crashThenPoll(0) {
		t.Fatal("first exit not restarted")
	}
	if crashThenPoll(time.Second) {
		t.Fatal("immediate exit restarted without backoff")
	}
	if !crashThenPoll(5 * time.Second) {
		t.Fatal("not restarted after backoff")
	}
	if crashThenPoll(5 * time.Second) {
		t.Fatal("backoff did not grow")
	}
	if !crashThenPoll(5 * time.Second) {
		t.Fatal("not restarted after grown backoff")
	}
	_ = e.Stop(ctx, d.Container.ID, 0)
	if err = s.Start(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "alice"); !got.Container.Running() {
		t.Fatal("user start waited for backoff")
	}
}

// A request that goes away while the environment comes up doesn't leave it
// reported as not ready.
func TestVPNCancelledReadyWaitIsNotRecordedAsNotReady(t *testing.T) {
	s, e, r, conn := pairSetup(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.port = l.Addr().(*net.TCPAddr).Port
	r.gateway.LocalPort = e.port
	l.Close() // nothing answers: never ready
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	if _, err = s.Create(ctx, "alice", Settings{Image: "img", VPNConnectionID: conn.ID}, noProgress); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	if got, _ := s.Get(context.Background(), "alice"); got.Error == errNotReady || got.VPN.State == "blocked" {
		t.Fatalf("cancelled wait recorded as not ready: %+v", got)
	}
}
