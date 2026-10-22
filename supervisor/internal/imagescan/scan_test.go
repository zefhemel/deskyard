package imagescan

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	content string
	mode    fs.FileMode
	link    string // a symlink to this, instead of a file
}

const (
	passwd = "root:x:0:0:root:/root:/bin/bash\nuser:x:1000:1000::/workspace:/bin/bash\n"
	shadow = "root:*:20000:0:99999:7:::\nuser:!:20000:0:99999:7:::\n"
	group  = "root:x:0:\nusers:x:100:\nuser:x:1000:\n"
)

func baseTree() map[string]entry {
	return map[string]entry{
		"etc/passwd":                     {content: passwd, mode: 0o644},
		"etc/shadow":                     {content: shadow, mode: 0o640},
		"etc/group":                      {content: group, mode: 0o644},
		"usr/bin/su":                     {content: "su v1", mode: fs.ModeSetuid | 0o755},
		"usr/bin/ls":                     {content: "ls", mode: 0o755},
		"usr/lib/libx.so":                {content: "x", mode: 0o644},
		"usr/bin/ok":                     {link: "../lib/libx.so"},
		"opt/deskyard/home-template/.rc": {content: "rc", mode: 0o666},
		"tmp/scratch":                    {content: "t", mode: 0o666},
		"workspace/mine":                 {content: "m", mode: 0o666},
		"proc/1/environ":                 {content: "skipped", mode: 0o666},
	}
}

func makeTree(t *testing.T, files map[string]entry) string {
	t.Helper()
	root := t.TempDir()
	for p, e := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if e.link != "" {
			if err := os.Symlink(e.link, full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(full, []byte(e.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, e.mode); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(full); fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid) != e.mode&(fs.ModeSetuid|fs.ModeSetgid) {
			t.Skipf("this filesystem doesn't keep setuid bits for %s", p)
		}
	}
	return root
}

var opts = Options{UserUID: 1000} // nobody owns the test files as 1000

func scan(t *testing.T, files map[string]entry) Result {
	t.Helper()
	r, err := Scan(makeTree(t, files), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Through the scanner's own output format.
	var buf bytes.Buffer
	if err := Write(&buf, r); err != nil {
		t.Fatal(err)
	}
	r2, err := Parse(buf.Bytes(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return r2
}

func compare(t *testing.T, change func(map[string]entry)) []string {
	t.Helper()
	base := scan(t, baseTree())
	files := baseTree()
	change(files)
	return Compare(base, scan(t, files), opts)
}

func TestUnchangedImagePasses(t *testing.T) {
	if got := compare(t, func(map[string]entry) {}); len(got) != 0 {
		t.Fatalf("findings: %q", got)
	}
}

func TestFindings(t *testing.T) {
	got := compare(t, func(f map[string]entry) {
		f["usr/bin/su"] = entry{content: "su v2 (replaced)", mode: fs.ModeSetuid | 0o755}
		f["usr/local/bin/x"] = entry{content: "x", mode: fs.ModeSetgid | 0o755}
		f["usr/bin/sudo"] = entry{content: "sudo", mode: 0o755}
		f["etc/foo"] = entry{content: "foo", mode: 0o666}
		f["etc/supervisor/conf.d/x.conf"] = entry{content: "x", mode: 0o664}
		f["opt/deskyard/home-template/new"] = entry{content: "fine", mode: 0o666}
		f["usr/bin/evil"] = entry{link: "/workspace/evil"}
		f["usr/bin/chain"] = entry{link: "/var/x"}
		f["var/x"] = entry{link: "../tmp/z"}
		f["usr/bin/towritable"] = entry{link: "/srv/open/tool"}
		f["srv/open/tool"] = entry{content: "w", mode: 0o666}
		f["var/tmp/elsewhere"] = entry{content: "not a root-run place", mode: 0o666}
	})
	want := []string{
		"/etc/foo (writable by user)",
		"/etc/supervisor/conf.d/x.conf (writable by user)",
		"/usr/bin/chain (links into the user's reach: /tmp/z)",
		"/usr/bin/evil (links into the user's reach: /workspace/evil)",
		"/usr/bin/su (setuid or setgid, changed from the base)",
		"/usr/bin/sudo (sudo or doas)",
		"/usr/bin/towritable (links into the user's reach: /srv/open/tool)",
		"/usr/local/bin/x (setuid or setgid)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAccountChanges(t *testing.T) {
	for _, c := range []struct {
		name, passwd, shadow, group, want string
	}{
		{"root password removed", passwd, "root::20000:0:99999:7:::\nuser:!:20000:0:99999:7:::\n", group, "account root (UID 0 with no password)"},
		{"root password set", passwd, "root:$6$abc:20000:0:99999:7:::\nuser:!:20000:0:99999:7:::\n", group, "account root (UID 0, password changed from the base)"},
		{"second root", passwd + "toor:x:0:0::/root:/bin/sh\n", shadow + "toor:*:1:::::\n", group, "account toor (a new UID 0 account)"},
		{"user not 1000", "root:x:0:0:root:/root:/bin/bash\nuser:x:1001:1000::/workspace:/bin/bash\n", shadow, group, "account user (UID 1001, not 1000)"},
		{"user in root's group", passwd, shadow, "root:x:0:user\nusers:x:100:\nuser:x:1000:\n", "group root (user added to it)"},
		{"a second GID 0 group", passwd, shadow, group + "wheel:x:0:\n", "group wheel (a new GID 0 group)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := compare(t, func(f map[string]entry) {
				f["etc/passwd"] = entry{content: c.passwd, mode: 0o644}
				f["etc/shadow"] = entry{content: c.shadow, mode: 0o640}
				f["etc/group"] = entry{content: c.group, mode: 0o644}
			})
			if !contains(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCapability(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file capabilities exist on Linux only")
	}
	if os.Geteuid() != 0 {
		t.Skip("setting security.capability needs root (CAP_SETFCAP)")
	}
	files := baseTree()
	files["usr/bin/python3"] = entry{content: "py", mode: 0o755}
	root := makeTree(t, files)
	base, err := Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	// cap_setuid+ep, as setcap writes it (VFS_CAP_REVISION_2).
	v := []byte{0, 0, 0, 2, 0x80, 0, 0, 0, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := setxattr(filepath.Join(root, "usr/bin/python3"), "security.capability", v); err != nil {
		t.Skipf("can't set the attribute here: %v", err)
	}
	cur, err := Scan(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := Compare(base, cur, opts); len(got) != 1 || got[0] != "/usr/bin/python3 (file capabilities)" {
		t.Fatalf("got %q", got)
	}
}

func TestParseFailsClosed(t *testing.T) {
	var good bytes.Buffer
	_ = Write(&good, Result{Files: []File{}, Accounts: []Account{{Name: "root"}}, Groups: []Group{}})
	if _, err := Parse(good.Bytes(), 0); err != nil {
		t.Fatalf("good output: %v", err)
	}
	for name, c := range map[string]struct {
		out  string
		code int
	}{
		"non-zero exit":   {good.String(), 1},
		"no sentinel":     {strings.TrimSuffix(good.String(), Sentinel+"\n"), 0},
		"cut short":       {good.String()[:20], 0},
		"garbage":         {"not json\n" + Sentinel + "\n", 0},
		"empty":           {"", 0},
		"no accounts":     {`{"files":[],"accounts":[],"groups":[]}` + "\n" + Sentinel + "\n", 0},
		"missing files":   {`{"accounts":[{"name":"root"}],"groups":[]}` + "\n" + Sentinel + "\n", 0},
		"extra data":      {`{"files":[],"accounts":[{"name":"root"}],"groups":[]} {}` + "\n" + Sentinel + "\n", 0},
		"unknown fields":  {`{"files":[],"accounts":[{"name":"root"}],"groups":[],"x":1}` + "\n" + Sentinel + "\n", 0},
		"sentinel inside": {Sentinel + "\n" + good.String()[:10] + "\n" + Sentinel + "x\n", 0},
	} {
		if _, err := Parse([]byte(c.out), c.code); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestScanNeedsAccounts(t *testing.T) {
	files := baseTree()
	delete(files, "etc/passwd")
	if _, err := Scan(makeTree(t, files), opts); err == nil {
		t.Fatal("scanned an image without /etc/passwd")
	}
}
