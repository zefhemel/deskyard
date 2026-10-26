package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

const (
	insideMaxLine    = 8 << 10          // longer lines are discarded
	insideMaxEpoch   = 64               // longest epoch we remember or send
	insideIdle       = 45 * time.Second // the stream sends a line every 15 s
	insideBackoffMin = time.Second
	insideBackoffMax = 30 * time.Second
	insideOldImage   = 60 * time.Second // retry delay when the image has no stream
)

// insideCursor is how far into a desktop's stream we have read.
type insideCursor struct {
	Epoch string
	Seq   uint64
}

// readInside feeds the lines of an inside stream to the tracker, advancing
// cur. The stream is hostile input: lines over 8 KiB and lines that aren't
// JSON are skipped, as are events at or before the cursor (duplicates after a
// reconnect); the tracker drops events that fail Sanitize.
func readInside(ctx context.Context, body io.Reader, ref activity.Ref, tr *activity.Tracker, cur *insideCursor, heartbeat ...func(bool)) error {
	br := bufio.NewReaderSize(body, insideMaxLine)
	discarding := false
	report := func(idle bool) {
		for _, beat := range heartbeat {
			beat(idle)
		}
	}
	for ctx.Err() == nil {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			report(false)
			discarding = true // the rest of this line is thrown away too
			continue
		}
		if err != nil {
			return err
		}
		if discarding {
			discarding = false
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue // legacy keep-alive; insufficient for automatic reclamation
		}
		var status struct {
			Kind            string `json:"kind"`
			CommandsRunning *bool  `json:"commandsRunning"`
		}
		if json.Unmarshal(line, &status) == nil && status.Kind == "status" {
			idle := status.CommandsRunning != nil && !*status.CommandsRunning
			if idle {
				// Reconcile an end lost during disconnection.
				tr.ResetInside(ref)
			}
			report(idle)
			continue
		}
		report(false) // even a rejected event invalidates the previous idle report
		var e activity.Event
		if json.Unmarshal(line, &e) != nil || e.Seq <= cur.Seq {
			continue
		}
		cur.Seq = e.Seq
		tr.Inside(ref, e) // which rate-limits, then sanitizes
	}
	return ctx.Err()
}

// consumeInside handles a 200 response: a new epoch means the stream
// restarted, so the commands we think are running there are over, then it
// reads the lines.
func consumeInside(ctx context.Context, resp *http.Response, ref activity.Ref, tr *activity.Tracker, cur *insideCursor, heartbeat ...func(bool)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	epoch := resp.Header.Get("X-Activity-Epoch")
	if len(epoch) > insideMaxEpoch {
		epoch = epoch[:insideMaxEpoch]
	}
	if epoch != cur.Epoch {
		tr.ResetInside(ref)
		*cur = insideCursor{Epoch: epoch}
	}
	return readInside(ctx, resp.Body, ref, tr, cur, heartbeat...)
}

type insidePosition struct {
	generation string
	cursor     insideCursor
}

// insideReader follows the activity stream of every running desktop while
// someone looks (and a while after), or automatic reclamation needs activity.
type insideReader struct {
	s       *Server
	client  *http.Client
	mu      sync.Mutex
	running map[string]*environmentWorker
	cursors map[string]insidePosition
	healthy map[string]time.Time
	wakeC   chan struct{}
	lastUse time.Time
}

func newInsideReader(s *Server) *insideReader {
	return &insideReader{
		s: s,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				ResponseHeaderTimeout: 10 * time.Second,
				DisableCompression:    true,
			},
		},
		running: map[string]*environmentWorker{},
		cursors: map[string]insidePosition{},
		healthy: map[string]time.Time{},
		wakeC:   make(chan struct{}, 1),
	}
}

func (ir *insideReader) wake() {
	if ir == nil {
		return // a server that isn't running its background work
	}
	select {
	case ir.wakeC <- struct{}{}:
	default:
	}
}

func (ir *insideReader) run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		ir.reconcile(ctx)
		select {
		case <-ctx.Done():
			ir.mu.Lock()
			for _, worker := range ir.running {
				worker.cancel()
			}
			ir.mu.Unlock()
			return
		case <-tick.C:
		case <-ir.wakeC:
		}
	}
}

// reconcile follows running desktops while wanted and lets go of the rest.
func (ir *insideReader) reconcile(ctx context.Context) {
	if ir.s.events.count() > 0 {
		ir.lastUse = time.Now()
	}
	list, err := ir.s.Environments.List(ctx)
	if err != nil {
		return
	}
	look := time.Since(ir.lastUse) < statsLinger
	wanted := map[string]environments.Environment{}
	live := map[string]bool{}
	refs := map[string]activity.Ref{}
	for _, c := range list {
		refs[c.Name] = refOf(c)
		if c.State != "running" {
			continue
		}
		live[c.Name] = true
		if look || (ir.s.memoryReclaim.Enabled && c.MemoryReclaim) {
			wanted[c.Name] = c
		}
	}
	// A desktop that stopped takes its stream with it: what we thought was
	// running there is over. (Letting go because nobody looks is not that.)
	var stopped []activity.Ref
	var starts []func()
	ir.mu.Lock()
	for name, worker := range ir.running {
		if c, ok := wanted[name]; !ok || worker.generation != environmentGeneration(c) {
			worker.cancel()
			delete(ir.healthy, name)
			if !live[name] || (ok && worker.generation != environmentGeneration(c)) {
				stopped = append(stopped, refs[name])
				delete(ir.cursors, name)
			}
		}
	}
	for name := range ir.cursors {
		if !live[name] {
			delete(ir.cursors, name)
			stopped = append(stopped, refs[name])
		}
	}
	for name, c := range wanted {
		if _, ok := ir.running[name]; ok {
			continue
		}
		worker := startEnvironmentWorker(ctx, c)
		ir.running[name] = worker
		position := ir.cursors[name]
		if position.generation != worker.generation {
			if position.generation != "" {
				stopped = append(stopped, refOf(c))
			}
			position = insidePosition{generation: worker.generation}
		}
		ir.cursors[name] = position
		starts = append(starts, func() { go ir.follow(worker, c, position.cursor) })
	}
	ir.mu.Unlock()
	// The tracker takes its own locks and calls back: never hold ours.
	for _, r := range stopped {
		if r.Name != "" {
			ir.s.activity.ResetInside(r)
		}
	}
	for _, start := range starts {
		start()
	}
}

// follow keeps one desktop's stream connected until ctx ends, resuming from
// the cursor after errors and restarts.
func (ir *insideReader) follow(worker *environmentWorker, c environments.Environment, cur insideCursor) {
	ctx := worker.ctx
	defer close(worker.done)
	defer worker.cancel()
	defer ir.wake()
	ref := refOf(c)
	defer func() {
		ir.mu.Lock()
		if ir.running[c.Name] == worker {
			delete(ir.running, c.Name)
		}
		if ir.running[c.Name] == nil && ir.cursors[c.Name].generation == worker.generation {
			ir.cursors[c.Name] = insidePosition{generation: worker.generation, cursor: cur}
		}
		ir.mu.Unlock()
	}()
	backoff := insideBackoffMin
	for ctx.Err() == nil {
		wait, err := ir.connect(worker, c, ref, &cur)
		if errors.Is(err, environments.ErrStopped) {
			return
		}
		if wait == 0 {
			wait = backoff
			backoff = min(backoff*2, insideBackoffMax)
		} else {
			backoff = insideBackoffMin
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// connect makes one connection and reads it until it ends. It returns how
// long to wait before the next try (0: use the back-off).
func (ir *insideReader) connect(worker *environmentWorker, c environments.Environment, ref activity.Ref, cur *insideCursor) (time.Duration, error) {
	ctx, name := worker.ctx, c.Name
	defer func() {
		ir.mu.Lock()
		if ir.running[name] == worker {
			delete(ir.healthy, name)
		}
		ir.mu.Unlock()
	}()
	fresh, err := ir.s.Environments.Get(ctx, name)
	if err != nil {
		return 0, err
	}
	if environmentGeneration(fresh) != worker.generation {
		return 0, environments.ErrStopped
	}
	addr, token, err := ir.s.Environments.Target(ctx, name)
	if err != nil {
		return 0, err
	}
	q := url.Values{"epoch": {cur.Epoch}, "after": {fmt.Sprint(cur.Seq)}}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, "http://"+addr+"/_activity/events?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Deskyard-Token", token)
	resp, err := ir.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return insideOldImage, nil // an image without the stream
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("activity stream: %s", resp.Status)
	}
	// A stream that goes quiet (not even a keep-alive) is cut and retried.
	idle := time.AfterFunc(insideIdle, cancel)
	defer idle.Stop()
	resp.Body = &idleBody{ReadCloser: resp.Body, idle: idle}
	err = consumeInside(rctx, resp, ref, ir.s.activity, cur, func(idle bool) {
		ir.mu.Lock()
		if ir.running[name] != worker || ctx.Err() != nil {
			ir.mu.Unlock()
			return
		}
		if idle {
			ir.healthy[name] = time.Now()
		} else {
			delete(ir.healthy, name)
		}
		ir.mu.Unlock()
	})
	// It was connected: the next try is soon, not after a long back-off.
	return insideBackoffMin, err
}

// idleBody pushes the idle timer back on every read.
type idleBody struct {
	io.ReadCloser
	idle *time.Timer
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.idle.Reset(insideIdle)
	}
	return n, err
}

// A status heartbeat follows replay of the backlog. No idle report, a lost connection,
// or an old image without activity support makes automatic reclaim wait.
func (ir *insideReader) ready(name string, now time.Time) bool {
	if ir == nil {
		return false
	}
	ir.mu.Lock()
	defer ir.mu.Unlock()
	at, ok := ir.healthy[name]
	return ok && now.Sub(at) >= 0 && now.Sub(at) < insideIdle
}
