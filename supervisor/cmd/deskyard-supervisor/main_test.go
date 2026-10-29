package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDataDir(t *testing.T) {
	if err := checkDataDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, filepath.Join(t.TempDir(), "missing")} {
		err := checkDataDir(p)
		if err == nil || !strings.Contains(err.Error(), "-v <host folder>:/data") {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

func TestEngineError(t *testing.T) {
	cause := errors.New("engine down")
	if err := engineError("tcp://h:2375", cause); !errors.Is(err, cause) || strings.Contains(err.Error(), "group") {
		t.Fatalf("tcp: %v", err)
	}
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "d.sock")
	if err := engineError("unix://"+path, cause); !errors.Is(err, cause) {
		t.Fatalf("missing socket: %v", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := engineError("unix://"+path, cause); !errors.Is(err, cause) {
		t.Fatalf("usable socket: %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores socket permissions")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	err = engineError("unix://"+path, cause)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "group") {
		t.Fatalf("got %v", err)
	}
}
