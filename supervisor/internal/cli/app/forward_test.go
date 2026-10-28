package app

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForwardArgs(t *testing.T) {
	cmd := findCommand("forward")
	if cmd == nil {
		t.Fatal("no forward command")
	}
	got, err := parseForwardArgs(cmd, []string{"3000", "3000:8000", "-R", "5173:5174", "--reverse", "9229"})
	want := []*portMap{{local: 3000, env: 3000}, {local: 3000, env: 8000}, {reverse: true, env: 5173, local: 5174}, {reverse: true, env: 9229, local: 9229}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %v %v", got, err)
	}
	for i := range want {
		if got[i].reverse != want[i].reverse || got[i].local != want[i].local || got[i].env != want[i].env {
			t.Errorf("mapping %d: got %+v, want %+v", i, *got[i], *want[i])
		}
	}
	h := newHarness(t)
	for _, args := range [][]string{{}, {"abc"}, {"0"}, {"70000"}, {"1:2:3"}, {"3000:"}, {":3000"}, {"-R"}, {"3000", "-R"}, {"-R", "-R", "1"}, {"-X", "3000"}, {"30.5"}, {"3000", "4000:3000"}, {"-R", "1:9000", "-R", "2:9000"}} {
		r := h.run(append([]string{"-e", "x", "forward"}, args...)...)
		if r.code != 2 || !strings.Contains(r.stderr, "usage: deskyard forward") {
			t.Errorf("forward %v: exit %d, want 2 (stderr %q)", args, r.code, r.stderr)
		}
	}
	h.inside = true
	r := h.run("forward", "3000")
	if r.code != 2 || !strings.Contains(r.stderr, "forward maps ports between this machine and an environment; choose one with -e ENV") {
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
	if !strings.Contains(stderr.String(), fmt.Sprintf("Linked to alice: alice:%d → 127.0.0.1:%d, %d → alice:%d", outDesktop, outLocal, inLocal, inDesktop)) {
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
	if !strings.Contains(stderr.String(), "Lost alice (environment stopped), retrying in") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if got, err := roundTrip(outLocal, "c"); got != "echo c" {
		t.Errorf("out after reconnect: %q %v", got, err)
	}
	if got, err := roundTrip(inDesktop, "d"); got != "echo d" {
		t.Errorf("in after reconnect: %q %v", got, err)
	}

	// A refused credential ends it.
	f.AnswerForwards(http.StatusUnauthorized)
	f.DropForwards()
	select {
	case c := <-code:
		if c != 1 || !strings.Contains(stderr.String(), "(401): run `deskyard login") {
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

func TestForwardArgsCaps(t *testing.T) {
	cmd := findCommand("forward")
	build := func(ins, outs int) []string {
		var args []string
		for i := 0; i < ins; i++ {
			args = append(args, strconv.Itoa(2000+i))
		}
		for i := 0; i < outs; i++ {
			args = append(args, "-R", strconv.Itoa(5000+i))
		}
		return args
	}
	for _, c := range []struct {
		ins, outs int
		ok        bool
	}{{64, 0, true}, {65, 0, false}, {64, 192, true}, {64, 193, false}, {0, 256, true}, {0, 257, false}} {
		_, err := parseForwardArgs(cmd, build(c.ins, c.outs))
		if (err == nil) != c.ok {
			t.Errorf("%d in, %d out: err %v, want ok=%v", c.ins, c.outs, err, c.ok)
		}
	}
	h := newHarness(t)
	r := h.run(append([]string{"-e", "x", "forward"}, build(65, 0)...)...)
	if r.code != 2 || !strings.Contains(r.stderr, "at most 64 mappings into the environment") {
		t.Errorf("65 ins: exit %d, stderr %q", r.code, r.stderr)
	}
}
