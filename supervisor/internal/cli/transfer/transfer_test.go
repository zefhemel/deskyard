package transfer

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/execws"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in      string
		remote  bool
		name, p string
	}{
		{"alice:/workspace/x", true, "alice", "/workspace/x"},
		{":/workspace/x", true, "", "/workspace/x"},
		{"./a:b", false, "", "./a:b"},
		{"/tmp/a:b", false, "", "/tmp/a:b"},
		{"plain.txt", false, "", "plain.txt"},
		{"-", false, "", "-"},
	}
	for _, c := range cases {
		e := ParseEndpoint(c.in)
		if e.IsRemote != c.remote || e.Remote != c.name || e.Path != c.p {
			t.Errorf("ParseEndpoint(%q) = %+v", c.in, e)
		}
	}
}

func setup(t *testing.T) (*clitest.Fake, Exec) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake runs commands with sh and tar")
	}
	f := clitest.New(t, "alice")
	c := &remote.Client{Alias: "alice", Base: f.URL, Computer: "alice", Token: clitest.Token}
	return f, func(ctx context.Context, req execws.Request, sio execws.IO) (int, error) {
		return execws.Run(ctx, c, req, sio)
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func write(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUploadFile(t *testing.T) {
	f, exec := setup(t)
	local := t.TempDir()
	write(t, filepath.Join(local, "a.txt"), "hello", 0o644)
	write(t, filepath.Join(local, "run.sh"), "#!/bin/sh\n", 0o755)

	// To a new path.
	if err := Upload(ctx(t), exec, filepath.Join(local, "a.txt"), filepath.Join(f.Dir, "b.txt"), nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(f.Dir, "b.txt")); got != "hello" {
		t.Errorf("b.txt = %q", got)
	}
	// Into an existing directory, keeping the executable bit.
	os.Mkdir(filepath.Join(f.Dir, "bin"), 0o755)
	if err := Upload(ctx(t), exec, filepath.Join(local, "run.sh"), filepath.Join(f.Dir, "bin")+"/", nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(f.Dir, "bin", "run.sh"))
	if err != nil || fi.Mode()&0o100 == 0 {
		t.Errorf("run.sh: %v %v", fi, err)
	}
	// From stdin.
	if err := Upload(ctx(t), exec, "-", filepath.Join(f.Dir, "stdin.txt"), strings.NewReader("piped")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(f.Dir, "stdin.txt")); got != "piped" {
		t.Errorf("stdin.txt = %q", got)
	}
	// Failure is reported with the remote's message.
	err = Upload(ctx(t), exec, filepath.Join(local, "a.txt"), "/no/such/dir/x", nil)
	if err == nil || !strings.Contains(err.Error(), "copy failed on the computer") {
		t.Errorf("want a failure, got %v", err)
	}
}

func TestUploadDir(t *testing.T) {
	f, exec := setup(t)
	src := filepath.Join(t.TempDir(), "site")
	write(t, filepath.Join(src, "index.html"), "<h1>", 0o644)
	write(t, filepath.Join(src, "css", "a.css"), "body{}", 0o644)
	os.Symlink("index.html", filepath.Join(src, "link.html"))
	dst := filepath.Join(f.Dir, "deep", "site")
	if err := Upload(ctx(t), exec, src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dst, "index.html")) != "<h1>" || read(t, filepath.Join(dst, "css", "a.css")) != "body{}" {
		t.Errorf("directory contents differ")
	}
	if l, err := os.Readlink(filepath.Join(dst, "link.html")); err != nil || l != "index.html" {
		t.Errorf("symlink: %q %v", l, err)
	}
}

func TestDownloadFile(t *testing.T) {
	f, exec := setup(t)
	write(t, filepath.Join(f.Dir, "out.csv"), "a,b\n", 0o644)
	local := t.TempDir()
	// Into a directory.
	if err := Download(ctx(t), exec, filepath.Join(f.Dir, "out.csv"), local, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(local, "out.csv")); got != "a,b\n" {
		t.Errorf("out.csv = %q", got)
	}
	// To a file name.
	if err := Download(ctx(t), exec, filepath.Join(f.Dir, "out.csv"), filepath.Join(local, "renamed.csv"), nil); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(local, "renamed.csv")); got != "a,b\n" {
		t.Errorf("renamed.csv = %q", got)
	}
	// To stdout.
	var out bytes.Buffer
	if err := Download(ctx(t), exec, filepath.Join(f.Dir, "out.csv"), "-", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "a,b\n" {
		t.Errorf("stdout = %q", out.String())
	}
	// Missing.
	err := Download(ctx(t), exec, filepath.Join(f.Dir, "nope"), local, nil)
	if err == nil || !strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("want not found, got %v", err)
	}
	entries, _ := os.ReadDir(local)
	if len(entries) != 2 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestDownloadDir(t *testing.T) {
	f, exec := setup(t)
	write(t, filepath.Join(f.Dir, "proj", "a.txt"), "A", 0o644)
	write(t, filepath.Join(f.Dir, "proj", "sub", "b.txt"), "B", 0o600)
	dst := filepath.Join(t.TempDir(), "copy")
	if err := Download(ctx(t), exec, filepath.Join(f.Dir, "proj"), dst, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dst, "a.txt")) != "A" || read(t, filepath.Join(dst, "sub", "b.txt")) != "B" {
		t.Errorf("directory contents differ")
	}
}

func TestExtractRefusesTraversal(t *testing.T) {
	for _, name := range []string{"../evil", "./a/../../evil"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
		tw.Close()
		dir := filepath.Join(t.TempDir(), "d")
		err := ExtractTar(&buf, dir)
		// Cleaned names stay inside: ../evil becomes dir/evil.
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
			t.Errorf("%s escaped the destination", name)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	// A symlink followed by a write through it is refused.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	outside := t.TempDir()
	tw.WriteHeader(&tar.Header{Name: "./l", Typeflag: tar.TypeSymlink, Linkname: outside})
	tw.WriteHeader(&tar.Header{Name: "./l/x", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	if err := ExtractTar(&buf, t.TempDir()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("want a refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Errorf("wrote through the symlink")
	}
}

func TestLocalCopy(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "f.txt"), "F", 0o644)
	write(t, filepath.Join(src, "d", "g.txt"), "G", 0o644)
	dst := t.TempDir()
	if err := LocalCopy(filepath.Join(src, "f.txt"), dst); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dst, "f.txt")) != "F" {
		t.Error("file copy")
	}
	if err := LocalCopy(src, filepath.Join(dst, "all")); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dst, "all", "d", "g.txt")) != "G" {
		t.Error("dir copy")
	}
}
