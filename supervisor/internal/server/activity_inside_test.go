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
	if err := readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur); err == nil {
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
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur)
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
	_ = readInside(context.Background(), strings.NewReader(body), insideRef, tr, cur)
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
	_ = readInside(context.Background(), strings.NewReader(start), insideRef, tr, cur)
	if s := tr.Summary("d"); len(s) != 1 || len(s[0].Now) != 1 {
		t.Fatalf("command should be running: %+v", s)
	}
	resp := &http.Response{Header: http.Header{"X-Activity-Epoch": {"b"}}, Body: http.NoBody}
	_ = consumeInside(context.Background(), resp, insideRef, tr, cur)
	if *cur != (insideCursor{Epoch: "b"}) {
		t.Fatalf("cursor %+v", *cur)
	}
	if s := tr.Summary("d"); len(s) > 0 && len(s[0].Now) != 0 {
		t.Fatalf("reset should drop the running command: %+v", s)
	}
	// The same epoch keeps the cursor.
	cur.Seq = 7
	_ = consumeInside(context.Background(), resp, insideRef, tr, cur)
	if cur.Seq != 7 {
		t.Fatalf("cursor %+v", *cur)
	}
}
