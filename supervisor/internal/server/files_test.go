package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// filesEngine is a running "alice" whose /workspace is a real folder on this
// machine: Exec performs the few commands the file routes use (realpath,
// stat, find, cat, tar) in Go, so symlinks and ".." resolve for real.
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

func write(t *testing.T, p, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, mod, mod)
}

func fetch(t *testing.T, c *http.Client, url string) (int, []byte, http.Header) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func TestFilesListAndDownload(t *testing.T) {
	_, ts, root := newFilesServer(t)
	now := time.Now()
	write(t, filepath.Join(root, "old.txt"), "old", now.Add(-time.Hour))
	write(t, filepath.Join(root, "new.txt"), "newer", now)
	write(t, filepath.Join(root, "proj", "a.txt"), "A", now)
	write(t, filepath.Join(root, "proj", "sub", "b.txt"), "BB", now)
	bin := bytes.Repeat([]byte{0, 1, 2, 255, 254}, 100000)
	if err := os.WriteFile(filepath.Join(root, "data.bin"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(root, "data.bin"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	admin := login(t, ts, "admin")
	base := ts.URL + "/api/environments/alice"

	t.Run("list", func(t *testing.T) {
		code, body, _ := fetch(t, admin, base+"/files")
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		var res struct {
			Path    string
			Entries []fileEntry
		}
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatal(err)
		}
		if res.Path != "/workspace" {
			t.Errorf("path %q", res.Path)
		}
		var names []string
		for _, e := range res.Entries {
			names = append(names, e.Name)
		}
		// proj and new.txt were touched now (name order breaks the tie), then old.txt, data.bin.
		if got := strings.Join(names, ","); got != "new.txt,proj,old.txt,data.bin" {
			t.Errorf("order %s", got)
		}
		if !res.Entries[1].Dir || res.Entries[0].Dir || res.Entries[0].Size != 5 {
			t.Errorf("entries %+v", res.Entries)
		}
		// A relative path works too.
		code, body, _ = fetch(t, admin, base+"/files?path=proj/sub")
		if code != 200 || !strings.Contains(string(body), `"b.txt"`) {
			t.Errorf("subfolder: %d %s", code, body)
		}
	})

	t.Run("file byte for byte", func(t *testing.T) {
		code, body, h := fetch(t, admin, base+"/download?path=/workspace/data.bin")
		if code != 200 || !bytes.Equal(body, bin) {
			t.Fatalf("%d, %d bytes", code, len(body))
		}
		if !strings.Contains(h.Get("Content-Disposition"), `attachment; filename=data.bin`) || h.Get("Content-Length") != strconv.Itoa(len(bin)) {
			t.Errorf("headers %v", h)
		}
	})

	t.Run("folder as zip", func(t *testing.T) {
		code, body, h := fetch(t, admin, base+"/download?path=proj")
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		if !strings.Contains(h.Get("Content-Disposition"), "proj.zip") {
			t.Errorf("name %v", h.Get("Content-Disposition"))
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			got[f.Name] = string(b)
		}
		want := map[string]string{"a.txt": "A", "sub/": "", "sub/b.txt": "BB"}
		if len(got) != len(want) {
			t.Fatalf("entries %v", got)
		}
		for k, v := range want {
			if g, ok := got[k]; !ok || g != v {
				t.Errorf("%s = %q (present %v), want %q", k, g, ok, v)
			}
		}
	})

	t.Run("outside /workspace is refused", func(t *testing.T) {
		outside := t.TempDir()
		write(t, filepath.Join(outside, "secret"), "x", now)
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Fatal(err)
		}
		sibling := filepath.Join(filepath.Dir(root), "sibling-"+filepath.Base(root))
		write(t, sibling, "x", now)
		t.Cleanup(func() { os.Remove(sibling) })
		up := "../" + filepath.Base(sibling)
		for _, p := range []string{"..", up, "/etc/passwd", "/workspace/" + up, "escape", "escape/secret", "/"} {
			for _, route := range []string{"/download", "/files"} {
				if code, body, _ := fetch(t, admin, base+route+"?path="+p); code != 403 {
					t.Errorf("%s %q: %d %s", route, p, code, body)
				}
			}
		}
	})

	t.Run("missing and wrong kind", func(t *testing.T) {
		if code, _, _ := fetch(t, admin, base+"/download?path=nope"); code != 404 {
			t.Errorf("missing: %d", code)
		}
		if code, _, _ := fetch(t, admin, base+"/files?path=new.txt"); code != 400 {
			t.Errorf("list a file: %d", code)
		}
	})
}

func TestFilesAccess(t *testing.T) {
	s, ts, root := newFilesServer(t)
	write(t, filepath.Join(root, "f.txt"), "hi", time.Now())
	base := ts.URL + "/api/environments/alice"
	for _, route := range []string{"/files", "/download?path=f.txt"} {
		if code, _, _ := fetch(t, login(t, ts, "ben"), base+route); code != 200 {
			t.Errorf("a viewer on %s: %d", route, code)
		}
		if code, _, _ := fetch(t, login(t, ts, "anna"), base+route); code != 404 {
			t.Errorf("no access on %s: %d", route, code)
		}
		anon := &http.Client{}
		if code, _, _ := fetch(t, anon, base+route); code != 401 {
			t.Errorf("anonymous on %s: %d", route, code)
		}
		_, ben := issueCredential(t, s, accountNamed(t, s, "ben"))
		if code, _, _ := bearerRequest(t, "GET", base+route, ben); code != 200 {
			t.Errorf("a viewer's client credential on %s: %d", route, code)
		}
		if code, _, _ := bearerRequest(t, "GET", base+route, "dyt_00000000_removed"); code != 401 {
			t.Errorf("a removed access token on %s: %d", route, code)
		}
	}
}

func TestFilesStoppedDesktop(t *testing.T) {
	_, ts := newTestServer(t) // alice is stopped here
	admin := login(t, ts, "admin")
	if code, _, _ := fetch(t, admin, ts.URL+"/api/environments/alice/files"); code != 409 {
		t.Errorf("stopped: %d", code)
	}
}

func TestTarToZipSkipsLinks(t *testing.T) {
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	_ = tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "./ok", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("k"))
	_ = tw.WriteHeader(&tar.Header{Name: "./link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.WriteHeader(&tar.Header{Name: "./../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("e"))
	_ = tw.Close()
	var zb bytes.Buffer
	if err := tarToZip(&zb, &tb); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb.Bytes()), int64(zb.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); got != "ok,evil" {
		t.Errorf("zip entries %s (a link must not appear, and names stay relative)", got)
	}
}

func TestFolderZipSurvivesTarQuirks(t *testing.T) {
	s, ts, root := newFilesServer(t)
	e := s.Eng.(*filesEngine)
	now := time.Now()
	write(t, filepath.Join(root, "dir", "one.txt"), "one", now)
	write(t, filepath.Join(root, "dir", "two.txt"), "two", now)
	write(t, filepath.Join(root, "dir", "unreadable"), "secret", now)
	if err := os.Link(filepath.Join(root, "dir", "one.txt"), filepath.Join(root, "dir", "one-link.txt")); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin")
	for _, exit := range []int{0, 1} { // 1: "file changed as we read it", the archive is complete
		e.tarExit = exit
		code, body, _ := fetch(t, admin, ts.URL+"/api/environments/alice/download?path=dir")
		if code != 200 {
			t.Fatalf("exit %d: %d", exit, code)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, f := range zr.File {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			got[f.Name] = string(b)
		}
		want := map[string]string{"one.txt": "one", "one-link.txt": "one", "two.txt": "two"}
		if len(got) != len(want) {
			t.Fatalf("exit %d: entries %v, want %v (hard links kept, the unreadable file skipped)", exit, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("exit %d: %s = %q, want %q", exit, k, got[k], v)
			}
		}
	}
}
