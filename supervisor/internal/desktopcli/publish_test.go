package desktopcli

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Use a real ELF executable, changing its machine field for the other
// architecture. Publication validates artifacts without executing them.
func binaries(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x7fELF")) {
		t.Skip("ELF fixture requires a Linux test runner")
	}
	// Race-enabled test executables have an interpreter. Remove that header
	// in this non-executed fixture to model the shipped CGO_ENABLED=0 build.
	off := binary.LittleEndian.Uint64(data[32:40])
	size := binary.LittleEndian.Uint16(data[54:56])
	count := binary.LittleEndian.Uint16(data[56:58])
	for i := uint16(0); i < count; i++ {
		pos := off + uint64(i)*uint64(size)
		if elf.ProgType(binary.LittleEndian.Uint32(data[pos:pos+4])) == elf.PT_INTERP {
			binary.LittleEndian.PutUint32(data[pos:pos+4], uint32(elf.PT_NULL))
		}
	}
	source := t.TempDir()
	for arch, machine := range map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64} {
		b := bytes.Clone(data)
		binary.LittleEndian.PutUint16(b[18:20], uint16(machine))
		dir := filepath.Join(source, "linux-"+arch)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "deskyard"), b, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

func TestPublishPreservesDirectoryAndOpenExecutable(t *testing.T) {
	source, state := binaries(t), t.TempDir()
	if err := Publish(source, state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "cli/linux-amd64/deskyard")
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before, _ := old.Stat()
	directory, _ := os.Stat(filepath.Dir(path))
	first, _ := io.ReadAll(old)
	if err := Publish(source, state); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.Stat(path)
	if !os.SameFile(before, unchanged) {
		t.Fatal("unchanged binary replaced")
	}
	updated := append(bytes.Clone(first), []byte("new build")...)
	if err := os.WriteFile(filepath.Join(source, "linux-amd64/deskyard"), updated, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Publish(source, state); err != nil {
		t.Fatal(err)
	}
	afterDir, _ := os.Stat(filepath.Dir(path))
	if !os.SameFile(directory, afterDir) {
		t.Fatal("mounted directory replaced")
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, updated) {
		t.Fatalf("new invocation sees wrong binary: %v", err)
	}
	if _, err := old.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	stillOld, _ := io.ReadAll(old)
	if !bytes.Equal(stillOld, first) {
		t.Fatal("old executable modified in place")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0755 {
		t.Fatalf("executable mode %o", st.Mode().Perm())
	}
}

func TestPublishRefusesInvalidBundleBeforeChangingExistingCLI(t *testing.T) {
	for _, failure := range []string{"missing", "not ELF", "wrong architecture", "dynamic"} {
		t.Run(failure, func(t *testing.T) {
			source, state := binaries(t), t.TempDir()
			if err := Publish(source, state); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(state, "cli/linux-amd64/deskyard")
			previous, _ := os.ReadFile(target)
			if err := os.WriteFile(filepath.Join(source, "linux-amd64/deskyard"), append(bytes.Clone(previous), 'x'), 0755); err != nil {
				t.Fatal(err)
			}
			invalid := filepath.Join(source, "linux-arm64/deskyard")
			switch failure {
			case "missing":
				if err := os.Remove(invalid); err != nil {
					t.Fatal(err)
				}
			case "not ELF":
				if err := os.WriteFile(invalid, []byte("bad build"), 0755); err != nil {
					t.Fatal(err)
				}
			case "wrong architecture":
				if err := os.WriteFile(invalid, previous, 0755); err != nil {
					t.Fatal(err)
				}
			case "dynamic":
				b, _ := os.ReadFile(invalid)
				off := binary.LittleEndian.Uint64(b[32:40])
				binary.LittleEndian.PutUint32(b[off:off+4], uint32(elf.PT_INTERP))
				if err := os.WriteFile(invalid, b, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := Publish(source, state); err == nil {
				t.Fatal("invalid bundle accepted")
			}
			current, _ := os.ReadFile(target)
			if !bytes.Equal(current, previous) {
				t.Fatal("failed validation changed the installed CLI")
			}
		})
	}
}
