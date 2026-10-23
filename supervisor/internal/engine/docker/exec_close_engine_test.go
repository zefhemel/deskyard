//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Closing input must let the command finish and both output streams drain;
// only Process.Close disposes the entire attachment.
func TestExecStdinEOFDrainsOutput(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, err := d.Create(ctx, engine.ContainerSpec{Name: "deskyard-exec-eof-" + strings.ToLower(rand.Text()[:8]), Image: "alpine:3.22", Network: "none", Cmd: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(context.Background(), id)
	if err := d.Start(ctx, id); err != nil {
		t.Fatal(err)
	}
	p, err := d.Exec(ctx, id, engine.ExecSpec{Stdin: true, Cmd: []string{"sh", "-c", "cat; printf after-eof; printf diagnostic >&2; exit 7"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	stop := context.AfterFunc(ctx, p.Close)
	defer stop()
	var out, errout []byte
	var outErr, stderrErr error
	var readers sync.WaitGroup
	readers.Go(func() { out, outErr = io.ReadAll(p.Stdout) })
	readers.Go(func() { errout, stderrErr = io.ReadAll(p.Stderr) })
	if _, err := io.WriteString(p.Stdin, "input:"); err != nil {
		t.Fatal(err)
	}
	if err := p.Stdin.Close(); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
	code, err := p.Wait()
	if string(out) != "input:after-eof" || string(errout) != "diagnostic" || outErr != nil || stderrErr != nil || code != 7 || err != nil {
		t.Fatalf("stdout=%q (%v), stderr=%q (%v), exit=%d (%v)", out, outErr, errout, stderrErr, code, err)
	}
}
