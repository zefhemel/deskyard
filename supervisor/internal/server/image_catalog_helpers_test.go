package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Image catalogue test support; no image building or scanning.
type catalogEngine struct {
	engine.Engine
	mu         sync.Mutex
	images     map[string]engine.Image
	tags       map[string]string
	desktops   []engine.Container
	inspectErr map[string]error
	pulls      []string
	pullErr    error
}

func newCatalogEngine() *catalogEngine {
	e := &catalogEngine{images: map[string]engine.Image{}, tags: map[string]string{}}
	e.addImage("img", "sha256:base1", "user")
	return e
}
func (e *catalogEngine) addImage(ref, id, user string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.images[id] = engine.Image{ID: id, User: user, Arch: "arm64", Labels: map[string]string{
		environments.LabelContract: environments.Contract, "deskyard.kind": "desktop", "org.opencontainers.image.title": "Debian XFCE"}}
	e.tags[ref] = id
}

func (e *catalogEngine) resolve(ref string) (engine.Image, bool) {
	if id, ok := e.tags[ref]; ok {
		ref = id
	}
	img, ok := e.images[ref]
	return img, ok
}

func (e *catalogEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.inspectErr[ref]; err != nil {
		return engine.Image{}, err
	}
	if img, ok := e.resolve(ref); ok {
		return img, nil
	}
	return engine.Image{}, engine.ErrNotFound
}

func (e *catalogEngine) Pull(_ context.Context, ref string, _ func(engine.PullProgress)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pulls = append(e.pulls, ref)
	return e.pullErr
}

func (e *catalogEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.desktops {
		if c.Name == id || c.ID == id {
			return c, nil
		}
	}
	return engine.Container{}, engine.ErrNotFound
}

func (e *catalogEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]engine.Container(nil), e.desktops...), nil
}

func (e *catalogEngine) Info(context.Context) (engine.HostInfo, error) {
	return engine.HostInfo{CPUs: 4}, nil
}

type catalogTest struct {
	t     *testing.T
	s     *Server
	ts    *httptest.Server
	e     *catalogEngine
	admin *http.Client
}

func newCatalogTest(t *testing.T) *catalogTest {
	e := newCatalogEngine()
	s, ts := newTestServerWith(t, e)
	return &catalogTest{t: t, s: s, ts: ts, e: e, admin: login(t, ts, "admin")}
}
func (bt *catalogTest) do(c *http.Client, method, path string, body any) (int, []byte) {
	bt.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, bt.ts.URL+path, r)
	req.Header.Set("Origin", bt.ts.URL)
	resp, err := c.Do(req)
	if err != nil {
		bt.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// start sends a build request and returns its job.
func (bt *catalogTest) start(method, path string, body any) string {
	bt.t.Helper()
	code, b := bt.do(bt.admin, method, path, body)
	if code != http.StatusAccepted {
		bt.t.Fatalf("%s %s: %d %s", method, path, code, b)
	}
	var res struct{ Job string }
	_ = json.Unmarshal(b, &res)
	return res.Job
}

func (bt *catalogTest) wait(id string) jobState {
	bt.t.Helper()
	j := bt.s.jobs.get(id)
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
		bt.t.Fatalf("job %s didn't finish", id)
	}
	return j.get()
}

func (bt *catalogTest) catalog() map[string]catalogImageJSON {
	bt.t.Helper()
	code, b := bt.do(bt.admin, "GET", "/api/images", nil)
	if code != 200 {
		bt.t.Fatalf("images: %d %s", code, b)
	}
	var list []catalogImageJSON
	_ = json.Unmarshal(b, &list)
	out := map[string]catalogImageJSON{}
	for _, i := range list {
		out[i.Ref] = i
	}
	return out
}

func withEnvironment(t *testing.T, bt *catalogTest, files map[string]string) string {
	t.Helper()
	bt.e.desktops = []engine.Container{{ID: "c1", Name: "deskyard-alice", Image: "img", ImageID: "sha256:base1", State: "exited",
		Labels: map[string]string{environments.LabelManaged: "true", environments.LabelName: "alice", environments.LabelRevision: "1"}}}
	defineEnvironments(t, bt.s)
	folder := bt.s.Environments.Layout.HomeOf("alice")
	for p, data := range files {
		full := filepath.Join(folder, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return folder
}

var aliceHome = map[string]string{".bashrc": "# mine"}
