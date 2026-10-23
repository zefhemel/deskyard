package docker

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func execLifetimeAdapter(t *testing.T, inspect http.HandlerFunc) (*Docker, <-chan struct{}) {
	t.Helper()
	disconnected := make(chan struct{})
	var connection net.Conn
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/gateway/exec"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"exec-fixture"}`))
		case strings.HasSuffix(r.URL.Path, "/exec/exec-fixture/start"):
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			connection = conn
			mu.Unlock()
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = rw.Flush()
			header := make([]byte, 8)
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], 8)
			_, _ = conn.Write(append(header, []byte("pending!")...))
			_, _ = io.Copy(io.Discard, conn)
			close(disconnected)
		case strings.HasSuffix(r.URL.Path, "/exec/exec-fixture/json"):
			if inspect != nil {
				inspect(w, r)
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"Running":false,"ExitCode":0}`))
			}
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), client.WithAPIVersion("1.53"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	d := &Docker{c: c}

	t.Cleanup(func() {
		mu.Lock()
		if connection != nil {
			_ = connection.Close()
		}
		mu.Unlock()
	})
	return d, disconnected
}
func TestExecCancellationClosesAttachmentAndUnreadDemuxPipes(t *testing.T) {
	d, disconnected := execLifetimeAdapter(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := d.Exec(ctx, "gateway", engine.ExecSpec{Cmd: []string{"sleep", "infinity"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cancel()
	select {
	case <-disconnected:
	case <-time.After(250 * time.Millisecond):
		t.Error("Docker attachment remained open after cancellation")
		p.Close()
	}
	done := make(chan struct{})
	go func() { _, _ = io.ReadAll(p.Stdout); _, _ = io.ReadAll(p.Stderr); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("unread demultiplex pipes did not unblock after cancellation")
	}
}
func TestExecWaitInspectHonorsCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	d, _ := execLifetimeAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Running":false,"ExitCode":0}`))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := d.Exec(ctx, "gateway", engine.ExecSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan error, 1)
	go func() { _, e := p.Wait(); done <- e }()
	<-entered
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Errorf("Wait cancellation lost: %v", e)
		}
	case <-time.After(250 * time.Millisecond):
		t.Error("Wait used an uncancelled Docker inspect context")
		close(release)
		<-done
	}
}
