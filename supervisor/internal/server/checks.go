package server

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/imagescan"
	"github.com/zefhemel/deskyard/supervisor/internal/scanbin"
)

// scannerBinary returns the static image scanner for a Linux architecture
// (tests replace it).
var scannerBinary = scanbin.Binary

var scanOptions = imagescan.Options{UserUID: 1000}

// rootScan compares the new image with its base (see imagescan.Compare): the
// new one may add no sudo or doas, no new or changed setuid, setgid or
// file-capability binary, nothing user can write (or links to) where root
// runs from, and no account that gives root. The error names every finding.
// Anything short of two complete scans fails the build.
func (s *Server) rootScan(ctx context.Context, baseID, newID string) error {
	before, err := s.scanImage(ctx, baseID)
	if err != nil {
		return fmt.Errorf("scanning the base: %w", err)
	}
	after, err := s.scanImage(ctx, newID)
	if err != nil {
		return fmt.Errorf("scanning the image: %w", err)
	}
	found := imagescan.Compare(before, after, scanOptions)
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("the image would give user a way to become root:\n  %s", strings.Join(found, "\n  "))
}

// scanImage runs the scanner, as root, in a throwaway container of ref with
// no network. The scanner is copied in and also keeps the container up, so
// nothing in the image runs.
func (s *Server) scanImage(ctx context.Context, ref string) (imagescan.Result, error) {
	img, err := s.Eng.ImageInspect(ctx, ref)
	if err != nil {
		return imagescan.Result{}, err
	}
	bin, err := scannerBinary(img.Arch)
	if err != nil {
		return imagescan.Result{}, err
	}
	exe := "deskyard-image-scan-" + randomID(8)
	id, err := s.Eng.Create(ctx, engine.ContainerSpec{
		Name: "deskyard-scan-" + randomID(8), Image: img.ID, Network: "none", Restart: "no",
		Entrypoint: []string{"/" + exe, "wait"},
	})
	if err != nil {
		return imagescan.Result{}, err
	}
	defer func() { _ = s.Eng.Remove(context.WithoutCancel(ctx), id) }()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: exe, Mode: 0o755, Size: int64(len(bin)), ModTime: time.Unix(0, 0)}); err != nil {
		return imagescan.Result{}, err
	}
	_, _ = tw.Write(bin)
	if err := tw.Close(); err != nil {
		return imagescan.Result{}, err
	}
	if err := s.Eng.CopyToContainer(ctx, id, "/", &buf); err != nil {
		return imagescan.Result{}, fmt.Errorf("copying the scanner in: %w", err)
	}
	if err := s.Eng.Start(ctx, id); err != nil {
		return imagescan.Result{}, err
	}
	out, errOut, code, err := engine.Output(ctx, s.Eng, id, engine.ExecSpec{Cmd: []string{"/" + exe}, User: "0:0"})
	if err != nil {
		return imagescan.Result{}, err
	}
	r, err := imagescan.Parse(out, code)
	if err != nil {
		if msg := strings.TrimSpace(string(errOut)); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return imagescan.Result{}, err
	}
	return r, nil
}

// smokeTestDesktop starts a throwaway desktop of the image, waits until it
// answers (as a new desktop would: 2 minutes), then removes it with its
// folder. New desktops' names can't start with desktops.SmokePrefix, so it is
// never someone's.
func (s *Server) smokeTestDesktop(ctx context.Context, imageID string) error {
	name := desktops.SmokePrefix + randomID(6)
	_, err := s.Desktops.Create(ctx, name, desktops.Settings{Image: imageID}, func(string, float64) {})
	if errors.Is(err, desktops.ErrExists) {
		return err // not ours: leave it
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if rerr := s.Desktops.Remove(bg, name, true); rerr != nil && !errors.Is(rerr, desktops.ErrNotFound) {
		s.Log.Warn("removing the test desktop", "desktop", name, "err", rerr)
	}
	return err
}
