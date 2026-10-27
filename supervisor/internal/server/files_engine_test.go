package server

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// filesEngine is a running "alice" whose /workspace is a real folder on this
// machine: Exec performs the few commands file uploads use in Go, so paths
// resolve for real.
type filesEngine struct {
	fakeEngine
	root    string // the folder standing in for /workspace, symlinks resolved
	tarExit int    // tar's exit code after a complete archive (1: "file changed as we read it")
}

func (e *filesEngine) Inspect(ctx context.Context, id string) (engine.Container, error) {
	c, err := e.fakeEngine.Inspect(ctx, id)
	c.State = "running"
	return c, err
}

func (e *filesEngine) List(context.Context, map[string]string) ([]engine.Container, error) {
	c := alice
	c.State = "running"
	return []engine.Container{c}, nil
}

// host maps a desktop path to the stand-in folder.
func (e *filesEngine) host(p string) string {
	if !filepath.IsAbs(p) {
		p = "/workspace/" + p
	}
	if p == "/workspace" || strings.HasPrefix(p, "/workspace/") {
		return e.root + strings.TrimPrefix(p, "/workspace")
	}
	return p
}

// guest is host's inverse for resolved paths.
func (e *filesEngine) guest(p string) string {
	if p == e.root || strings.HasPrefix(p, e.root+"/") {
		return "/workspace" + strings.TrimPrefix(p, e.root)
	}
	return p
}

func (e *filesEngine) Exec(_ context.Context, _ string, spec engine.ExecSpec) (*engine.Process, error) {
	var out bytes.Buffer
	var stderr string
	code := 0
	args := spec.Cmd[1:]
	for len(args) > 0 && args[0] == "--" || len(args) > 0 && strings.HasPrefix(args[0], "-") && spec.Cmd[0] == "realpath" {
		args = args[1:]
	}
	switch spec.Cmd[0] {
	case "realpath":
		for _, a := range args {
			r, err := filepath.EvalSymlinks(e.host(a))
			if err != nil {
				code, stderr = 1, "realpath: "+a+": No such file or directory"
				break
			}
			out.WriteString(e.guest(r) + "\n")
		}
	case "find":
		if len(spec.Cmd) > 3 && spec.Cmd[2] == "-maxdepth" && spec.Cmd[3] == "0" { // type and size of one path
			fi, err := os.Stat(e.host(spec.Cmd[1]))
			if err != nil {
				code = 1
				break
			}
			t := "f"
			if fi.IsDir() {
				t = "d"
			}
			out.WriteString(t + "\t" + strconv.FormatInt(fi.Size(), 10))
			break
		}
		ents, err := os.ReadDir(e.host(spec.Cmd[1]))
		if err != nil {
			code = 1
			break
		}
		for _, d := range ents {
			fi, err := os.Stat(filepath.Join(e.host(spec.Cmd[1]), d.Name()))
			if err != nil {
				continue
			}
			t := "f"
			if fi.IsDir() {
				t = "d"
			}
			out.WriteString(t + "\t" + strconv.FormatInt(fi.Size(), 10) + "\t" + strconv.FormatInt(fi.ModTime().Unix(), 10) + ".0\t" + d.Name() + "\x00")
		}
	case "cat":
		b, err := os.ReadFile(e.host(args[0]))
		if err != nil {
			code = 1
			break
		}
		out.Write(b)
	case "tar": // tar [flags] -C DIR -cf - .
		var dir string
		flags := map[string]bool{}
		for i := 1; i < len(spec.Cmd); i++ {
			if spec.Cmd[i] == "-C" {
				dir = e.host(spec.Cmd[i+1])
				i++
			} else {
				flags[spec.Cmd[i]] = true
			}
		}
		tw := tar.NewWriter(&out)
		var seen []os.FileInfo
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(dir, p)
			fi, _ := os.Lstat(p)
			if d.Name() == "unreadable" {
				if !flags["--ignore-failed-read"] {
					code, stderr = 2, "tar: unreadable: Permission denied"
				}
				return nil
			}
			h, _ := tar.FileInfoHeader(fi, "")
			h.Name = "./" + rel
			if rel == "." {
				h.Name = "./"
			}
			if fi.Mode().IsRegular() && !flags["--hard-dereference"] {
				for k, o := range seen { // GNU tar's second name of a hard link is a header only
					if os.SameFile(o, fi) {
						h.Typeflag, h.Linkname, h.Size = tar.TypeLink, "./"+strconv.Itoa(k), 0
					}
				}
				seen = append(seen, fi)
			}
			_ = tw.WriteHeader(h)
			if fi.Mode().IsRegular() && h.Typeflag == tar.TypeReg {
				b, _ := os.ReadFile(p)
				_, _ = tw.Write(b)
			}
			return nil
		})
		_ = tw.Close()
		if code == 0 {
			code = e.tarExit
		}
	case "rm": // rm -f -- PATH
		_ = os.Remove(e.host(args[len(args)-1]))
	case "sh": // sh -c SCRIPT sh ARGS...: run for real, with /workspace paths mapped to the stand-in folder
		argv := append([]string{}, spec.Cmd...)
		for i := 4; i < len(argv); i++ {
			if strings.HasPrefix(argv[i], "/workspace") {
				argv[i] = e.host(argv[i])
			}
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		in, _ := cmd.StdinPipe()
		o, _ := cmd.StdoutPipe()
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &engine.Process{Stdin: in, Stdout: o,
			Wait:  func() (int, error) { _ = cmd.Wait(); return cmd.ProcessState.ExitCode(), nil },
			Close: func() { _ = in.Close(); _ = cmd.Process.Kill() }}, nil
	default:
		code, stderr = 127, spec.Cmd[0]+": not found"
	}
	return &engine.Process{Stdout: &out, Stderr: strings.NewReader(stderr),
		Wait: func() (int, error) { return code, nil }, Close: func() {}}, nil
}

func newFilesServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	accts, _ := accounts.Load(dir)
	_, _ = accts.EnsureAdmin("admin", "pw")
	_, _ = accts.Create(accounts.NewAccount{Username: "anna", Password: "pw"})
	_, _ = accts.Create(accounts.NewAccount{Username: "ben", Password: "pw"})
	e := &filesEngine{root: root}
	s, err := New(Options{Log: log, Eng: e, Accounts: accts, Layout: config.NewLayout(dir), DefaultImage: "img",
		Environments: environments.New(e, environments.Config{DataDir: dir, Subnet: "10.0.0.0/16"}, log),
		UI:           fstest.MapFS{"index.html": {Data: []byte("ui")}}})
	if err != nil {
		t.Fatal(err)
	}
	defineEnvironments(t, s)
	// Granted once alice is defined: startup drops grants of undefined names.
	if err := accts.SetAccess("alice", fullAccess("ben")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, ts, root
}
