package storage

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// The helper's images, pinned. restic is taken out of its image into
// helpers of rclone's, so Deskyard publishes no image of its own.
const (
	RcloneImage = helper.Image
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

// EngineRunner runs each Call in a new storage helper of RcloneImage
// (helper.Runner), removed when the call returns. restic comes from
// ResticImage: extracted once into BinDir (in the data folder) and mounted
// read-only, or, without a BinDir, copied into each helper.
type EngineRunner struct {
	Helpers *helper.Runner
	BinDir  string

	binMu sync.Mutex
}

// resticBinary is the restic binary extracted into BinDir, made on first use.
func (r *EngineRunner) resticBinary(ctx context.Context) (string, error) {
	path := filepath.Join(r.BinDir, "restic-"+strings.TrimPrefix(ResticImage, "restic/restic:"))
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		return path, nil
	}
	rc, err := r.Helpers.Eng.CopyFromImage(ctx, ResticImage, resticPath)
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

func (r *EngineRunner) Run(ctx context.Context, c Call) (Result, error) {
	if err := r.Helpers.Ensure(ctx, ResticImage); err != nil {
		return Result{}, err
	}
	spec := helper.Spec{Purpose: helper.Storage, Image: RcloneImage, Mounts: slices.Clone(c.Mounts), Cmd: c.Args, Stdout: c.Stdout}
	if r.BinDir != "" {
		r.binMu.Lock()
		bin, err := r.resticBinary(ctx)
		r.binMu.Unlock()
		if err != nil {
			return Result{}, fmt.Errorf("preparing restic: %w", err)
		}
		spec.Mounts = append(spec.Mounts, engine.Mount{Source: bin, Target: "/usr/local/bin/restic", ReadOnly: true, MustExist: true})
	} else {
		spec.Prepare = func(ctx context.Context, id string) error {
			bin, err := r.Helpers.Eng.CopyFromImage(ctx, ResticImage, resticPath)
			if err != nil {
				return fmt.Errorf("copying restic: %w", err)
			}
			defer bin.Close()
			if err := r.Helpers.Eng.CopyToContainer(ctx, id, "/usr/local/bin", bin); err != nil {
				return fmt.Errorf("copying restic: %w", err)
			}
			return nil
		}
	}
	for k, v := range c.Env {
		spec.Env = append(spec.Env, k+"="+v)
	}
	sort.Strings(spec.Env)
	res, err := r.Helpers.Run(ctx, spec)
	return Result{Code: res.Code, Stderr: res.Stderr}, err
}
