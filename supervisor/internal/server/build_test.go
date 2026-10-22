package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/imagescan"
	"github.com/zefhemel/deskyard/supervisor/internal/recipes"
)

// buildEngine is a fake engine with images, tags, builds and the throwaway
// scan containers; desktops are the containers listed in desktops.
type buildEngine struct {
	engine.Engine

	mu         sync.Mutex
	images     map[string]engine.Image // by ID
	tags       map[string]string       // ref → ID
	containers map[string]string       // scan container ID → image ID
	desktops   []engine.Container
	scans      map[string]scanOut // image ID → scanner output (default: a clean scan)
	copied     []string           // CopyToContainer calls: container:dir
	specs      []engine.ContainerSpec
	inspectErr map[string]error // ImageInspect fails for these refs
	pulls      []string
	pullErr    error
	builds     []engine.BuildSpec
	dockerfile string
	context    map[string]string // the last build context: name → content ("<dir>", "-> target")
	folderTar  []byte            // what a folder-reading helper container streams
	n          int

	// buildFn replaces the default build (an image with the base's labels
	// plus the build's).
	buildFn func(ctx context.Context, spec engine.BuildSpec, log func(string)) (string, error)
}

func newBuildEngine() *buildEngine {
	e := &buildEngine{
		images:     map[string]engine.Image{},
		tags:       map[string]string{},
		containers: map[string]string{},
		scans:      map[string]scanOut{},
	}
	e.addImage("img", "sha256:base1", "user")
	return e
}

func (e *buildEngine) addImage(ref, id, user string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.images[id] = engine.Image{ID: id, User: user, Arch: "arm64", Labels: map[string]string{
		desktops.LabelContract: desktops.Contract, labelKind: "desktop", labelTitle: "Debian XFCE"}}
	e.tags[ref] = id
}

func (e *buildEngine) resolve(ref string) (engine.Image, bool) {
	if id, ok := e.tags[ref]; ok {
		ref = id
	}
	img, ok := e.images[ref]
	return img, ok
}

func (e *buildEngine) ImageInspect(_ context.Context, ref string) (engine.Image, error) {
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

func (e *buildEngine) Pull(_ context.Context, ref string, _ func(engine.PullProgress)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pulls = append(e.pulls, ref)
	return e.pullErr
}

func (e *buildEngine) Build(ctx context.Context, spec engine.BuildSpec, log func(string)) (string, error) {
	tr := tar.NewReader(spec.Context)
	var df []byte
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		switch h.Typeflag {
		case tar.TypeDir:
			files[h.Name] = "<dir>"
		case tar.TypeSymlink:
			files[h.Name] = "-> " + h.Linkname
		default:
			files[h.Name] = string(b)
		}
		if h.Name == "Dockerfile" {
			df = b
		}
	}
	e.mu.Lock()
	e.builds = append(e.builds, spec)
	e.dockerfile = string(df)
	e.context = files
	fn := e.buildFn
	e.mu.Unlock()
	if fn != nil {
		return fn(ctx, spec, log)
	}
	log("Step 1/3 : FROM base")
	log("Successfully built")
	base, _, _ := strings.Cut(strings.TrimPrefix(string(df), "FROM "), "\n")
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.images[base]
	if !ok {
		return "", fmt.Errorf("no base %s", base)
	}
	e.n++
	id := fmt.Sprintf("sha256:built%d", e.n)
	labels := map[string]string{}
	for k, v := range b.Labels {
		labels[k] = v
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	e.images[id] = engine.Image{ID: id, Labels: labels, User: b.User, Arch: b.Arch}
	return id, nil
}

func (e *buildEngine) Tag(_ context.Context, id, ref string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tags[ref] = id
	return nil
}

func (e *buildEngine) ImageTags(_ context.Context, repo string) (map[string]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]string{}
	for ref, id := range e.tags {
		if strings.HasPrefix(ref, repo+":") {
			out[ref] = id
		}
	}
	return out, nil
}

func (e *buildEngine) RemoveImage(_ context.Context, ref string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tags[ref]; ok {
		delete(e.tags, ref)
		return nil
	}
	if _, ok := e.images[ref]; ok {
		delete(e.images, ref)
		return nil
	}
	return engine.ErrNotFound
}

func (e *buildEngine) Create(_ context.Context, spec engine.ContainerSpec) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	img, ok := e.resolve(spec.Image)
	if !ok {
		return "", engine.ErrNotFound
	}
	e.containers[spec.Name] = img.ID
	e.specs = append(e.specs, spec)
	return spec.Name, nil
}

func (e *buildEngine) Start(context.Context, string) error { return nil }

func (e *buildEngine) Remove(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.containers, id)
	return nil
}

type scanOut struct {
	out  string
	code int
}

// scanned is the scanner's output for r.
func scanned(r imagescan.Result) scanOut {
	var b bytes.Buffer
	_ = imagescan.Write(&b, r)
	return scanOut{out: b.String()}
}

func cleanResult() imagescan.Result {
	return imagescan.Result{
		Files:    []imagescan.File{{Path: "/usr/bin/passwd", Kind: imagescan.KindSetuid, SHA256: "aa"}},
		Accounts: []imagescan.Account{{Name: "root", Password: "p"}, {Name: "user", UID: 1000, GID: 1000, Password: "q"}},
		Groups:   []imagescan.Group{{Name: "root", Members: []string{}}},
	}
}

func (e *buildEngine) CopyToContainer(_ context.Context, id, dir string, _ io.Reader) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.copied = append(e.copied, id+":"+dir)
	return nil
}

func (e *buildEngine) Exec(_ context.Context, id string, _ engine.ExecSpec) (*engine.Process, error) {
	if strings.HasPrefix(id, "deskyard-read-") {
		return &engine.Process{
			Stdout: bytes.NewReader(e.folderTar), Stderr: strings.NewReader(""),
			Wait: func() (int, error) { return 0, nil }, Close: func() {},
		}, nil
	}
	e.mu.Lock()
	out, ok := e.scans[e.containers[id]]
	e.mu.Unlock()
	if !ok {
		out = scanned(cleanResult())
	}
	return &engine.Process{
		Stdout: strings.NewReader(out.out), Stderr: strings.NewReader("scanner says hello"),
		Wait: func() (int, error) { return out.code, nil }, Close: func() {},
	}, nil
}

func (e *buildEngine) Inspect(_ context.Context, id string) (engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.desktops {
		if c.Name == id || c.ID == id {
			return c, nil
		}
	}
	return engine.Container{}, engine.ErrNotFound
}

func (e *buildEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]engine.Container(nil), e.desktops...), nil
}

func (e *buildEngine) Info(context.Context) (engine.HostInfo, error) {
	return engine.HostInfo{CPUs: 4}, nil
}

func (e *buildEngine) tagsOf(repo string) map[string]string {
	t, _ := e.ImageTags(context.Background(), repo)
	return t
}

// --- helpers -----------------------------------------------------------------------------

type buildTest struct {
	t     *testing.T
	s     *Server
	ts    *httptest.Server
	e     *buildEngine
	admin *http.Client
	clock time.Time
}

func newBuildTest(t *testing.T) *buildTest {
	e := newBuildEngine()
	s, ts := newTestServerWith(t, e)
	s.smokeTest = func(context.Context, string) error { return nil }
	oldScanner := scannerBinary
	scannerBinary = func(string) ([]byte, error) { return []byte("scanner"), nil }
	t.Cleanup(func() { scannerBinary = oldScanner })
	bt := &buildTest{t: t, s: s, ts: ts, e: e, admin: login(t, ts, "admin"), clock: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	old := buildClock
	buildClock = func() time.Time { bt.clock = bt.clock.Add(time.Minute); return bt.clock }
	t.Cleanup(func() { buildClock = old })
	return bt
}

func (bt *buildTest) do(c *http.Client, method, path string, body any) (int, []byte) {
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
func (bt *buildTest) start(method, path string, body any) string {
	bt.t.Helper()
	code, b := bt.do(bt.admin, method, path, body)
	if code != http.StatusAccepted {
		bt.t.Fatalf("%s %s: %d %s", method, path, code, b)
	}
	var res struct{ Job string }
	_ = json.Unmarshal(b, &res)
	return res.Job
}

func (bt *buildTest) wait(id string) jobState {
	bt.t.Helper()
	j := bt.s.jobs.get(id)
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
		bt.t.Fatalf("job %s didn't finish", id)
	}
	return j.get()
}

func (bt *buildTest) build(rec recipes.Recipe) jobState {
	bt.t.Helper()
	return bt.wait(bt.start("POST", "/api/recipes", rec))
}

func (bt *buildTest) catalog() map[string]catalogImageJSON {
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

func tools(slug, base string) recipes.Recipe {
	return recipes.Recipe{Slug: slug, Name: "Tools " + slug, Base: base, Mise: []string{"jq"}, Commands: "echo hi"}
}

// --- tests -------------------------------------------------------------------------------

func TestBuildFlowTagsAndCatalog(t *testing.T) {
	bt := newBuildTest(t)
	id := bt.start("POST", "/api/recipes", tools("tools", "img"))
	st := bt.wait(id)
	if st.Status != "done" {
		t.Fatalf("build: %+v", st)
	}
	// Every log line went out as its own update, with no progress.
	states, _, _ := bt.s.jobs.get(id).since(0)
	var lines []string
	for _, s := range states {
		if s.Progress != nil && s.Status == "running" && s.Message != "Starting" {
			t.Errorf("progress on a log line: %+v", s)
		}
		lines = append(lines, s.Message)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Step 1/3 : FROM base\nSuccessfully built") {
		t.Errorf("log lines: %q", lines)
	}

	tags := bt.e.tagsOf("deskyard-custom/tools")
	built := tags["deskyard-custom/tools:latest"]
	if len(tags) != 2 || built == "" || tags["deskyard-custom/tools:20261022-120100"] != built {
		t.Fatalf("tags: %v", tags)
	}
	img, _ := bt.e.ImageInspect(context.Background(), built)
	l := img.Labels
	if l[labelKind] != "custom" || l[labelTitle] != "Tools tools" || l[labelVersion] != "2026-10-22 12:01" ||
		l[labelBase] != "sha256:base1" || l[desktops.LabelContract] != desktops.Contract || l[labelSecrets] != "" {
		t.Errorf("labels: %v", l)
	}
	var rec recipes.Recipe
	if err := json.Unmarshal([]byte(l[labelRecipe]), &rec); err != nil || rec.Slug != "tools" || rec.Commands != "echo hi" {
		t.Errorf("recipe label: %v %+v", err, rec)
	}
	// FROM the base's ID; the base's USER comes back at the end.
	if !strings.HasPrefix(bt.e.dockerfile, "FROM sha256:base1\n") || !strings.HasSuffix(bt.e.dockerfile, "\nUSER user\n") {
		t.Errorf("dockerfile:\n%s", bt.e.dockerfile)
	}
	if b := bt.e.builds[0]; b.NoCache || len(bt.e.pulls) != 0 {
		t.Errorf("first build: nocache %v, pulls %v", b.NoCache, bt.e.pulls)
	}

	c := bt.catalog()["deskyard-custom/tools:latest"]
	if !c.Custom || c.Recipe == nil || *c.Recipe != "tools" || c.BaseUpdated || c.Secrets || c.Version != "2026-10-22 12:01" {
		t.Errorf("catalog entry: %+v", c)
	}
	if code, b := bt.do(bt.admin, "GET", "/api/recipes", nil); code != 200 || !strings.Contains(string(b), `"slug":"tools"`) {
		t.Errorf("recipes: %d %s", code, b)
	}

	// A rebuild pulls the base, skips the cache, moves :latest and drops the
	// old timestamp tag no container uses.
	if st := bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil)); st.Status != "done" {
		t.Fatalf("rebuild: %+v", st)
	}
	tags = bt.e.tagsOf("deskyard-custom/tools")
	if len(tags) != 2 || tags["deskyard-custom/tools:latest"] == built || tags["deskyard-custom/tools:20261022-120200"] == "" {
		t.Errorf("tags after rebuild: %v", tags)
	}
	if b := bt.e.builds[1]; !b.NoCache || len(bt.e.pulls) != 1 || bt.e.pulls[0] != "img" {
		t.Errorf("rebuild: nocache %v, pulls %v", b.NoCache, bt.e.pulls)
	}

	// A base built on this machine can't be pulled, but still serves.
	bt.e.pullErr = errors.New("pull access denied")
	if st := bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil)); st.Status != "done" {
		t.Errorf("rebuild on an unpullable base: %+v", st)
	}
	bt.e.pullErr = nil

	// Secrets, the rule for slugs, and a base outside the catalog.
	sec := tools("sec", "img")
	sec.Secrets = true
	if st := bt.build(sec); st.Status != "done" || !bt.catalog()["deskyard-custom/sec:latest"].Secrets {
		t.Errorf("secrets: %+v", st)
	}
	if code, _ := bt.do(bt.admin, "POST", "/api/recipes", tools("Bad", "img")); code != 400 {
		t.Errorf("bad slug: %d", code)
	}
	if code, _ := bt.do(bt.admin, "POST", "/api/recipes", tools("other", "nosuch")); code != 400 {
		t.Errorf("base outside the catalog: %d", code)
	}
	if code, _ := bt.do(bt.admin, "POST", "/api/recipes", tools("tools", "img")); code != 409 {
		t.Errorf("existing slug: %d", code)
	}
	if code, _ := bt.do(bt.admin, "PUT", "/api/recipes/tools", tools("tools", "deskyard-custom/tools:latest")); code != 400 {
		t.Errorf("built on itself: %d", code)
	}
	anna := login(t, bt.ts, "anna")
	if code, _ := bt.do(anna, "GET", "/api/recipes", nil); code != 403 {
		t.Errorf("anna lists recipes: %d", code)
	}
	if code, _ := bt.do(anna, "POST", "/api/recipes/tools/rebuild", nil); code != 403 {
		t.Errorf("anna rebuilds: %d", code)
	}
}

func TestBuildRefusedWhenAlreadyBuilding(t *testing.T) {
	bt := newBuildTest(t)
	release := make(chan struct{})
	bt.e.buildFn = func(ctx context.Context, _ engine.BuildSpec, _ func(string)) (string, error) {
		<-release
		return "", errors.New("stopped")
	}
	id := bt.start("POST", "/api/recipes", tools("tools", "img"))
	for _, req := range []struct{ method, path string }{{"POST", "/api/recipes/tools/rebuild"}, {"PUT", "/api/recipes/tools"}, {"DELETE", "/api/recipes/tools"}} {
		code, b := bt.do(bt.admin, req.method, req.path, tools("tools", "img"))
		if code != 409 || !strings.Contains(string(b), "already building") {
			t.Errorf("%s %s: %d %s", req.method, req.path, code, b)
		}
	}
	// Another recipe builds meanwhile.
	other := bt.start("POST", "/api/recipes", tools("other", "img"))
	close(release)
	bt.wait(id)
	bt.wait(other)
	bt.e.buildFn = nil
	if st := bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil)); st.Status != "done" {
		t.Errorf("after the first build: %+v", st)
	}
}

func TestRootScanRejects(t *testing.T) {
	bt := newBuildTest(t)
	bad := cleanResult()
	bad.Files = append(bad.Files,
		imagescan.File{Path: "/usr/bin/sudo", Kind: imagescan.KindSudo, SHA256: "s"},
		imagescan.File{Path: "/usr/bin/sudo", Kind: imagescan.KindSetuid, SHA256: "s"},
		imagescan.File{Path: "/usr/local/bin/x", Kind: imagescan.KindSetuid, SHA256: "x"},
		imagescan.File{Path: "/etc/foo", Kind: imagescan.KindWritable})
	bad.Files[0].SHA256 = "replaced"
	bad.Accounts[0].Empty = true
	bt.e.scans["sha256:built1"] = scanned(bad)
	st := bt.build(tools("tools", "img"))
	if st.Status != "failed" {
		t.Fatalf("want failed: %+v", st)
	}
	for _, want := range []string{"/usr/bin/sudo (sudo or doas)", "/usr/bin/sudo (setuid or setgid)", "/usr/local/bin/x (setuid or setgid)",
		"/etc/foo (writable by user)", "/usr/bin/passwd (setuid or setgid, changed from the base)", "account root (UID 0 with no password)"} {
		if !strings.Contains(st.Error, want) {
			t.Errorf("error lacks %q: %s", want, st.Error)
		}
	}
	assertNothingLeft(t, bt, "tools", "sha256:built1")
	if len(bt.e.containers) != 0 {
		t.Errorf("scan containers left: %v", bt.e.containers)
	}
	if len(bt.e.copied) != 2 || !strings.HasSuffix(bt.e.copied[0], ":/") {
		t.Errorf("the scanner wasn't copied into both containers: %v", bt.e.copied)
	}
	for _, sp := range bt.e.specs {
		if !sp.NoHealthcheck || sp.Network != "none" || len(sp.Entrypoint) != 2 || sp.Entrypoint[1] != "wait" {
			t.Errorf("scan container runs something of the image's: %+v", sp)
		}
	}
}

// The scan fails closed: anything but two complete scans fails the build.
func TestRootScanFailsClosed(t *testing.T) {
	good := scanned(cleanResult()).out
	for name, out := range map[string]scanOut{
		"non-zero exit": {out: good, code: 2},
		"no sentinel":   {out: strings.TrimSuffix(good, imagescan.Sentinel+"\n")},
		"garbage":       {out: "find: -printf: unknown primary\n" + imagescan.Sentinel + "\n"},
		"nothing":       {},
	} {
		t.Run(name, func(t *testing.T) {
			bt := newBuildTest(t)
			bt.e.scans["sha256:built1"] = out
			st := bt.build(tools("tools", "img"))
			if st.Status != "failed" || !strings.Contains(st.Error, "scanning the image") || !strings.Contains(st.Error, "scanner says hello") {
				t.Fatalf("want a failed scan: %+v", st)
			}
			assertNothingLeft(t, bt, "tools", "sha256:built1")
		})
	}
	bt := newBuildTest(t)
	scannerBinary = func(arch string) ([]byte, error) { return nil, errors.New("no scanner for " + arch) }
	if st := bt.build(tools("tools", "img")); st.Status != "failed" || !strings.Contains(st.Error, "no scanner for arm64") {
		t.Fatalf("without a scanner: %+v", st)
	}
}

func assertNothingLeft(t *testing.T, bt *buildTest, slug, built string) {
	t.Helper()
	if tags := bt.e.tagsOf("deskyard-custom/" + slug); len(tags) != 0 {
		t.Errorf("tags left: %v", tags)
	}
	if _, ok := bt.catalog()["deskyard-custom/"+slug+":latest"]; ok {
		t.Errorf("catalog entry left")
	}
	if built != "" {
		if _, err := bt.e.ImageInspect(context.Background(), built); err == nil {
			t.Errorf("image %s left", built)
		}
	}
}

func TestFailedBuildLeavesNothing(t *testing.T) {
	bt := newBuildTest(t)
	bt.e.buildFn = func(context.Context, engine.BuildSpec, func(string)) (string, error) {
		return "", errors.New("The command '/bin/sh -c false' returned a non-zero code: 1")
	}
	st := bt.build(tools("tools", "img"))
	if st.Status != "failed" || !strings.Contains(st.Error, "non-zero code") {
		t.Fatalf("build error: %+v", st)
	}
	assertNothingLeft(t, bt, "tools", "")

	// A failed smoke test removes the built image.
	bt.e.buildFn = nil
	bt.s.smokeTest = func(context.Context, string) error { return errors.New("didn't come up") }
	st = bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil))
	if st.Status != "failed" || !strings.Contains(st.Error, "didn't come up") {
		t.Fatalf("smoke: %+v", st)
	}
	assertNothingLeft(t, bt, "tools", "sha256:built1")

	// A base without the contract is refused before building.
	bt.e.addImage("plain", "sha256:plain", "")
	bt.e.mu.Lock()
	delete(bt.e.images["sha256:plain"].Labels, desktops.LabelContract)
	bt.e.mu.Unlock()
	_ = bt.s.images.put(catalogEntry{Ref: "plain"})
	st = bt.build(tools("plain-tools", "plain"))
	if st.Status != "failed" || !strings.Contains(st.Error, "isn't a Deskyard desktop image") {
		t.Errorf("no contract: %+v", st)
	}
	// And a build that drops it.
	bt.s.smokeTest = func(context.Context, string) error { return nil }
	bt.e.buildFn = func(context.Context, engine.BuildSpec, func(string)) (string, error) {
		bt.e.mu.Lock()
		defer bt.e.mu.Unlock()
		bt.e.images["sha256:nocontract"] = engine.Image{ID: "sha256:nocontract", Labels: map[string]string{}}
		return "sha256:nocontract", nil
	}
	st = bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil))
	if st.Status != "failed" || !strings.Contains(st.Error, "contract") {
		t.Errorf("dropped contract: %+v", st)
	}
	assertNothingLeft(t, bt, "tools", "sha256:nocontract")
}

func TestCustomSkippedByUpdateCheck(t *testing.T) {
	bt := newBuildTest(t)
	if st := bt.build(tools("tools", "img")); st.Status != "done" {
		t.Fatal(st)
	}
	if err := bt.s.checkImages(context.Background(), func(string, float64) {}); err != nil {
		t.Fatal(err)
	}
	if len(bt.e.pulls) != 1 || bt.e.pulls[0] != "img" {
		t.Errorf("pulls: %v", bt.e.pulls)
	}
}

func TestBaseUpdatedChain(t *testing.T) {
	bt := newBuildTest(t)
	if st := bt.build(tools("aa", "img")); st.Status != "done" {
		t.Fatal(st)
	}
	if st := bt.build(tools("bb", "deskyard-custom/aa:latest")); st.Status != "done" {
		t.Fatal(st)
	}
	updated := func() (bool, bool) {
		c := bt.catalog()
		return c["deskyard-custom/aa:latest"].BaseUpdated, c["deskyard-custom/bb:latest"].BaseUpdated
	}
	if a, b := updated(); a || b {
		t.Fatalf("fresh: a %v b %v", a, b)
	}
	// A newer base pulled: a is behind, b (on a) isn't.
	bt.e.addImage("img", "sha256:base2", "user")
	bt.s.images.setNewest("img", "sha256:base2")
	if a, b := updated(); !a || b {
		t.Fatalf("base pulled: a %v b %v", a, b)
	}
	// Rebuilding a moves it on, and now b is behind.
	if st := bt.wait(bt.start("POST", "/api/recipes/aa/rebuild", nil)); st.Status != "done" {
		t.Fatal(st)
	}
	if a, b := updated(); a || !b {
		t.Fatalf("a rebuilt: a %v b %v", a, b)
	}
	// b's rebuild doesn't pull its base: it's built here.
	pulls := len(bt.e.pulls)
	if st := bt.wait(bt.start("POST", "/api/recipes/bb/rebuild", nil)); st.Status != "done" {
		t.Fatal(st)
	}
	if a, b := updated(); a || b || len(bt.e.pulls) != pulls {
		t.Fatalf("b rebuilt: a %v b %v pulls %v", a, b, bt.e.pulls)
	}
}

func TestCancelAndTimeout(t *testing.T) {
	bt := newBuildTest(t)
	started := make(chan struct{}, 1)
	bt.e.buildFn = func(ctx context.Context, _ engine.BuildSpec, _ func(string)) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	id := bt.start("POST", "/api/recipes", tools("tools", "img"))
	<-started
	anna := login(t, bt.ts, "anna")
	if code, _ := bt.do(anna, "POST", "/api/jobs/"+id+"/cancel", nil); code != 404 {
		t.Errorf("anna cancels: %d", code)
	}
	if code, b := bt.do(bt.admin, "POST", "/api/jobs/"+id+"/cancel", nil); code != 204 {
		t.Fatalf("cancel: %d %s", code, b)
	}
	if st := bt.wait(id); st.Status != "failed" || st.Error != "cancelled" {
		t.Errorf("cancelled: %+v", st)
	}
	if code, _ := bt.do(bt.admin, "POST", "/api/jobs/"+id+"/cancel", nil); code != 409 {
		t.Errorf("cancel a finished job: %d", code)
	}
	assertNothingLeft(t, bt, "tools", "")

	old := buildTimeout
	buildTimeout = 50 * time.Millisecond
	t.Cleanup(func() { buildTimeout = old })
	id = bt.start("POST", "/api/recipes/tools/rebuild", nil)
	if st := bt.wait(id); st.Status != "failed" || !strings.Contains(st.Error, "took longer than 50ms") {
		t.Errorf("timeout: %+v", st)
	}
	// Jobs other than builds can't be cancelled.
	other := bt.s.jobs.start("admin", "", func(ctx context.Context, _ func(string, float64)) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	if code, _ := bt.do(bt.admin, "POST", "/api/jobs/"+other+"/cancel", nil); code != 409 {
		t.Errorf("cancel a pull: %d", code)
	}
}

func TestDeleteRecipe(t *testing.T) {
	bt := newBuildTest(t)
	if st := bt.build(tools("tools", "img")); st.Status != "done" {
		t.Fatal(st)
	}
	built := bt.e.tags["deskyard-custom/tools:latest"]
	bt.e.desktops = []engine.Container{{ID: "c1", Name: "deskyard-alice", Image: "deskyard-custom/tools:latest", ImageID: built,
		Labels: map[string]string{desktops.LabelManaged: "true", desktops.LabelName: "alice", desktops.LabelID: "id-alice"}}}
	if code, b := bt.do(bt.admin, "DELETE", "/api/recipes/tools", nil); code != 409 || !strings.Contains(string(b), "alice uses this image") {
		t.Errorf("in use: %d %s", code, b)
	}
	if code, _ := bt.do(bt.admin, "DELETE", "/api/images?ref=deskyard-custom/tools:latest", nil); code != 409 {
		t.Errorf("in use, through images: %d", code)
	}
	bt.e.desktops = nil
	if code, b := bt.do(bt.admin, "DELETE", "/api/images?ref=deskyard-custom/tools:latest", nil); code != 204 {
		t.Fatalf("delete: %d %s", code, b)
	}
	assertNothingLeft(t, bt, "tools", "")
	if _, err := bt.s.recipes.Get("tools"); !errors.Is(err, recipes.ErrNotFound) {
		t.Errorf("recipe left: %v", err)
	}
	if code, _ := bt.do(bt.admin, "DELETE", "/api/recipes/tools", nil); code != 404 {
		t.Errorf("delete again: %d", code)
	}
}

func TestJobHistoryIsCapped(t *testing.T) {
	j := &job{changed: make(chan struct{})}
	for i := range maxJobHistory + 10 {
		j.set(jobState{Status: "running", Message: fmt.Sprint(i)})
	}
	states, next, _ := j.since(0)
	if len(states) != maxJobHistory || states[0].Message != "10" || next != maxJobHistory+10 {
		t.Fatalf("got %d states from %q, next %d", len(states), states[0].Message, next)
	}
	if states, _, _ := j.since(next - 1); len(states) != 1 {
		t.Fatalf("since the last: %d", len(states))
	}
}

func TestSameRef(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"img", "img:latest", true},
		{"deskyard-custom/x", "deskyard-custom/x:latest", true},
		{"localhost:5000/img", "localhost:5000/img:latest", true},
		{"img:1", "img:latest", false},
		{"deskyard-custom/x:20261022-120000", "deskyard-custom/x:latest", false},
	} {
		if got := sameRef(c.a, c.b); got != c.same {
			t.Errorf("sameRef(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// A job that finishes its work although cancel came late is done, not cancelled.
func TestCancelTooLateIsDone(t *testing.T) {
	js := newJobs()
	stopped := make(chan struct{})
	id := js.startCancellable("admin", func(ctx context.Context, _ func(string, float64)) error {
		<-stopped
		return nil // finished anyway
	})
	j := js.get(id)
	if !j.stop() {
		t.Fatal("couldn't cancel")
	}
	close(stopped)
	<-j.done
	if st := j.get(); st.Status != "done" {
		t.Fatalf("got %+v", st)
	}
}

// A catalog that can't be saved leaves the tags as they were.
func TestCatalogFailureUntags(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only folders")
	}
	bt := newBuildTest(t)
	if st := bt.build(tools("tools", "img")); st.Status != "done" {
		t.Fatal(st)
	}
	first := bt.e.tagsOf("deskyard-custom/tools")
	if err := os.Chmod(bt.s.StateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(bt.s.StateDir, 0o700)
	st := bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil))
	_ = os.Chmod(bt.s.StateDir, 0o700)
	if st.Status != "failed" {
		t.Fatalf("want failed: %+v", st)
	}
	if got := bt.e.tagsOf("deskyard-custom/tools"); fmt.Sprint(got) != fmt.Sprint(first) {
		t.Errorf("tags %v, want %v", got, first)
	}
	if _, err := bt.e.ImageInspect(context.Background(), "sha256:built2"); err == nil {
		t.Error("the failed build's image is left")
	}
}

func TestSmokePrefixReserved(t *testing.T) {
	bt := newBuildTest(t)
	code, b := bt.do(bt.admin, "POST", "/api/desktops", map[string]string{"name": "build-check-abc123"})
	if code != 400 || !strings.Contains(string(b), "reserved") {
		t.Errorf("create: %d %s", code, b)
	}
	if code, b := bt.do(bt.admin, "GET", "/api/validate-name?name=build-check-x1", nil); !strings.Contains(string(b), "reserved") {
		t.Errorf("validate: %d %s", code, b)
	}
}

// If the current :latest can't be read, the build stops before any tag
// changes, so the working build keeps it.
func TestUnreadableLatestKeepsTags(t *testing.T) {
	bt := newBuildTest(t)
	if st := bt.build(tools("tools", "img")); st.Status != "done" {
		t.Fatal(st)
	}
	first := bt.e.tagsOf("deskyard-custom/tools")
	bt.e.mu.Lock()
	bt.e.inspectErr = map[string]error{"deskyard-custom/tools:latest": errors.New("daemon hiccup")}
	bt.e.mu.Unlock()
	st := bt.wait(bt.start("POST", "/api/recipes/tools/rebuild", nil))
	bt.e.mu.Lock()
	bt.e.inspectErr = nil
	bt.e.mu.Unlock()
	if st.Status != "failed" || !strings.Contains(st.Error, "daemon hiccup") {
		t.Fatalf("want failed: %+v", st)
	}
	if got := bt.e.tagsOf("deskyard-custom/tools"); fmt.Sprint(got) != fmt.Sprint(first) {
		t.Errorf("tags %v, want %v", got, first)
	}
}
