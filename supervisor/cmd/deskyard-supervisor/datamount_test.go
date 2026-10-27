package main

import (
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func TestDataHostPathFindsBind(t *testing.T) {
	self := engine.Container{Mounts: []engine.Mount{
		{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
		{Source: "/srv/deskyard", Target: "/data"},
	}}
	got, err := dataHostPath(self)
	if err != nil || got != "/srv/deskyard" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDataHostPathRefusesMissing(t *testing.T) {
	_, err := dataHostPath(engine.Container{Mounts: []engine.Mount{{Source: "/a", Target: "/other"}}})
	if err == nil || !strings.Contains(err.Error(), "-v <host folder>:/data") {
		t.Fatalf("err = %v", err)
	}
}

func TestDataHostPathRefusesNamedVolume(t *testing.T) {
	_, err := dataHostPath(engine.Container{Mounts: []engine.Mount{
		{Source: "/var/lib/docker/volumes/deskyard/_data", Target: "/data", Volume: "deskyard"},
	}})
	if err == nil || !strings.Contains(err.Error(), "named volume") || !strings.Contains(err.Error(), "-v <host folder>:/data") {
		t.Fatalf("err = %v", err)
	}
}

func TestDataHostPathRefusesNonHostSource(t *testing.T) {
	for _, src := range []string{"", "tmpfs", "relative/dir"} {
		_, err := dataHostPath(engine.Container{Mounts: []engine.Mount{{Source: src, Target: "/data"}}})
		if err == nil || !strings.Contains(err.Error(), "-v <host folder>:/data") {
			t.Fatalf("source %q: err = %v", src, err)
		}
	}
}
