package storage

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// The helper's images, pinned. restic is taken out of its image into
// containers of rclone's, so Deskyard publishes no image of its own.
const (
	RcloneImage = "rclone/rclone:1.75.2"
	ResticImage = "restic/restic:0.19.1"
	resticPath  = "/usr/bin/restic" // in ResticImage
)

// Call is one rclone or restic command in a helper container. Env holds no
// secrets: credentials reach the helper only as files in its mounts.
type Call struct {
	Mounts []engine.Mount
	Env    map[string]string
	Args   []string
	Stdout io.Writer // streamed; nil discards
}

// Result is a finished command: its exit code and the end of its stderr.
type Result struct {
	Code   int
	Stderr string
}

// Runner runs Calls. A non-zero exit is a Result, not an error.
type Runner interface {
	Run(ctx context.Context, c Call) (Result, error)
}

// EngineRunner runs each Call in a new helper container of RcloneImage,
// removed when the call returns. restic comes from ResticImage: extracted
// once into BinDir (a host folder) and mounted read-only, or, without a
// BinDir, copied into each container.
type EngineRunner struct {
	Eng    engine.Engine
	BinDir string

	pullMu sync.Mutex
}

// resticBinary is the restic binary extracted into BinDir, made on first use.
func (r *EngineRunner) resticBinary(ctx context.Context) (string, error) {
	path := filepath.Join(r.BinDir, "restic-"+strings.TrimPrefix(ResticImage, "restic/restic:"))
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		return path, nil
	}
	rc, err := r.Eng.CopyFromImage(ctx, ResticImage, resticPath)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	for {
		h, err := tr.Next()
		if err != nil {
			return "", fmt.Errorf("no restic in %s: %w", ResticImage, err)
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "restic" {
			continue
		}
		if err := os.MkdirAll(r.BinDir, 0o755); err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(r.BinDir, ".restic-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, io.LimitReader(tr, 256<<20)); err != nil {
			tmp.Close()
			return "", err
		}
		if err := tmp.Chmod(0o755); err != nil {
			tmp.Close()
			return "", err
		}
		if err := tmp.Close(); err != nil {
			return "", err
		}
		return path, os.Rename(tmp.Name(), path)
	}
}

func (r *EngineRunner) ensureImages(ctx context.Context) error {
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
	for _, ref := range []string{RcloneImage, ResticImage} {
		if _, err := r.Eng.ImageInspect(ctx, ref); errors.Is(err, engine.ErrNotFound) {
			if err := r.Eng.Pull(ctx, ref, nil); err != nil {
				return fmt.Errorf("pulling %s: %w", ref, err)
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (r *EngineRunner) Run(ctx context.Context, c Call) (Result, error) {
	if err := r.ensureImages(ctx); err != nil {
		return Result{}, err
	}
	mounts := slices.Clone(c.Mounts)
	if r.BinDir != "" {
		r.pullMu.Lock()
		bin, err := r.resticBinary(ctx)
		r.pullMu.Unlock()
		if err != nil {
			return Result{}, fmt.Errorf("preparing restic: %w", err)
		}
		mounts = append(mounts, engine.Mount{Source: bin, Target: "/usr/local/bin/restic", ReadOnly: true, MustExist: true})
	}
	raw := make([]byte, 4)
	_, _ = rand.Read(raw)
	name := "deskyard-storage-" + hex.EncodeToString(raw)
	if _, err := r.Eng.Create(ctx, engine.ContainerSpec{
		Name: name, Image: RcloneImage, Entrypoint: []string{"sleep"}, Cmd: []string{"86400"},
		Mounts: mounts, NoHealthcheck: true, Restart: "no",
	}); err != nil {
		return Result{}, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		_ = r.Eng.Remove(cleanup, name)
	}()
	if r.BinDir == "" {
		bin, err := r.Eng.CopyFromImage(ctx, ResticImage, resticPath)
		if err != nil {
			return Result{}, fmt.Errorf("copying restic: %w", err)
		}
		err = r.Eng.CopyToContainer(ctx, name, "/usr/local/bin", bin)
		bin.Close()
		if err != nil {
			return Result{}, fmt.Errorf("copying restic: %w", err)
		}
	}
	if err := r.Eng.Start(ctx, name); err != nil {
		return Result{}, err
	}
	env := make([]string, 0, len(c.Env))
	for k, v := range c.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	out := c.Stdout
	if out == nil {
		out = io.Discard
	}
	res, err := engine.Run(ctx, r.Eng, name, engine.ExecSpec{Cmd: slices.Clone(c.Args), User: "0", Env: env}, engine.RunOptions{
		StderrLimit: 1 << 20,
		Consume: func(rd io.Reader) error {
			_, err := io.Copy(out, rd)
			return err
		},
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Code: res.Code, Stderr: tail(string(res.Stderr), 8<<10)}, nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
