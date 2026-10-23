package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Older daemons accept unknown bind options but do not enforce them. Reject
// before creation instead of promising recursive read-only on an ignored flag.
func TestStrictReadOnlyRejectsOldDockerAPI(t *testing.T) {
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.43")
			w.WriteHeader(200)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/containers/create") {
			creates.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"Id":"fake"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	d, err := New("tcp://" + strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Create(context.Background(), engine.ContainerSpec{Name: "probe", Image: "alpine:3.22", Mounts: []engine.Mount{{Source: "/tmp/source", Target: "/data", MustExist: true, ReadOnly: true}}})
	if err == nil || !strings.Contains(err.Error(), "1.44") || creates.Load() != 0 {
		t.Fatalf("old daemon accepted unsafe bind: %v, creates=%d", err, creates.Load())
	}
}
