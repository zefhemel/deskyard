// Package helper runs short commands as root in throwaway containers on the
// engine, "helpers": for folders the supervisor (UID 1000) can't change or
// can't see itself, such as an environment's root-owned files or a host
// volume's folder on the engine host, and for rclone and restic.
//
// Every helper is labelled as this supervisor instance's, waits in sleep
// while its command runs through exec (so its exit code and stderr are
// known), and is removed when the command returns. Before its first helper a
// Runner removes the ones a crash of this instance left behind.
package helper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Image is every helper's image, pinned: rclone's, whose busybox also
// serves the plain folder commands, so one image serves every helper.
const Image = "rclone/rclone:1.75.2"

// Labels on helpers.
const (
	LabelHelper   = "deskyard.helper"   // the helper's purpose
	LabelInstance = "deskyard.instance" // the supervisor instance that runs it
)

// Purposes: a helper's LabelHelper and the prefix of its name. RemoveStale
// removes helpers of these.
const (
	Storage    = "storage"     // rclone and restic
	Folders    = "folders"     // making and removing folders as root
	MountCheck = "mount-check" // checking sources on the engine host
)

var purposes = []string{Storage, Folders, MountCheck}

// Spec is one command in a helper.
type Spec struct {
	Purpose string // one of the purposes
	Image   string // "" is Image
	Mounts  []engine.Mount
	Network string // "" is the engine's default network; "none" for none
	// Cmd runs as root, with Env.
	Cmd []string
	Env []string
	// Stdout receives the command's output; nil discards it.
	Stdout                io.Writer
	MemoryBytes, NanoCPUs int64 // 0: no limit
	// Prepare, when set, runs between creating the helper (id) and starting
	// it, as to copy files into it.
	Prepare func(ctx context.Context, id string) error
}

// Result is a finished command: its exit code and the end of its stderr.
type Result struct {
	Code   int
	Stderr string
}

// Err is nil for exit code 0, and otherwise what failed, with the last line
// of stderr or the exit code.
func (r Result) Err(what string) error {
	if r.Code == 0 {
		return nil
	}
	msg := strings.TrimSpace(r.Stderr)
	if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:])
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", r.Code)
	}
	return fmt.Errorf("%s: %s", what, msg)
}

// Runner runs helpers on Eng for the supervisor Instance.
type Runner struct {
	Eng      engine.Engine
	Instance string

	pullMu    sync.Mutex
	cleanOnce sync.Once
}

// removeTimeout bounds removing a helper, which never inherits the caller's
// cancellation.
const removeTimeout = 10 * time.Second

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Ensure pulls ref unless the engine has it.
func (r *Runner) Ensure(ctx context.Context, ref string) error {
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
	if _, err := r.Eng.ImageInspect(ctx, ref); errors.Is(err, engine.ErrNotFound) {
		if err := r.Eng.Pull(ctx, ref, nil); err != nil {
			return fmt.Errorf("pulling %s: %w", ref, err)
		}
	} else if err != nil {
		return err
	}
	return nil
}

// Run runs spec's command in a new helper, removed when it returns. A
// non-zero exit is a Result, not an error. An error removing the helper is
// joined to the outcome: a helper left behind keeps its mounts.
func (r *Runner) Run(ctx context.Context, spec Spec) (res Result, err error) {
	r.cleanOnce.Do(func() { r.RemoveStale(ctx) })
	image := spec.Image
	if image == "" {
		image = Image
	}
	if err := r.Ensure(ctx, image); err != nil {
		return Result{}, err
	}
	name := "deskyard-" + spec.Purpose + "-" + randomHex(4)
	id, err := r.Eng.Create(ctx, engine.ContainerSpec{
		Name: name, Image: image, Entrypoint: []string{"sleep"}, Cmd: []string{"86400"},
		Mounts: slices.Clone(spec.Mounts), Network: spec.Network, MemoryBytes: spec.MemoryBytes, NanoCPUs: spec.NanoCPUs,
		NoHealthcheck: true, Restart: "no", Labels: r.labels(spec.Purpose),
	})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), removeTimeout)
		defer done()
		if rerr := r.Eng.Remove(cleanup, id); rerr != nil && !errors.Is(rerr, engine.ErrNotFound) {
			err = errors.Join(err, fmt.Errorf("removing helper %s: %w", name, rerr))
		}
	}()
	if spec.Prepare != nil {
		if err := spec.Prepare(ctx, id); err != nil {
			return Result{}, err
		}
	}
	if err := r.Eng.Start(ctx, id); err != nil {
		return Result{}, fmt.Errorf("starting helper: %w", err)
	}
	out := spec.Stdout
	if out == nil {
		out = io.Discard
	}
	run, err := engine.Run(ctx, r.Eng, id, engine.ExecSpec{Cmd: slices.Clone(spec.Cmd), User: "0", Env: slices.Clone(spec.Env)}, engine.RunOptions{
		StderrLimit: 1 << 20,
		Consume: func(rd io.Reader) error {
			_, err := io.Copy(out, rd)
			return err
		},
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Code: run.Code, Stderr: tail(string(run.Stderr), 8<<10)}, nil
}

func (r *Runner) labels(purpose string) map[string]string {
	return map[string]string{LabelHelper: purpose, LabelInstance: r.Instance}
}

// RemoveStale removes this instance's helpers that a crash or kill left
// behind, which would otherwise keep folders mounted and repository locks
// held. Run calls it once, before the runner's first helper.
func (r *Runner) RemoveStale(ctx context.Context) {
	for _, p := range purposes {
		cs, err := r.Eng.List(ctx, r.labels(p))
		if err != nil {
			continue
		}
		for _, c := range cs {
			_ = r.Eng.Remove(ctx, c.ID)
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
