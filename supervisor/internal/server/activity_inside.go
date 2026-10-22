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
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
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
// cur. The stream is hostile input: lines over 8 KiB, lines that aren't JSON
// and events that fail Sanitize are skipped, as are events at or before the
// cursor (duplicates after a reconnect).
func readInside(ctx context.Context, body io.Reader, ref activity.Ref, tr *activity.Tracker, cur *insideCursor) error {
	br := bufio.NewReaderSize(body, insideMaxLine)
	discarding := false
	for ctx.Err() == nil {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
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
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue // keep-alive
		}
		var e activity.Event
		if json.Unmarshal(line, &e) != nil || e.Seq <= cur.Seq {
			continue
		}
		cur.Seq = e.Seq
		if e, ok := activity.Sanitize(e); ok {
			tr.Inside(ref, e)
		}
	}
	return ctx.Err()
}

// consumeInside handles a 200 response: a new epoch means the stream
// restarted, so the commands we think are running there are over, then it
// reads the lines.
func consumeInside(ctx context.Context, resp *http.Response, ref activity.Ref, tr *activity.Tracker, cur *insideCursor) error {
	epoch := resp.Header.Get("X-Activity-Epoch")
	if len(epoch) > insideMaxEpoch {
		epoch = epoch[:insideMaxEpoch]
	}
	if epoch != cur.Epoch {
		tr.ResetInside(ref)
		*cur = insideCursor{Epoch: epoch}
	}
	return readInside(ctx, resp.Body, ref, tr, cur)
}

// insideReader follows the activity stream of every running desktop while
// someone looks (and a while after), like the stats collector.
type insideReader struct {
	s       *Server
	client  *http.Client
	mu      sync.Mutex
	running map[string]context.CancelFunc
	cursors map[string]insideCursor
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
		running: map[string]context.CancelFunc{},
		cursors: map[string]insideCursor{},
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
			for _, cancel := range ir.running {
				cancel()
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
	list, err := ir.s.Desktops.List(ctx)
	if err != nil {
		return
	}
	look := time.Since(ir.lastUse) < statsLinger
	wanted := map[string]desktops.Desktop{}
	live := map[string]bool{}
	refs := map[string]activity.Ref{}
	for _, c := range list {
		refs[c.Name] = refOf(c)
		if c.State != "running" {
			continue
		}
		live[c.Name] = true
		if look {
			wanted[c.Name] = c
		}
	}
	// A desktop that stopped takes its stream with it: what we thought was
	// running there is over. (Letting go because nobody looks is not that.)
	var stopped []activity.Ref
	ir.mu.Lock()
	for name, cancel := range ir.running {
		if _, ok := wanted[name]; !ok {
			cancel()
			delete(ir.running, name)
			if !live[name] {
				stopped = append(stopped, refs[name])
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
		cctx, cancel := context.WithCancel(ctx)
		ir.running[name] = cancel
		go ir.follow(cctx, c, ir.cursors[name])
	}
	ir.mu.Unlock()
	// The tracker takes its own locks and calls back: never hold ours.
	for _, r := range stopped {
		if r.Name != "" {
			ir.s.activity.ResetInside(r)
		}
	}
}

// follow keeps one desktop's stream connected until ctx ends, resuming from
// the cursor after errors and restarts.
func (ir *insideReader) follow(ctx context.Context, c desktops.Desktop, cur insideCursor) {
	ref := refOf(c)
	defer func() {
		ir.mu.Lock()
		if ctx.Err() == nil {
			delete(ir.running, c.Name) // ended by itself; reconcile may restart it
		}
		ir.cursors[c.Name] = cur
		ir.mu.Unlock()
	}()
	backoff := insideBackoffMin
	for ctx.Err() == nil {
		wait, err := ir.connect(ctx, c.Name, ref, &cur)
		if errors.Is(err, desktops.ErrStopped) {
			ir.s.activity.ResetInside(ref)
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
func (ir *insideReader) connect(ctx context.Context, name string, ref activity.Ref, cur *insideCursor) (time.Duration, error) {
	addr, token, err := ir.s.Desktops.Target(ctx, name)
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
	err = consumeInside(rctx, resp, ref, ir.s.activity, cur)
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
