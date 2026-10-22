package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/execws"
)

// Exec runs a command on the remote desktop (execws.Run bound to a client).
type Exec func(ctx context.Context, req execws.Request, sio execws.IO) (int, error)

// Endpoint is one side of `deskyard cp`: a local path, or NAME:/path on a
// remote (NAME may be empty for the chosen remote: ":/path").
type Endpoint struct {
	Remote   string
	IsRemote bool
	Path     string
}

// ParseEndpoint splits "alice:/workspace/x" into remote and path. Anything
// with a path separator before the first colon, and Windows drive letters,
// are local.
func ParseEndpoint(s string) Endpoint {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return Endpoint{Path: s}
	}
	name := s[:i]
	if strings.ContainsAny(name, `/\`) {
		return Endpoint{Path: s}
	}
	if runtime.GOOS == "windows" && len(name) == 1 && isLetter(name[0]) {
		return Endpoint{Path: s}
	}
	return Endpoint{Remote: name, IsRemote: true, Path: s[i+1:]}
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// The remote side of an upload: write stdin to $1, or into $1/$2 when $1 is
// a directory; $3 = x makes it executable.
const uploadFileScript = `d=$1
if [ -d "$d" ]; then
  if [ -z "$2" ]; then echo "$d is a directory: give a file name" >&2; exit 1; fi
  d="${d%/}/$2"
fi
cat > "$d" || exit 1
if [ "$3" = x ]; then chmod +x "$d"; fi`

const uploadDirScript = `mkdir -p "$1" && tar -xf - -C "$1"`

// The remote side of a download: one type byte (D or F), then a tar stream
// or the file's bytes.
const downloadScript = `if [ -d "$1" ]; then printf D; exec tar -C "$1" -cf - .
elif [ -e "$1" ]; then printf F; exec cat -- "$1"
else echo "$1: no such file or directory" >&2; exit 1; fi`

// Upload copies local src ("-" = stdin) to remotePath. A file lands at
// remotePath, or inside it when it's a directory; a directory's contents
// land in remotePath, which is created.
func Upload(ctx context.Context, exec Exec, src, remotePath string, stdin io.Reader) error {
	if remotePath == "" {
		return errors.New("no remote path: write it as NAME:/workspace/path")
	}
	var stderr bytes.Buffer
	run := func(argv []string, in io.Reader) error {
		code, err := exec(ctx, execws.Request{Argv: argv}, execws.IO{Stdin: in, Stderr: &stderr})
		if err != nil {
			return err
		}
		if code != 0 {
			return remoteFailure("copy", code, stderr.String())
		}
		return nil
	}
	if src == "-" {
		return run([]string{"sh", "-c", uploadFileScript, "sh", remotePath, "", ""}, stdin)
	}
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(WriteTar(pw, src)) }()
		err := run([]string{"sh", "-c", uploadDirScript, "sh", remotePath}, pr)
		pr.Close()
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	x := ""
	if fi.Mode()&0o111 != 0 {
		x = "x"
	}
	return run([]string{"sh", "-c", uploadFileScript, "sh", remotePath, filepath.Base(src), x}, f)
}

// Download copies remotePath to local dst ("-" = stdout: a file's bytes,
// or a tar stream for a directory). A file lands at dst, or inside it when
// it's a directory; a directory's contents land in dst, which is created.
func Download(ctx context.Context, exec Exec, remotePath, dst string, stdout io.Writer) error {
	if remotePath == "" {
		return errors.New("no remote path: write it as NAME:/workspace/path")
	}
	w := &downloadWriter{remotePath: remotePath, dst: dst, stdout: stdout}
	var stderr bytes.Buffer
	code, err := exec(ctx, execws.Request{Argv: []string{"sh", "-c", downloadScript, "sh", remotePath}}, execws.IO{Stdout: w, Stderr: &stderr})
	ferr := w.finish(err == nil && code == 0)
	if err != nil {
		return err
	}
	if code != 0 {
		return remoteFailure("copy", code, stderr.String())
	}
	return ferr
}

type downloadWriter struct {
	remotePath, dst string
	stdout          io.Writer

	kind  byte
	out   io.Writer
	file  *os.File // temp file for a file download
	final string
	pw    *io.PipeWriter
	done  chan error
	err   error
}

func (w *downloadWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n := len(p)
	if w.kind == 0 {
		if len(p) == 0 {
			return 0, nil
		}
		w.kind = p[0]
		p = p[1:]
		if w.err = w.start(); w.err != nil {
			return 0, w.err
		}
	}
	if len(p) > 0 {
		if _, err := w.out.Write(p); err != nil {
			w.err = err
			return 0, err
		}
	}
	return n, nil
}

func (w *downloadWriter) start() error {
	switch {
	case w.dst == "-":
		w.out = w.stdout
	case w.kind == 'D':
		pr, pw := io.Pipe()
		w.pw, w.done = pw, make(chan error, 1)
		go func() {
			err := ExtractTar(pr, w.dst)
			if err == nil {
				io.Copy(io.Discard, pr) // tar's trailing padding
			}
			pr.CloseWithError(err)
			w.done <- err
		}()
		w.out = pw
	case w.kind == 'F':
		target := w.dst
		if fi, err := os.Stat(target); err == nil && fi.IsDir() {
			target = filepath.Join(target, path.Base(w.remotePath))
		}
		f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".cp-*")
		if err != nil {
			return err
		}
		w.file, w.final, w.out = f, target, f
	default:
		return fmt.Errorf("unexpected answer from the desktop (is it a Deskyard desktop image?)")
	}
	return nil
}

// finish completes or abandons the local side.
func (w *downloadWriter) finish(ok bool) error {
	var err error
	if w.pw != nil {
		if ok {
			w.pw.Close()
		} else {
			w.pw.CloseWithError(errors.New("download failed"))
		}
		if e := <-w.done; ok {
			err = e
		}
	}
	if w.file != nil {
		cerr := w.file.Close()
		if ok && cerr == nil && w.err == nil {
			_ = os.Chmod(w.file.Name(), 0o644)
			err = os.Rename(w.file.Name(), w.final)
		} else {
			os.Remove(w.file.Name())
			if ok && cerr != nil {
				err = cerr
			}
		}
	}
	if ok && w.kind == 0 && w.dst != "-" {
		return errors.New("unexpected empty answer from the desktop")
	}
	if err == nil && ok {
		err = w.err
	}
	return err
}

// LocalCopy copies within this machine with the same rules as remote copies.
func LocalCopy(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(WriteTar(pw, src)) }()
		err := ExtractTar(pr, dst)
		if err == nil {
			io.Copy(io.Discard, pr)
		}
		pr.CloseWithError(err)
		return err
	}
	target := dst
	if di, err := os.Stat(dst); err == nil && di.IsDir() {
		target = filepath.Join(dst, filepath.Base(src))
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func remoteFailure(what string, code int, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s failed on the desktop (exit %d)", what, code)
	}
	return fmt.Errorf("%s failed on the desktop (exit %d): %s", what, code, msg)
}

// Classify recognises the argv of Upload and Download as the supervisor
// sees it on an exec: direction "in" (into the desktop) or "out", and the
// remote path.
func Classify(argv []string) (direction, remotePath string, ok bool) {
	if len(argv) < 5 || argv[0] != "sh" || argv[1] != "-c" || argv[3] != "sh" {
		return "", "", false
	}
	switch argv[2] {
	case uploadFileScript, uploadDirScript:
		return "in", argv[4], true
	case downloadScript:
		return "out", argv[4], true
	}
	return "", "", false
}
