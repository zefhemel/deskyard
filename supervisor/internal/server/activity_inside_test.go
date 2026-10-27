package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

var insideRef = activity.Ref{Name: "d", ID: "id"}

func insideLine(seq int, rest string) string {
	return fmt.Sprintf(`{"seq":%d,"t":"%s",%s}`+"\n", seq, activity.NewTracker().Now().UTC().Format("2006-01-02T15:04:05.999Z07:00"), rest)
}

func TestReadInsideFeedsTracker(t *testing.T) {
	tr := activity.NewTracker()
	body := insideLine(1, `"agent":"a terminal","kind":"screen","screen":{"action":"click","pos":[10,10]}`) +
		insideLine(2, `"agent":"a terminal","kind":"cmd-start","id":"x","cmd":"ls"`) +
		insideLine(3, `"agent":"a terminal","kind":"cmd-end","id":"x","exit":0`)
	cur := &insideCursor{Epoch: "e"}
	if err := readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur, nil); err == nil {
		t.Fatal("expected EOF at the end of the body")
	}
	if cur.Seq != 3 {
		t.Fatalf("seq %d", cur.Seq)
	}
	sums := tr.Summary("d")
	if len(sums) != 1 || sums[0].ID != "inside:a terminal" {
		t.Fatalf("summaries %+v", sums)
	}
	var click, cmd bool
	for _, it := range sums[0].Recent {
		click = click || (it.Kind == "screen" && it.Action == "click")
		cmd = cmd || (it.Kind == "command" && it.Exit != nil)
	}
	if !click || !cmd || len(sums[0].Now) != 0 {
		t.Fatalf("recent %+v now %+v", sums[0].Recent, sums[0].Now)
	}
}

func TestReadInsideSkipsBadLines(t *testing.T) {
	tr := activity.NewTracker()
	body := strings.Repeat("x", 20<<10) + "\n" + "not json\n" + "\n" +
		insideLine(1, `"agent":"a terminal","kind":"screen","screen":{"action":"click","pos":[1,2]}`)
	cur := &insideCursor{}
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur, nil)
	if cur.Seq != 1 || len(tr.Summary("d")) != 1 {
		t.Fatalf("seq %d, summaries %+v", cur.Seq, tr.Summary("d"))
	}
}

func TestReadInsideSkipsOldSeq(t *testing.T) {
	tr := activity.NewTracker()
	body := insideLine(4, `"agent":"a","kind":"screen","screen":{"action":"click","pos":[1,2]}`) +
		insideLine(5, `"agent":"a","kind":"screen","screen":{"action":"click","pos":[1,2]}`) +
		insideLine(6, `"agent":"a","kind":"screen","screen":{"action":"click","pos":[1,2]}`)
	cur := &insideCursor{Seq: 5}
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur, nil)
	if cur.Seq != 6 {
		t.Fatalf("seq %d", cur.Seq)
	}
	if r := tr.Summary("d"); len(r) != 1 || len(r[0].Recent) != 1 {
		t.Fatalf("only seq 6 should count: %+v", r)
	}
}

func TestEpochChangeResetsInside(t *testing.T) {
	tr := activity.NewTracker()
	cur := &insideCursor{Epoch: "a"}
	start := insideLine(1, `"agent":"a","kind":"cmd-start","id":"x","cmd":"sleep 99"`)
	_ = readInside(context.Background(), strings.NewReader(start), insideRef, tr, cur, nil)
	if s := tr.Summary("d"); len(s) != 1 || len(s[0].Now) != 1 {
		t.Fatalf("command should be running: %+v", s)
	}
	resp := &http.Response{Header: http.Header{"X-Activity-Epoch": {"b"}}, Body: http.NoBody}
	_ = consumeInside(context.Background(), resp, insideRef, tr, cur, nil)
	if *cur != (insideCursor{Epoch: "b"}) {
		t.Fatalf("cursor %+v", *cur)
	}
	if s := tr.Summary("d"); len(s) > 0 && len(s[0].Now) != 0 {
		t.Fatalf("reset should drop the running command: %+v", s)
	}
	// The same epoch keeps the cursor.
	cur.Seq = 7
	_ = consumeInside(context.Background(), resp, insideRef, tr, cur, nil)
	if cur.Seq != 7 {
		t.Fatalf("cursor %+v", *cur)
	}
}

func TestInsideStatusRequiresExplicitIdleReport(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{
		{"\n", 0},
		{"{\"kind\":\"status\",\"commandsRunning\":true}\n", 0},
		{"{\"kind\":\"status\"}\n", 0},
		{"{\"kind\":\"status\",\"commandsRunning\":false}\n", 1},
	} {
		calls := 0
		tr := activity.NewTracker()
		cur := insideCursor{}
		_ = readInside(context.Background(), strings.NewReader(tc.body), insideRef, tr, &cur, nil, func(idle bool) {
			if idle {
				calls++
			}
		})
		if calls != tc.want {
			t.Fatalf("%q: got %d", tc.body, calls)
		}
	}
}

func TestInsideStatusReconcilesLostEndAndInvalidatesOnWork(t *testing.T) {
	tr := activity.NewTracker()
	cur := insideCursor{}
	ready := false
	consume := func(body string) {
		_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, &cur, nil, func(idle bool) { ready = idle })
	}
	consume(insideLine(1, `"agent":"terminal","kind":"cmd-start","id":"work","cmd":"sleep"`))
	if len(tr.Summary("d")[0].Now) != 1 {
		t.Fatal("start missing")
	}
	consume("{\"kind\":\"status\",\"commandsRunning\":false}\n")
	for _, sum := range tr.Summary("d") {
		if len(sum.Now) > 0 {
			t.Fatal("lost end was not reconciled")
		}
	}
	if !ready {
		t.Fatal("idle not ready")
	}
	// Unknown/rejected activity cannot leave an earlier idle report valid.
	consume("bad json\n")
	if ready {
		t.Fatal("activity did not invalidate idle status")
	}
}

func TestInsideRejectedStartInvalidatesIdle(t *testing.T) {
	tr := activity.NewTracker()
	tr.InsideBurst = 0
	tr.InsideRate = 0
	cur := insideCursor{}
	ready := false
	body := `{"kind":"status","commandsRunning":false}` + "\n" + insideLine(1, `"agent":"terminal","kind":"cmd-start","id":"work","cmd":"sleep"`)
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, &cur, nil, func(idle bool) { ready = idle })
	if ready || len(tr.Summary("d")) != 0 {
		t.Fatal("rate-limited start left idle status valid")
	}
}

func TestInsideMonitoringContinuesWithoutUIForAutoReclaim(t *testing.T) {
	s, _ := newTestServerWith(t, runningReclaimEngine())
	ir := newInsideReader(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ir.reconcile(ctx)
	ir.mu.Lock()
	_, following := ir.running["bob"]
	ir.mu.Unlock()
	if !following || s.events.count() != 0 {
		t.Fatal("eligible desktop was not followed without UI")
	}
	if err := s.Environments.SetMemoryReclaim("bob", false); err != nil {
		t.Fatal(err)
	}
	ir.reconcile(ctx)
	ir.mu.Lock()
	worker, present := ir.running["bob"]
	following = present && worker.ctx.Err() == nil
	ir.mu.Unlock()
	if following {
		t.Fatal("opt-out desktop followed without UI")
	}
}

// A desktop's terminals event asks for a session list; it is no agent activity.
func TestReadInsideTerminalsEvent(t *testing.T) {
	tr := activity.NewTracker()
	body := insideLine(1, `"agent":"tmux","kind":"terminals"`) +
		insideLine(2, `"agent":"tmux","kind":"terminals"`) +
		insideLine(2, `"agent":"tmux","kind":"terminals"`)
	cur := &insideCursor{}
	n := 0
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur, func() { n++ })
	if n != 2 || cur.Seq != 2 || len(tr.Summary("d")) != 0 {
		t.Fatalf("terminals %d, seq %d, summaries %+v", n, cur.Seq, tr.Summary("d"))
	}
}
