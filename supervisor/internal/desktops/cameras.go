package desktops

import (
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
// container it shows the host's devices too. A desktop gets them all: the
// image picks the first its user can write to for Selkies' webcam.
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

// Webcam reports whether the running desktop has a virtual camera, which
// Selkies feeds from a viewer's webcam.
func (c Desktop) Webcam() bool {
	return c.Running() && slices.ContainsFunc(c.Container.Devices, func(d string) bool { return strings.HasPrefix(d, "/dev/video") })
}
