// Package imagescan looks for ways a custom image could let its user (UID
// 1000) become root. The scan runs inside a throwaway container of the image
// as a static binary of its own (cmd/deskyard-image-scan), so it relies on
// nothing the image provides; the supervisor scans the base and the new
// image and compares the two (Compare).
package imagescan

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Sentinel is the scanner's last output line: output without it is cut short.
const Sentinel = "--deskyard-image-scan-complete--"

// Kinds of findings.
const (
	KindSudo       = "sudo"       // a sudo, sudoedit or doas executable
	KindSetuid     = "setuid"     // a setuid or setgid regular file
	KindCapability = "capability" // a file with file capabilities
	KindWritable   = "writable"   // writable by the user where root runs from
	KindSymlink    = "symlink"    // a link from there into the user's reach
)

// Result is what a scan finds, as JSON.
type Result struct {
	Files    []File    `json:"files"`
	Accounts []Account `json:"accounts"`
	Groups   []Group   `json:"groups"`
}

type File struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"` // sudo, setuid, capability
	Detail string `json:"detail,omitempty"` // the capability (hex), or the link's resolved target
}

// Account is a line of /etc/passwd, with its password (from /etc/shadow when
// passwd says "x") as a hash.
type Account struct {
	Name     string `json:"name"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Password string `json:"password"` // sha256 of the password field
	Empty    bool   `json:"empty"`    // no password at all
}

type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

// Options are fixed in the image; tests change them.
type Options struct {
	UserUID int // the desktop's user, 1000
}

// skipped are never walked: the kernel's, devices, and the user's own folder.
var skipped = []string{"/proc", "/sys", "/dev", "/workspace"}

// rootRuns are where root runs things from, so the user may write nothing
// there (the home template is the user's by design). /usr covers
// /usr/local/bin, /etc covers supervisord's config.
var rootRuns = []string{"/usr", "/etc", "/opt/workspace", "/opt/deskyard"}

const homeTemplate = "/opt/deskyard/home-template"

// ancestors are the folders above rootRuns ("/", "/opt"): a user who could
// write one could swap what's below it.
var ancestors = func() map[string]bool {
	m := map[string]bool{}
	for _, r := range rootRuns {
		for d := path.Dir(r); ; d = path.Dir(d) {
			m[d] = true
			if d == "/" {
				break
			}
		}
	}
	return m
}()

// userPlaces are where a link from rootRuns may not point.
var userPlaces = []string{"/workspace", "/tmp", "/var/tmp", "/home", "/dev/shm", "/run/workspace"}

func under(p string, dirs ...string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// Scan walks the filesystem at root (a container's "/", or a test folder),
// reading every path as if root were "/".
func Scan(root string, o Options) (Result, error) {
	res := Result{Files: []File{}}
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		p := "/" + strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(full, root)), "/")
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // gone meanwhile
			}
			return err
		}
		if d.IsDir() && under(p, skipped...) {
			return filepath.SkipDir
		}
		fi, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		f, err := check(root, p, full, fi, o)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		res.Files = append(res.Files, f...)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if res.Accounts, err = readAccounts(root); err != nil {
		return Result{}, err
	}
	if res.Groups, err = readGroups(root); err != nil {
		return Result{}, err
	}
	return res, nil
}

func check(root, p, full string, fi fs.FileInfo, o Options) ([]File, error) {
	var out []File
	mode := fi.Mode()
	regular := mode.IsRegular()
	base := path.Base(p)
	if (base == "sudo" || base == "sudoedit" || base == "doas") && (mode&fs.ModeSymlink != 0 || (regular && mode&0o111 != 0)) {
		f := File{Path: p, Kind: KindSudo}
		if regular {
			f.SHA256 = hashFile(full)
		}
		out = append(out, f)
	}
	if regular && mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
		out = append(out, File{Path: p, Kind: KindSetuid, SHA256: hashFile(full)})
	}
	if regular {
		cp, err := capability(full)
		if err != nil {
			return nil, err
		}
		if cp != nil {
			out = append(out, File{Path: p, Kind: KindCapability, SHA256: hashFile(full), Detail: hex.EncodeToString(cp)})
		}
	}
	if (under(p, rootRuns...) && !under(p, homeTemplate)) || ancestors[p] {
		if mode&fs.ModeSymlink != 0 {
			target, err := resolve(root, p)
			if err != nil {
				return nil, err
			}
			if under(target, userPlaces...) || reachable(root, target, o) {
				out = append(out, File{Path: p, Kind: KindSymlink, Detail: target})
			}
		} else if writable(fi, o) {
			out = append(out, File{Path: p, Kind: KindWritable})
		}
	}
	return out, nil
}

// writable: the user owns it (and so can make it writable), or its group or
// everyone may write it.
func writable(fi fs.FileInfo, o Options) bool {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == o.UserUID {
		return true
	}
	return fi.Mode().Perm()&0o022 != 0
}

// reachable: the user can write target, or could create it (the nearest
// existing folder above it is writable).
func reachable(root, target string, o Options) bool {
	for p := target; ; p = path.Dir(p) {
		fi, err := os.Lstat(filepath.Join(root, p))
		if err == nil {
			return writable(fi, o)
		}
		if p == "/" {
			return false
		}
	}
}

// resolve follows the links in p inside root, as the kernel would with root
// as "/". A missing part ends the walk; the rest is kept as written.
func resolve(root, p string) (string, error) {
	todo := strings.Split(strings.Trim(p, "/"), "/")
	cur := "/"
	for hops := 0; len(todo) > 0; {
		c := todo[0]
		todo = todo[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = path.Dir(cur)
			continue
		}
		next := path.Join(cur, c)
		fi, err := os.Lstat(filepath.Join(root, next))
		if err != nil {
			return path.Join(append([]string{next}, todo...)...), nil
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > 40 {
			return "", errors.New("too many levels of symbolic links")
		}
		t, err := os.Readlink(filepath.Join(root, next))
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(t, "/") {
			cur = "/"
		}
		todo = append(strings.Split(t, "/"), todo...)
	}
	return cur, nil
}

func hashFile(name string) string {
	f, err := os.Open(name)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func readLines(name string) ([][]string, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var out [][]string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Split(line, ":"))
	}
	return out, sc.Err()
}

func readAccounts(root string) ([]Account, error) {
	lines, err := readLines(filepath.Join(root, "etc/passwd"))
	if err != nil {
		return nil, fmt.Errorf("reading /etc/passwd: %w", err)
	}
	shadow := map[string]string{}
	if sl, err := readLines(filepath.Join(root, "etc/shadow")); err == nil {
		for _, f := range sl {
			if len(f) >= 2 {
				shadow[f[0]] = f[1]
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading /etc/shadow: %w", err)
	}
	var out []Account
	for _, f := range lines {
		if len(f) < 4 {
			return nil, fmt.Errorf("/etc/passwd: bad line for %q", f[0])
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("/etc/passwd: bad ids for %q", f[0])
		}
		pw := f[1]
		if pw == "x" {
			if s, ok := shadow[f[0]]; ok {
				pw = s
			}
		}
		out = append(out, Account{Name: f[0], UID: uid, GID: gid, Password: hashString(pw), Empty: pw == ""})
	}
	if len(out) == 0 {
		return nil, errors.New("/etc/passwd has no accounts")
	}
	return out, nil
}

func readGroups(root string) ([]Group, error) {
	lines, err := readLines(filepath.Join(root, "etc/group"))
	if err != nil {
		return nil, fmt.Errorf("reading /etc/group: %w", err)
	}
	out := []Group{}
	for _, f := range lines {
		if len(f) < 3 {
			return nil, fmt.Errorf("/etc/group: bad line for %q", f[0])
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("/etc/group: bad id for %q", f[0])
		}
		g := Group{Name: f[0], GID: gid, Members: []string{}}
		if len(f) > 3 && f[3] != "" {
			g.Members = strings.Split(f[3], ",")
		}
		out = append(out, g)
	}
	return out, nil
}

// Write prints r as the scanner's output: JSON, then the sentinel line.
func Write(w io.Writer, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n%s\n", b, Sentinel)
	return err
}

// Parse reads a scanner's output and exit code. Anything but a complete,
// well-formed result is an error: the build is refused rather than passed.
func Parse(out []byte, code int) (Result, error) {
	if code != 0 {
		return Result{}, fmt.Errorf("the scanner failed (exit %d)", code)
	}
	body, ok := bytes.CutSuffix(out, []byte("\n"+Sentinel+"\n"))
	if !ok {
		return Result{}, errors.New("the scanner's output is incomplete")
	}
	var r Result
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Result{}, fmt.Errorf("the scanner's output is unreadable: %w", err)
	}
	if dec.More() {
		return Result{}, errors.New("the scanner's output has extra data")
	}
	if r.Files == nil || len(r.Accounts) == 0 || r.Groups == nil {
		return Result{}, errors.New("the scanner's output is incomplete")
	}
	return r, nil
}

var reasons = map[string]string{
	KindSudo:       "sudo or doas",
	KindSetuid:     "setuid or setgid",
	KindCapability: "file capabilities",
	KindWritable:   "writable by user",
	KindSymlink:    "links into the user's reach",
}

// Compare lists what cur adds over base that could give the user root, one
// "what (why)" per line; empty means nothing.
func Compare(base, cur Result, o Options) []string {
	var out []string
	before := map[string]File{}
	for _, f := range base.Files {
		before[f.Kind+"\x00"+f.Path] = f
	}
	for _, f := range cur.Files {
		b, ok := before[f.Kind+"\x00"+f.Path]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s (%s)", f.Path, reasonFor(f)))
		case b.SHA256 != f.SHA256 || b.Detail != f.Detail:
			out = append(out, fmt.Sprintf("%s (%s, changed from the base)", f.Path, reasonFor(f)))
		}
	}

	accounts := map[string]Account{}
	for _, a := range base.Accounts {
		accounts[a.Name] = a
	}
	user := false
	for _, a := range cur.Accounts {
		b, ok := accounts[a.Name]
		if a.Name == "user" {
			user = true
			if a.UID != o.UserUID {
				out = append(out, fmt.Sprintf("account user (UID %d, not %d)", a.UID, o.UserUID))
			}
		}
		if a.UID == 0 && (!ok || b.UID != 0) {
			out = append(out, fmt.Sprintf("account %s (a new UID 0 account)", a.Name))
		}
		if a.GID == 0 && (!ok || b.GID != 0) {
			out = append(out, fmt.Sprintf("account %s (a new GID 0 account)", a.Name))
		}
		if a.UID == 0 && a.Empty {
			out = append(out, fmt.Sprintf("account %s (UID 0 with no password)", a.Name))
		} else if a.UID == 0 && ok && b.UID == 0 && a.Password != b.Password {
			out = append(out, fmt.Sprintf("account %s (UID 0, password changed from the base)", a.Name))
		}
	}
	if !user {
		out = append(out, "account user (missing)")
	}

	groups := map[string]Group{}
	for _, g := range base.Groups {
		groups[g.Name] = g
	}
	for _, g := range cur.Groups {
		b, ok := groups[g.Name]
		if g.GID == 0 && (!ok || b.GID != 0) {
			out = append(out, fmt.Sprintf("group %s (a new GID 0 group)", g.Name))
		}
		if contains(g.Members, "user") && (!ok || !contains(b.Members, "user") || b.GID != g.GID) {
			out = append(out, fmt.Sprintf("group %s (user added to it)", g.Name))
		}
	}
	sort.Strings(out)
	return out
}

func reasonFor(f File) string {
	if f.Kind == KindSymlink {
		return reasons[f.Kind] + ": " + f.Detail
	}
	return reasons[f.Kind]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
