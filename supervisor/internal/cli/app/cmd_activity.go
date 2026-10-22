package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

// ---- activity-serve

// Activity stream limits: what the ring keeps, and how often an idle stream
// sends an empty line so proxies and readers see it is alive.
const (
	activityWindow    = 15 * time.Minute
	activityMax       = 5000
	activityHeartbeat = 15 * time.Second
)

// runActivityServe is the desktop's activity stream: events arrive as UDP
// datagrams (one JSON event each, from any program in the desktop, so
// untrusted), are sanitized and kept in a ring, and are served as NDJSON over
// HTTP on the same address. Writers never wait: UDP drops when nobody reads.
func runActivityServe(a *App, c *call) error {
	addr := c.str("--listen", activity.StreamAddr)
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	defer pc.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ring := activity.NewRing(activityWindow, activityMax, time.Now)
	go readDatagrams(pc, ring)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) { serveEvents(w, r, ring) })
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// readDatagrams turns datagrams into ring events until the socket closes.
func readDatagrams(pc net.PacketConn, ring *activity.Ring) {
	buf := make([]byte, 2*activity.MaxDatagram)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("activity-serve: read: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if n > activity.MaxDatagram {
			continue
		}
		var e activity.Event
		if json.Unmarshal(buf[:n], &e) != nil {
			continue
		}
		if e, ok := activity.Sanitize(e); ok {
			ring.Add(e)
		}
	}
}

// serveEvents streams everything newer than ?after (everything, when ?epoch
// isn't the ring's), then live events, with an empty line every 15 s.
func serveEvents(w http.ResponseWriter, r *http.Request, ring *activity.Ring) {
	q := r.URL.Query()
	after, _ := strconv.ParseUint(q.Get("after"), 10, 64)
	if q.Get("epoch") != ring.Epoch() {
		after = 0
	}
	fl, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Activity-Epoch", ring.Epoch())
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if fl != nil {
		fl.Flush()
	}
	bw := bufio.NewWriter(w)
	for {
		ctx, cancel := context.WithTimeout(r.Context(), activityHeartbeat)
		events := ring.Wait(ctx, after)
		cancel()
		if r.Context().Err() != nil {
			return
		}
		if events == nil {
			fmt.Fprintln(bw) // heartbeat
		}
		for _, e := range events {
			line, _ := json.Marshal(e)
			bw.Write(line)
			bw.WriteByte('\n')
			after = e.Seq
		}
		if bw.Flush() != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
}
