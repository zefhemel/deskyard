package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type result struct {
	code           int
	stdout, stderr string
}

type harness struct {
	t      *testing.T
	env    map[string]string
	inside bool
	config string
	stdin  string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, env: map[string]string{}, config: filepath.Join(t.TempDir(), "remotes.json")}
}

func (h *harness) run(args ...string) result {
	var out, errb bytes.Buffer
	a := &App{
		Stdin: strings.NewReader(h.stdin), Stdout: &out, Stderr: &errb,
		Getenv:     func(k string) string { return h.env[k] },
		ConfigPath: h.config,
		Inside:     func() bool { return h.inside },
		Version:    "test",
	}
	code := a.Run(context.Background(), args)
	return result{code, out.String(), errb.String()}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file (run go test ./internal/cli/app -update to accept):\n--- got\n%s", path, got)
	}
}

func TestGoldenHelp(t *testing.T) {
	h := newHarness(t)
	r := h.run("--help")
	if r.code != 0 {
		t.Fatalf("--help exit %d", r.code)
	}
	golden(t, "help/overview.txt", r.stdout)
	for _, c := range commands {
		args := append(strings.Fields(c.Name), "--help")
		r := h.run(args...)
		if r.code != 0 || r.stderr != "" {
			t.Errorf("%s --help: exit %d stderr %q", c.Name, r.code, r.stderr)
		}
		golden(t, "help/"+strings.ReplaceAll(c.Name, " ", "_")+".txt", r.stdout)
		// help CMD is the same.
		if r2 := h.run(append([]string{"help"}, strings.Fields(c.Name)...)...); r2.stdout != r.stdout {
			t.Errorf("help %s differs from %s --help", c.Name, c.Name)
		}
	}
	if r := h.run("remote", "--help"); r.code != 0 || !strings.Contains(r.stdout, "remote add") {
		t.Errorf("remote --help: %+v", r)
	}
}

func TestGoldenGuide(t *testing.T) {
	r := newHarness(t).run("guide")
	if r.code != 0 {
		t.Fatalf("exit %d", r.code)
	}
	if n := strings.Count(r.stdout, "\n"); n > 120 {
		t.Errorf("the guide has %d lines; keep it under 120", n)
	}
	golden(t, "guide.txt", r.stdout)
}

func TestGoldenDescribe(t *testing.T) {
	r := newHarness(t).run("describe", "--json")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	var doc struct {
		Commands []struct {
			Name string `json:"name"`
			Args []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"arguments"`
			Output struct{ Type string } `json:"output"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Commands) != len(commands) {
		t.Errorf("describe lists %d commands, want %d", len(doc.Commands), len(commands))
	}
	for _, c := range doc.Commands {
		if c.Output.Type == "" {
			t.Errorf("%s has no output type", c.Name)
		}
	}
	golden(t, "describe.json", r.stdout)
}

func TestSpecIsConsistent(t *testing.T) {
	for _, c := range commands {
		if c.run == nil || c.Summary == "" || c.Desc == "" || len(c.Examples) == 0 || c.Where == "" || c.Exit == "" || c.Output.Type == "" {
			t.Errorf("%s: incomplete spec", c.Name)
		}
		if !strings.HasPrefix(c.Usage, c.Name) {
			t.Errorf("%s: usage %q should start with the name", c.Name, c.Usage)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	h.inside = true
	for _, args := range [][]string{{}, {"bogus"}, {"click", "1"}, {"zoom", "50", "50", "10", "10"}, {"remote"}, {"remote", "bogus"},
		{"exec"}, {"cp", "a"}, {"remote", "add", "x"}, {"--nope"}, {"-d"}, {"status", "--bogus"}, {"exec", "--cwd"}} {
		r := h.run(args...)
		if r.code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
		if r.stderr == "" {
			t.Errorf("%v: no message", args)
		}
	}
}

func TestNoRemoteChosen(t *testing.T) {
	h := newHarness(t)
	r := h.run("screenshot")
	if r.code != 1 || !strings.Contains(r.stderr, "no remote chosen: run `deskyard remote list`, then use -d NAME") {
		t.Errorf("%+v", r)
	}
	r = h.run("-d", "nope", "size")
	if r.code != 1 || !strings.Contains(r.stderr, `no remote named "nope"`) {
		t.Errorf("%+v", r)
	}
	h.env["DESKYARD_URL"] = "https://h/alice"
	r = h.run("size")
	if r.code != 1 || !strings.Contains(r.stderr, "DESKYARD_TOKEN") {
		t.Errorf("%+v", r)
	}
}

func remoteHarness(t *testing.T) (*harness, *clitest.Fake) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake runs commands with sh")
	}
	f := clitest.New(t, "alice")
	h := newHarness(t)
	if r := h.run("remote", "add", "al", f.DesktopURL(), "--token", clitest.Token); r.code != 0 {
		t.Fatalf("remote add: %+v", r)
	}
	return h, f
}

func TestRemoteCommands(t *testing.T) {
	f := clitest.New(t, "alice")
	h := newHarness(t)

	r := h.run("remote", "add", "al", f.DesktopURL(), "--token", "dyt_bad_token")
	if r.code != 1 || !strings.Contains(r.stderr, "token rejected (401): ask someone with access to alice") {
		t.Errorf("bad token: %+v", r)
	}
	r = h.run("remote", "add", "al", f.URL+"/bob", "--token", clitest.Token)
	if r.code != 1 || !strings.Contains(r.stderr, `this token is for the desktop "alice", not "bob"`) {
		t.Errorf("wrong desktop: %+v", r)
	}
	r = h.run("remote", "add", "al", f.DesktopURL(), "--token", clitest.Token)
	if r.code != 0 || !strings.Contains(r.stdout, "Added remote al") || !strings.Contains(r.stdout, "default") {
		t.Errorf("add: %+v", r)
	}
	// A URL without the desktop's name learns it from the token.
	r = h.run("remote", "add", "two", f.URL, "--token", clitest.Token)
	if r.code != 0 {
		t.Errorf("add two: %+v", r)
	}
	cfg, err := remote.Load(h.config)
	if err != nil || cfg.Remotes["two"].Desktop != "alice" || cfg.Default != "al" {
		t.Errorf("config: %+v %v", cfg, err)
	}

	r = h.run("remote", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "* al") || strings.Contains(r.stdout, "secret") {
		t.Errorf("list: %+v", r)
	}
	r = h.run("remote", "list", "--json")
	var rows []map[string]any
	if json.Unmarshal([]byte(r.stdout), &rows); len(rows) != 2 || rows[0]["default"] != true {
		t.Errorf("list --json: %s", r.stdout)
	}
	if r = h.run("remote", "default", "two"); r.code != 0 {
		t.Errorf("default: %+v", r)
	}
	if r = h.run("remote", "default"); strings.TrimSpace(r.stdout) != "two" {
		t.Errorf("default shows %q", r.stdout)
	}
	if r = h.run("remote", "remove", "two"); r.code != 0 {
		t.Errorf("remove: %+v", r)
	}
	if r = h.run("remote", "remove", "two"); r.code != 1 {
		t.Errorf("second remove: %+v", r)
	}

	// Stopped: the token works, so the remote is saved with a note.
	f.Stopped = true
	r = h.run("remote", "add", "st", f.DesktopURL(), "--token", clitest.Token)
	if r.code != 0 || !strings.Contains(r.stdout, "alice is stopped") {
		t.Errorf("add stopped: %+v", r)
	}
	r = h.run("-d", "st", "status")
	if r.code != 1 || !strings.Contains(r.stderr, "alice is stopped; someone with access can start it") {
		t.Errorf("status stopped: %+v", r)
	}
}

func TestStatusRemote(t *testing.T) {
	h, _ := remoteHarness(t)
	r := h.run("status")
	if r.code != 0 || !strings.Contains(r.stdout, "name:   alice") || !strings.Contains(r.stdout, "screen: 1280x800") {
		t.Errorf("%+v", r)
	}
	r = h.run("status", "--json")
	var st map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &st); err != nil || st["state"] != "running" || st["remote"] != "al" {
		t.Errorf("%s %v", r.stdout, err)
	}
}

func TestEnvRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	f := clitest.New(t, "alice")
	h := newHarness(t)
	h.env["DESKYARD_URL"] = f.DesktopURL()
	h.env["DESKYARD_TOKEN"] = clitest.Token
	if r := h.run("size"); r.code != 0 || r.stdout != "1280 800\n" {
		t.Errorf("%+v", r)
	}
}

func TestExecRemote(t *testing.T) {
	h, f := remoteHarness(t)
	r := h.run("exec", "--", "sh", "-c", "echo hi; echo oops >&2; exit 7")
	if r.code != 7 || r.stdout != "hi\n" || r.stderr != "oops\n" {
		t.Errorf("%+v", r)
	}
	r = h.run("exec", "--json", "-e", "A=1", "--cwd", f.Dir, "sh", "-c", "echo $A; printf '\\377' >&2")
	var obj map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &obj); err != nil || r.code != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if obj["stdout"] != "1\n" || obj["exit"] != float64(0) || obj["stderrBase64"] != "/w==" {
		t.Errorf("json: %v", obj)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if last.Cwd != f.Dir || last.Env["A"] != "1" {
		t.Errorf("request: %+v", last)
	}
	h.stdin = "piped input"
	if r := h.run("exec", "--", "cat"); r.stdout != "piped input" {
		t.Errorf("stdin: %+v", r)
	}
}

func TestDesktopRemote(t *testing.T) {
	h, f := remoteHarness(t)
	if r := h.run("size"); r.code != 0 || r.stdout != "1280 800\n" {
		t.Errorf("size: %+v", r)
	}
	if r := h.run("screenshot"); r.stdout != string(clitest.FakePNG) {
		t.Errorf("screenshot to stdout: %q", r.stdout)
	}
	file := filepath.Join(t.TempDir(), "shot.png")
	h.env["DESKYARD_SETTLE"] = "0"
	if r := h.run("zoom", "0", "0", "10", "10", file); r.code != 0 || r.stdout != "" {
		t.Errorf("zoom to file: %+v", r)
	}
	if b, _ := os.ReadFile(file); string(b) != string(clitest.FakePNG) {
		t.Errorf("file = %q", b)
	}
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	if strings.Join(last.Argv, " ") != "deskyard zoom 0 0 10 10 -" || last.Env["DESKYARD_SETTLE"] != "0" {
		t.Errorf("zoom request: %+v", last)
	}
	// Usage errors are caught before going remote.
	n := len(f.Requests())
	if r := h.run("click", "1"); r.code != 2 || len(f.Requests()) != n {
		t.Errorf("click 1: %+v", r)
	}
	// The remote's exit code passes through.
	f.Desktop = func(args []string, stdout, stderr io.Writer) int {
		io.WriteString(stderr, "no display\n")
		return 5
	}
	if r := h.run("key", "Return"); r.code != 5 || r.stderr != "no display\n" {
		t.Errorf("key: %+v", r)
	}
}

func TestCpRemote(t *testing.T) {
	h, f := remoteHarness(t)
	local := t.TempDir()
	os.WriteFile(filepath.Join(local, "a.txt"), []byte("A"), 0o644)
	if r := h.run("cp", filepath.Join(local, "a.txt"), "al:"+f.Dir+"/"); r.code != 0 {
		t.Fatalf("upload: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Dir, "a.txt")); string(b) != "A" {
		t.Errorf("uploaded %q", b)
	}
	if r := h.run("cp", ":"+f.Dir+"/a.txt", filepath.Join(local, "b.txt")); r.code != 0 {
		t.Fatalf("download: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(local, "b.txt")); string(b) != "A" {
		t.Errorf("downloaded %q", b)
	}
	if r := h.run("cp", "x", "y"); r.code != 2 || !strings.Contains(r.stderr, "NAME:/path") {
		t.Errorf("no remote side: %+v", r)
	}
	if r := h.run("cp", "al:/a", "al:/b"); r.code != 2 {
		t.Errorf("two remotes: %+v", r)
	}
}

func TestBrowserConnectInside(t *testing.T) {
	h := newHarness(t)
	h.inside = true
	r := h.run("browser", "connect")
	if r.code != 0 || r.stdout != "http://127.0.0.1:9221\n" {
		t.Errorf("%+v", r)
	}
}

func TestExecLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	h := newHarness(t)
	h.inside = true
	r := h.run("exec", "--json", "--", "sh", "-c", "echo local; exit 4")
	var obj map[string]any
	json.Unmarshal([]byte(r.stdout), &obj)
	if obj["stdout"] != "local\n" || obj["exit"] != float64(4) {
		t.Errorf("%+v", r)
	}
	if r := h.run("exec", "--", "sh", "-c", "exit 3"); r.code != 3 {
		t.Errorf("exit code: %+v", r)
	}
	if r := h.run("exec", "--", "no-such-command-xyz"); r.code != 127 {
		t.Errorf("not found: %+v", r)
	}
}

func TestForwardArgs(t *testing.T) {
	cmd := findCommand("forward")
	if cmd == nil {
		t.Fatal("no forward command")
	}
	got, err := parseForwardArgs(cmd, []string{"3000", "3000:8000", "-R", "5173:5174", "--reverse", "9229"})
	want := []*portMap{{local: 3000, desktop: 3000}, {local: 3000, desktop: 8000}, {reverse: true, desktop: 5173, local: 5174}, {reverse: true, desktop: 9229, local: 9229}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %v %v", got, err)
	}
	for i := range want {
		if got[i].reverse != want[i].reverse || got[i].local != want[i].local || got[i].desktop != want[i].desktop {
			t.Errorf("mapping %d: got %+v, want %+v", i, *got[i], *want[i])
		}
	}
	h := newHarness(t)
	for _, args := range [][]string{{}, {"abc"}, {"0"}, {"70000"}, {"1:2:3"}, {"3000:"}, {":3000"}, {"-R"}, {"3000", "-R"}, {"-R", "-R", "1"}, {"-X", "3000"}, {"30.5"}} {
		r := h.run(append([]string{"-d", "x", "forward"}, args...)...)
		if r.code != 2 || !strings.Contains(r.stderr, "usage: deskyard forward") {
			t.Errorf("forward %v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
	}
	h.inside = true
	r := h.run("forward", "3000")
	if r.code != 2 || !strings.Contains(r.stderr, "forward maps ports between this machine and a desktop; choose one with -d NAME") {
		t.Errorf("inside: %+v", r)
	}
	if r := h.run("forward", "--help"); r.code != 0 || !strings.Contains(r.stdout, "-R") {
		t.Errorf("--help: %+v", r)
	}
}

// syncBuf is a bytes.Buffer safe to read while a command writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// start runs a long-running command; it ends when ctx does.
func (h *harness) start(ctx context.Context, args ...string) (stdout, stderr *syncBuf, code <-chan int) {
	stdout, stderr = &syncBuf{}, &syncBuf{}
	a := &App{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
		Getenv:     func(k string) string { return h.env[k] },
		ConfigPath: h.config,
		Inside:     func() bool { return h.inside },
		Version:    "test",
	}
	ch := make(chan int, 1)
	go func() { ch <- a.Run(ctx, args) }()
	return stdout, stderr, ch
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// echoServer echoes each line back with a prefix.
func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					fmt.Fprintf(c, "echo %s\n", sc.Text())
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func roundTrip(port int, msg string) (string, error) {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(c, "%s\n", msg)
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestForwardReconnects(t *testing.T) {
	h, f := remoteHarness(t)
	old := forwardBackoff
	forwardBackoff = func(n int) time.Duration { return time.Duration(20<<n) * time.Millisecond }
	t.Cleanup(func() { forwardBackoff = old })

	outDesktop := echoServer(t) // "in the desktop": the fake's helper side dials this machine
	outLocal := freePort(t)
	inLocal := echoServer(t)
	inDesktop := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, stderr, code := h.start(ctx, "forward", "-R", fmt.Sprintf("%d:%d", outDesktop, outLocal), fmt.Sprintf("%d:%d", inLocal, inDesktop))

	live := func(n int) func() bool {
		return func() bool {
			out := stdout.String()
			return strings.Count(out, fmt.Sprintf("out %d 127.0.0.1:%d\n", outDesktop, outLocal)) >= n &&
				strings.Count(out, fmt.Sprintf("in %d %d\n", inLocal, inDesktop)) >= n
		}
	}
	waitFor(t, "the mappings", 5*time.Second, live(1))
	if got := f.ForwardStarts()[0]; got != fmt.Sprintf(`{"forwards":[{"direction":"out","port":%d},{"direction":"in","port":%d}]}`, outDesktop, inDesktop) {
		t.Errorf("first message %s", got)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("Connected to alice: alice:%d → 127.0.0.1:%d, %d → alice:%d", outDesktop, outLocal, inLocal, inDesktop)) {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(outLocal, "a"); got != "echo a" {
		t.Errorf("out: %q %v", got, err)
	}
	if got, err := roundTrip(inDesktop, "b"); got != "echo b" {
		t.Errorf("in: %q %v", got, err)
	}

	// Dropped, then stopped twice: the local listener stays bound throughout.
	f.AnswerForwards(409, 409)
	f.DropForwards()
	for !live(2)() {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(outLocal)); err == nil {
			l.Close()
			t.Fatal("the local listener was released while reconnecting")
		}
		select {
		case c := <-code:
			t.Fatalf("exited %d: %s", c, stderr.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if n := len(f.ForwardStarts()); n != 2 {
		t.Errorf("%d connections, want 2", n)
	}
	if !strings.Contains(stderr.String(), "Lost alice (desktop stopped), retrying in") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(outLocal, "c"); got != "echo c" {
		t.Errorf("out after reconnect: %q %v", got, err)
	}
	if got, err := roundTrip(inDesktop, "d"); got != "echo d" {
		t.Errorf("in after reconnect: %q %v", got, err)
	}

	// A refused token ends it.
	f.AnswerForwards(http.StatusUnauthorized)
	f.DropForwards()
	select {
	case c := <-code:
		if c != 1 || !strings.Contains(stderr.String(), "token rejected (401)") {
			t.Errorf("exit %d, stderr %s", c, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still running after a 401")
	}
}

func TestForwardPortInUse(t *testing.T) {
	h, _ := remoteHarness(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	q := taken.Addr().(*net.TCPAddr).Port
	inLocal := echoServer(t)
	inDesktop := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, stderr, code := h.start(ctx, "forward", "-R", fmt.Sprintf("%d:%d", echoServer(t), q), fmt.Sprintf("%d:%d", inLocal, inDesktop))
	waitFor(t, "the in mapping", 5*time.Second, func() bool { return strings.Contains(stdout.String(), fmt.Sprintf("in %d %d\n", inLocal, inDesktop)) })
	if !strings.Contains(stderr.String(), fmt.Sprintf("127.0.0.1:%d: port in use", q)) {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(inDesktop, "x"); got != "echo x" {
		t.Errorf("in: %q %v", got, err)
	}
	cancel()
	if c := <-code; c != 0 {
		t.Errorf("exit %d after Ctrl-C", c)
	}
}
