package execws

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

func setup(t *testing.T) (*clitest.Fake, *remote.Client) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake runs commands with sh")
	}
	f := clitest.New(t, "alice")
	return f, &remote.Client{Alias: "alice", Base: f.URL, Computer: "alice", Token: clitest.Credential}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestStdoutStderrExit(t *testing.T) {
	f, c := setup(t)
	var out, errb bytes.Buffer
	code, err := Run(ctx(t), c, Request{Argv: []string{"sh", "-c", "echo out; echo err >&2; echo $FOO; pwd; exit 3"}, Env: map[string]string{"FOO": "bar"}},
		IO{Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3", code)
	}
	if !strings.HasPrefix(out.String(), "out\nbar\n") {
		t.Errorf("stdout = %q", out.String())
	}
	if errb.String() != "err\n" {
		t.Errorf("stderr = %q", errb.String())
	}
	reqs := f.Requests()
	if len(reqs) != 1 || reqs[0].Argv[0] != "sh" || reqs[0].Env["FOO"] != "bar" || reqs[0].TTY {
		t.Errorf("requests = %+v", reqs)
	}
}

func TestStdinAndEOF(t *testing.T) {
	_, c := setup(t)
	big := bytes.Repeat([]byte("0123456789"), 20000) // several chunks
	var out bytes.Buffer
	code, err := Run(ctx(t), c, Request{Argv: []string{"cat"}}, IO{Stdin: bytes.NewReader(big), Stdout: &out})
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if !bytes.Equal(out.Bytes(), big) {
		t.Errorf("cat returned %d bytes, want %d", out.Len(), len(big))
	}
}

func TestNilStdinClosesRightAway(t *testing.T) {
	_, c := setup(t)
	var out bytes.Buffer
	code, err := Run(ctx(t), c, Request{Argv: []string{"sh", "-c", "cat; echo done"}}, IO{Stdout: &out})
	if err != nil || code != 0 || out.String() != "done\n" {
		t.Fatalf("code %d err %v out %q", code, err, out.String())
	}
}

func TestLargeOutput(t *testing.T) {
	_, c := setup(t)
	var out bytes.Buffer
	code, err := Run(ctx(t), c, Request{Argv: []string{"sh", "-c", "head -c 3000000 /dev/zero"}}, IO{Stdout: &out})
	if err != nil || code != 0 || out.Len() != 3000000 {
		t.Fatalf("code %d err %v len %d", code, err, out.Len())
	}
}

func TestServerError(t *testing.T) {
	_, c := setup(t)
	_, err := Run(ctx(t), c, Request{Argv: []string{"/no/such/program"}}, IO{})
	if err == nil || !strings.Contains(err.Error(), "alice:") {
		t.Fatalf("want the server's error, got %v", err)
	}
}

func TestAuthAndStopped(t *testing.T) {
	f, c := setup(t)
	bad := *c
	bad.Token = "dyc_0000dead_nope"
	if _, err := Run(ctx(t), &bad, Request{Argv: []string{"true"}}, IO{}); !remote.IsStatus(err, 401) {
		t.Errorf("want 401, got %v", err)
	}
	f.Stopped = true
	_, err := Run(ctx(t), c, Request{Argv: []string{"true"}}, IO{})
	if !remote.IsStatus(err, 409) || !strings.Contains(err.Error(), "alice is stopped") {
		t.Errorf("want 409, got %v", err)
	}
}

func TestResizeIsSent(t *testing.T) {
	_, c := setup(t)
	resize := make(chan [2]int, 1)
	resize <- [2]int{100, 40}
	code, err := Run(ctx(t), c, Request{Argv: []string{"sh", "-c", "sleep 0.2"}, TTY: true, Cols: 80, Rows: 24}, IO{Resize: resize})
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
}
