package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func TestExecWaitCancellation(t *testing.T) {
	inspecting := make(chan struct{})
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inspecting)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		fmt.Fprint(w, `{"Running":true}`)
	}))
	defer ts.Close()
	defer close(release)
	c, err := client.New(client.WithHost(ts.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d := &Docker{c: c}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := d.waitExec(ctx, "test"); done <- err }()
	<-inspecting
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("inspection ignored cancellation")
	}
}
func TestExecWaitExit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"Running":false,"ExitCode":7}`) }))
	defer ts.Close()
	c, err := client.New(client.WithHost(ts.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d := &Docker{c: c}
	code, err := d.waitExec(context.Background(), "test")
	if err != nil || code != 7 {
		t.Fatalf("exit %d %v", code, err)
	}
}
