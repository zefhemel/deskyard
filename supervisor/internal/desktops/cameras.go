package desktops

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var videoDevice = regexp.MustCompile(`^video[0-9]+$`)

// Cameras lists the host's virtual cameras, the v4l2loopback devices, as
// /dev paths: the video devices under sys/devices/virtual/video4linux (a real
// camera sits under its bus, not there). sys is the host's /sys; inside a
// container it shows the host's devices too. An admin gives a desktop one of
// them (Settings.Camera), which only that desktop then has.
func Cameras(sys string) []string {
	entries, err := os.ReadDir(filepath.Join(sys, "devices", "virtual", "video4linux"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if videoDevice.MatchString(e.Name()) {
			out = append(out, "/dev/"+e.Name())
		}
	}
	num := func(dev string) int { n, _ := strconv.Atoi(strings.TrimPrefix(dev, "/dev/video")); return n }
	slices.SortFunc(out, func(a, b string) int { return num(a) - num(b) })
	return out
}

// Cameras that can't be given to a desktop.
var (
	ErrCameraUnknown = errors.New("not one of this host's virtual cameras")
	ErrCameraTaken   = errors.New("another desktop has that camera")
)

// LocalEngine reports whether an engine address (DOCKER_HOST style; "" for
// the platform's default socket) is a unix socket on this machine.
func LocalEngine(host string) bool {
	return host == "" || strings.HasPrefix(host, "unix://")
}

// HostCameras lists the host's virtual cameras, or nil when the engine
// isn't on this machine (its host's cameras can't be seen from here).
func (s *Service) HostCameras() []string {
	if !s.Cfg.LocalEngine {
		return nil
	}
	return Cameras(cmp.Or(s.Cfg.SysDir, "/sys"))
}

// CheckCamera reports why a desktop can't have that camera: it isn't one of
// the host's, or another desktop has it. A desktop has its camera to itself.
func (s *Service) CheckCamera(ctx context.Context, name, camera string) error {
	s.camMu.Lock()
	defer s.camMu.Unlock()
	return s.cameraFree(ctx, name, camera)
}

// cameraFree is CheckCamera with camMu held.
func (s *Service) cameraFree(ctx context.Context, name, camera string) error {
	if !slices.Contains(s.HostCameras(), camera) {
		return fmt.Errorf("%s: %w", camera, ErrCameraUnknown)
	}
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Name != name && c.Settings.Camera == camera {
			return fmt.Errorf("%w: %s has %s", ErrCameraTaken, c.Name, camera)
		}
	}
	return nil
}

func cameraOf(devices []string) string {
	for _, d := range devices {
		if strings.HasPrefix(d, "/dev/video") {
			return d
		}
	}
	return ""
}

// Webcam reports whether the running desktop has a virtual camera, which
// Selkies feeds from a viewer's webcam.
func (c Desktop) Webcam() bool {
	return c.Running() && c.Settings.Camera != ""
}
