// Package desktopcli publishes the supervisor's static CLI builds for desktop
// directory mounts. Mounted directories are never replaced during an update.
package desktopcli

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	Source     = "/opt/deskyard/cli"
	Target     = "/opt/deskyard/cli"
	Label      = "deskyard.cli"
	Capability = "runtime-v1"
)

var architectures = []string{"amd64", "arm64"}

// Directory selects the published directory using the desktop image's
// architecture, which need not match the supervisor process's architecture.
func Directory(state, arch string) (string, error) {
	switch arch {
	case "amd64", "arm64":
		return filepath.Join(state, "cli", "linux-"+arch), nil
	default:
		return "", fmt.Errorf("no desktop CLI build for architecture %q", arch)
	}
}

// Publish validates the entire bundle before publishing any file. Rename is
// atomic per architecture: new executions see the new binary, while processes
// already running keep their executable. An unchanged file keeps its inode.
func Publish(source, state string) error {
	builds := make(map[string][]byte, len(architectures))
	for _, arch := range architectures {
		path := filepath.Join(source, "linux-"+arch, "deskyard")
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("desktop CLI %s: %w", path, err)
		}
		if err := validate(b, arch); err != nil {
			return fmt.Errorf("desktop CLI %s: %w", path, err)
		}
		builds[arch] = b
	}
	for _, arch := range architectures {
		dir, _ := Directory(state, arch)
		if err := publish(dir, builds[arch]); err != nil {
			return fmt.Errorf("publish desktop CLI %s: %w", arch, err)
		}
	}
	return nil
}

func validate(b []byte, arch string) error {
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer f.Close()
	machine := elf.EM_X86_64
	if arch == "arm64" {
		machine = elf.EM_AARCH64
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != machine || f.Type != elf.ET_EXEC {
		return fmt.Errorf("expected a static Linux %s executable", arch)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return errors.New("CLI must be statically linked (CGO_ENABLED=0)")
		}
	}
	return nil
}

func publish(dir string, b []byte) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "deskyard")
	previous, err := os.ReadFile(path)
	if err == nil && bytes.Equal(previous, b) {
		return os.Chmod(path, 0755)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(dir, ".deskyard-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Chmod(0755); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
