package docker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Exercise the real adapter and demultiplexer, replacing only the daemon.
func TestExecCloseDisposesUnreadOutput(t *testing.T) {
	for _, stream := range []stdcopy.StdType{stdcopy.Stdout, stdcopy.Stderr} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			sent := make(chan struct{})
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/exec"):
					fmt.Fprint(w, `{"Id":"test"}`)
				case strings.HasSuffix(r.URL.Path, "/start"):
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					fmt.Fprint(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
					header := make([]byte, 8)
					header[0] = byte(stream)
					binary.BigEndian.PutUint32(header[4:], 32768)
					_, _ = conn.Write(header)
					_, _ = io.WriteString(conn, strings.Repeat("x", 32768))
					close(sent)
					_, _ = io.Copy(io.Discard, conn)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			c, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(ts.URL, "http://")), client.WithAPIVersion("1.52"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			p, err := (&Docker{c: c}).Exec(context.Background(), "container", engine.ExecSpec{Cmd: []string{"probe"}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { p.Stdout.(io.Closer).Close(); p.Stderr.(io.Closer).Close(); p.Close() }()
			<-sent
			// One byte consumed from a larger frame leaves the synchronous
			// demultiplexer write blocked until disposal releases its reader.
			unread := p.Stdout
			if stream == stdcopy.Stderr {
				unread = p.Stderr
			}
			if _, err := io.ReadFull(unread, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			var calls sync.WaitGroup
			for range 8 {
				calls.Go(p.Close)
			}
			calls.Wait()
			for name, reader := range map[string]io.Reader{"stdout": p.Stdout, "stderr": p.Stderr} {
				done := make(chan error, 1)
				go func() {
					b, err := io.ReadAll(reader)
					if len(b) > 0 {
						err = fmt.Errorf("disposed output retained %d bytes", len(b))
					}
					done <- err
				}()
				select {
				case err := <-done:
					if !errors.Is(err, io.ErrClosedPipe) {
						t.Errorf("%s after Close: %v", name, err)
					}
				case <-time.After(time.Second):
					t.Errorf("%s blocked after Close", name)
				}
			}
		})
	}
}
