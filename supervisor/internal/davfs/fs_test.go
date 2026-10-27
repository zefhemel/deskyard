package davfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type folders struct{ home, projects, photos, archive string }

func newTree(t *testing.T) (*FS, folders) {
	t.Helper()
	base := t.TempDir()
	d := folders{filepath.Join(base, "home"), filepath.Join(base, "projects"), filepath.Join(base, "photos"), filepath.Join(base, "archive")}
	for _, p := range []string{d.home, d.projects, d.photos, d.archive} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(d.home, "readme.txt"), "hello")
	write(t, filepath.Join(d.photos, "cat.jpg"), "meow")
	f, err := New([]Mount{
		{At: "/workspace", Folder: d.home},
		{At: "/share/projects", Folder: d.projects},
		{At: "/share/photos", Folder: d.photos, ReadOnly: true},
		{At: "/data/old/archive", Folder: d.archive},
		{At: "/share/gone", Folder: filepath.Join(base, "missing")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f, d
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
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

func list(t *testing.T, f *FS, name string) []string {
	t.Helper()
	file, err := f.OpenFile(context.Background(), name, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer file.Close()
	fis, err := file.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, fi := range fis {
		out = append(out, fi.Name())
	}
	slices.Sort(out)
	return out
}

func put(t *testing.T, ctx context.Context, f *FS, name, content string) error {
	t.Helper()
	file, err := f.OpenFile(ctx, name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	return file.Close()
}

func TestTreeShowsMountsUnderSynthesizedFolders(t *testing.T) {
	f, _ := newTree(t)
	ctx := context.Background()
	for name, want := range map[string][]string{
		"/":          {"data", "share", "workspace"},
		"/share":     {"photos", "projects"}, // the missing folder is left out
		"/data/old":  {"archive"},
		"/workspace": {"readme.txt"},
	} {
		if got := list(t, f, name); !slices.Equal(got, want) {
			t.Errorf("%s lists %v, want %v", name, got, want)
		}
	}
	fi, err := f.Stat(ctx, "/share/photos")
	if err != nil || fi.Name() != "photos" || !fi.IsDir() {
		t.Fatalf("stat of a mount = %v, %v", fi, err)
	}
	if _, err := f.Stat(ctx, "/share/gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing mount: %v", err)
	}
	if _, err := f.Stat(ctx, "/etc"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("/etc: %v", err)
	}
}

func TestWritesOnlyInsideWritableMounts(t *testing.T) {
	f, d := newTree(t)
	ctx := context.Background()
	for _, name := range []string{"/x.txt", "/share/x.txt", "/share/photos/x.txt", "/data/x"} {
		if f.Writable(name) {
			t.Errorf("%s is writable", name)
		}
		if err := put(t, ctx, f, name, "x"); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("put %s: %v", name, err)
		}
		if err := f.Mkdir(ctx, name, 0o755); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("mkdir %s: %v", name, err)
		}
	}
	for _, name := range []string{"/workspace", "/share/projects", "/share", "/share/photos/cat.jpg"} {
		if err := f.RemoveAll(ctx, name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("remove %s: %v", name, err)
		}
	}
	if err := f.Rename(ctx, "/workspace", "/workspace2"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("renaming a mount: %v", err)
	}
	if err := put(t, ctx, f, "/workspace/new.txt", "new"); err != nil {
		t.Fatal(err)
	}
	if err := f.Mkdir(ctx, "/share/projects/dir", 0o700); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(d.home, "new.txt")); got != "new" {
		t.Fatalf("new.txt = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(d.projects, "dir")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("dir = %v", fi)
	}
}

func TestNoWayOutOfAMount(t *testing.T) {
	f, d := newTree(t)
	ctx := context.Background()
	outside := filepath.Join(filepath.Dir(d.home), "secret")
	write(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(d.home, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../projects", filepath.Join(d.home, "up")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/workspace/link", "/workspace/up/x", "/workspace/../../secret"} {
		if file, err := f.OpenFile(ctx, name, os.O_RDONLY, 0); err == nil {
			file.Close()
			t.Errorf("opened %s", name)
		}
	}
	if err := put(t, ctx, f, "/workspace/up/x", "x"); err == nil {
		t.Error("wrote through a symlink out of the mount")
	}
	if got := read(t, outside); got != "secret" {
		t.Fatal("the outside file changed")
	}
}

func TestMovesBetweenMountsCopyThenRemove(t *testing.T) {
	f, d := newTree(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(d.home, "trip", "day1"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d.home, "trip", "day1", "a.txt"), "a")
	if err := os.WriteFile(filepath.Join(d.home, "trip", "run.sh"), []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("day1/a.txt", filepath.Join(d.home, "trip", "latest")); err != nil {
		t.Fatal(err)
	}
	if err := f.Rename(ctx, "/workspace/trip", "/share/projects/trip"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(d.home, "trip")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the source is still there: %v", err)
	}
	if got := read(t, filepath.Join(d.projects, "trip", "day1", "a.txt")); got != "a" {
		t.Fatalf("a.txt = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(d.projects, "trip", "run.sh")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("run.sh lost its mode: %v", fi)
	}
	if target, _ := os.Readlink(filepath.Join(d.projects, "trip", "latest")); target != "day1/a.txt" {
		t.Fatalf("latest -> %q", target)
	}
	// Within a mount it is a rename.
	if err := f.Rename(ctx, "/share/projects/trip", "/share/projects/trip2"); err != nil {
		t.Fatal(err)
	}
	// Into a read-only mount nothing moves.
	if err := f.Rename(ctx, "/workspace/readme.txt", "/share/photos/readme.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("into read-only: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.home, "readme.txt")); err != nil {
		t.Fatal("the source went")
	}
}

func TestFailedCrossMountMoveKeepsTheSource(t *testing.T) {
	f, d := newTree(t)
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	if err := os.MkdirAll(filepath.Join(d.home, "box"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d.home, "box", "ok.txt"), "ok")
	write(t, filepath.Join(d.home, "box", "locked.txt"), "no")
	if err := os.Chmod(filepath.Join(d.home, "box", "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := f.Rename(context.Background(), "/workspace/box", "/share/projects/box"); err == nil {
		t.Fatal("the move succeeded")
	}
	if got := read(t, filepath.Join(d.home, "box", "ok.txt")); got != "ok" {
		t.Fatal("the source lost a file")
	}
	if _, err := os.Lstat(filepath.Join(d.projects, "box")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the partial copy stayed: %v", err)
	}
}

func TestUploadsReplaceOnlyWhenComplete(t *testing.T) {
	f, d := newTree(t)
	target := filepath.Join(d.home, "readme.txt")
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}

	// A body read to its end replaces the file, keeping its mode.
	ctx, body := WithUpload(context.Background(), io.NopCloser(strings.NewReader("updated")))
	file, err := f.OpenFile(ctx, "/workspace/readme.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, f, "/workspace"); !slices.Equal(got, []string{"readme.txt"}) {
		t.Fatalf("while writing, the folder lists %v", got)
	}
	if _, err := io.Copy(file, body); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "hello" {
		t.Fatalf("replaced before close: %q", got)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, target); got != "updated" {
		t.Fatalf("readme.txt = %q", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}

	// A body cut short leaves the old content and no .part file.
	ctx, body = WithUpload(context.Background(), io.NopCloser(&failing{data: "part of it"}))
	file, err = f.OpenFile(ctx, "/workspace/readme.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(file, body); err == nil {
		t.Fatal("the copy didn't fail")
	}
	if err := file.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("close = %v", err)
	}
	if got := read(t, target); got != "updated" {
		t.Fatalf("readme.txt = %q", got)
	}
	entries, _ := os.ReadDir(d.home)
	if len(entries) != 1 {
		t.Fatalf("home holds %v", entries)
	}

	// A write without an upload (COPY, LOCK) is complete when closed.
	if err := put(t, context.Background(), f, "/workspace/copy.txt", "c"); err != nil || read(t, filepath.Join(d.home, "copy.txt")) != "c" {
		t.Fatalf("copy.txt: %v", err)
	}
	if err := put(t, context.Background(), f, "/workspace/nofolder/x.txt", "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("into a missing folder: %v", err)
	}
}

type failing struct{ data string }

func (r *failing) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestMacOSMetadataIsAcceptedAndDropped(t *testing.T) {
	f, d := newTree(t)
	ctx := context.Background()
	write(t, filepath.Join(d.home, "._made-on-the-desktop"), "x")
	for _, name := range []string{"/workspace/.DS_Store", "/workspace/._readme.txt", "/share/photos/._cat.jpg", "/._workspace"} {
		if !f.Writable(name) {
			t.Errorf("%s is not writable", name)
		}
		if err := put(t, ctx, f, name, "metadata"); err != nil {
			t.Errorf("put %s: %v", name, err)
		}
		if _, err := f.Stat(ctx, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s: %v", name, err)
		}
		if err := f.RemoveAll(ctx, name); err != nil {
			t.Errorf("remove %s: %v", name, err)
		}
	}
	if err := f.Rename(ctx, "/workspace/._a", "/workspace/._b"); err != nil {
		t.Errorf("rename: %v", err)
	}
	if err := f.Rename(ctx, "/workspace/readme.txt", "/workspace/._readme.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("renaming a file to metadata: %v", err)
	}
	if got := list(t, f, "/workspace"); !slices.Equal(got, []string{"readme.txt"}) {
		t.Fatalf("lists %v", got)
	}
	if _, err := os.Stat(filepath.Join(d.home, ".DS_Store")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(".DS_Store was stored")
	}
}

func TestMountPathsMustBeClean(t *testing.T) {
	for _, at := range []string{"/", "workspace", "/a/../b", "/a/"} {
		if _, err := New([]Mount{{At: at, Folder: t.TempDir()}}); err == nil {
			t.Errorf("%q accepted", at)
		}
	}
}

func TestFailedOverwriteKeepsTheDestination(t *testing.T) {
	f, d := newTree(t)
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	write(t, filepath.Join(d.projects, "report.txt"), "old report")
	write(t, filepath.Join(d.home, "report.txt"), "new report")
	if err := os.Chmod(filepath.Join(d.home, "report.txt"), 0); err != nil {
		t.Fatal(err)
	}
	// As x/net/webdav moves with Overwrite: T: remove the destination, then rename.
	ctx, done := WithReplacement(context.Background(), "/share/projects/report.txt")
	if err := f.RemoveAll(ctx, "/share/projects/report.txt"); err != nil {
		t.Fatal(err)
	}
	if err := f.Rename(ctx, "/workspace/report.txt", "/share/projects/report.txt"); err == nil {
		t.Fatal("the move succeeded")
	}
	if err := done(false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(d.projects, "report.txt")); got != "old report" {
		t.Fatalf("destination = %q", got)
	}
	if got := list(t, f, "/share/projects"); !slices.Equal(got, []string{"report.txt"}) {
		t.Fatalf("projects lists %v", got)
	}

	// A successful one replaces it and leaves nothing set aside.
	if err := os.Chmod(filepath.Join(d.home, "report.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, done = WithReplacement(context.Background(), "/share/projects/report.txt")
	if err := f.RemoveAll(ctx, "/share/projects/report.txt"); err != nil {
		t.Fatal(err)
	}
	if err := f.Rename(ctx, "/workspace/report.txt", "/share/projects/report.txt"); err != nil {
		t.Fatal(err)
	}
	if err := done(true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(d.projects, "report.txt")); got != "new report" {
		t.Fatalf("destination = %q", got)
	}
	if entries, _ := os.ReadDir(d.projects); len(entries) != 1 {
		t.Fatalf("projects holds %v", entries)
	}
}

func TestCrossMountMoveOfReadOnlyFolders(t *testing.T) {
	f, d := newTree(t)
	if err := os.MkdirAll(filepath.Join(d.home, "cache", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d.home, "cache", "pkg", "x.go"), "package x")
	for _, p := range []string{filepath.Join(d.home, "cache", "pkg"), filepath.Join(d.home, "cache")} {
		if err := os.Chmod(p, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = filepath.WalkDir(filepath.Dir(d.home), func(p string, e fs.DirEntry, _ error) error { return os.Chmod(p, 0o755) }) })
	// The copy arrives whole with the folders' modes; what of the read-only
	// source can't be removed stays, and the move still succeeds.
	if err := f.Rename(context.Background(), "/workspace/cache", "/share/projects/cache"); err != nil {
		t.Fatalf("the move failed: %v", err)
	}
	if got := read(t, filepath.Join(d.projects, "cache", "pkg", "x.go")); got != "package x" {
		t.Fatalf("x.go = %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(d.projects, "cache", "pkg")); fi == nil || fi.Mode().Perm() != 0o555 {
		t.Fatalf("pkg = %v", fi)
	}
}

func TestCancelledUploadDoesNotLand(t *testing.T) {
	f, d := newTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	ctx, body := WithUpload(ctx, io.NopCloser(strings.NewReader("revoked")))
	file, err := f.OpenFile(ctx, "/workspace/readme.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(file, body); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := file.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("close = %v", err)
	}
	if got := read(t, filepath.Join(d.home, "readme.txt")); got != "hello" {
		t.Fatalf("readme.txt = %q", got)
	}
}

func TestDroppedFilesDontReplaceRealOnes(t *testing.T) {
	f, _ := newTree(t)
	if err := f.Rename(context.Background(), "/workspace/._readme.txt", "/workspace/readme.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("rename = %v", err)
	}
}
