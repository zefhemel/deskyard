package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

var (
	// ErrNoRepo: no restic repository at that location.
	ErrNoRepo = errors.New("no backup repository there")
	// ErrWrongKey: a repository is there, but the recovery key doesn't open it.
	ErrWrongKey = errors.New("the recovery key doesn't open that repository")
)

// RepoMount is where a helper finds a repository's key folder,
// secret/backups/<repository>/, holding restic-password.
const RepoMount = "/secret/repo"

// Repo is where restic reaches a repository: a folder Path on Remote,
// through the remote's configuration folder, opened with the key in KeyDir
// (host paths).
type Repo struct {
	Remote, Path      string
	ConfigDir, KeyDir string
}

// URL is the repository as restic names it.
func (r Repo) URL() string { return "rclone:" + r.Remote + ":" + r.Path }

// Retention is how many snapshots of an item restic forget keeps, per period.
type Retention struct {
	Last    int `json:"last"`
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// DefaultRetention keeps 7 daily, 4 weekly and 12 monthly snapshots.
var DefaultRetention = Retention{Daily: 7, Weekly: 4, Monthly: 12}

func (k Retention) args() []string {
	var out []string
	for _, x := range []struct {
		flag string
		n    int
	}{{"--keep-last", k.Last}, {"--keep-hourly", k.Hourly}, {"--keep-daily", k.Daily}, {"--keep-weekly", k.Weekly}, {"--keep-monthly", k.Monthly}, {"--keep-yearly", k.Yearly}} {
		if x.n > 0 {
			out = append(out, x.flag, strconv.Itoa(x.n))
		}
	}
	return out
}

func (k Retention) Validate() error {
	for _, n := range []int{k.Last, k.Hourly, k.Daily, k.Weekly, k.Monthly, k.Yearly} {
		if n < 0 || n > 10000 {
			return errors.New("retention counts are 0 to 10000")
		}
	}
	if len(k.args()) == 0 {
		return errors.New("keep at least one snapshot")
	}
	return nil
}

// Snapshot is a restic snapshot, as restic snapshots --json describes it.
type Snapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Host    string    `json:"hostname"`
	Tags    []string  `json:"tags"`
	Paths   []string  `json:"paths"`
	Summary *struct {
		DataAdded           int64 `json:"data_added"`
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	} `json:"summary,omitempty"`
}

// Entry is a file or folder in a snapshot.
type Entry struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	Type  string    `json:"type"` // file, dir, symlink, …
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// Tool runs rclone and restic for this supervisor: Host is its restic host
// name, CacheDir the host folder for restic's cache.
type Tool struct {
	Run      Runner
	Host     string
	CacheDir string
}

// call is a restic call on repo, with extra mounts.
func (t Tool) call(repo Repo, mounts []engine.Mount, args ...string) Call {
	m := []engine.Mount{{Source: repo.ConfigDir, Target: RemoteMount, ReadOnly: true}}
	if repo.KeyDir != "" {
		m = append(m, engine.Mount{Source: repo.KeyDir, Target: RepoMount, ReadOnly: true})
	}
	if t.CacheDir != "" {
		m = append(m, engine.Mount{Source: t.CacheDir, Target: "/cache"})
	}
	return Call{
		Mounts: append(m, mounts...),
		Env: map[string]string{
			"RCLONE_CONFIG":        RemoteMount + "/rclone.conf",
			"RESTIC_PASSWORD_FILE": RepoMount + "/restic-password",
			"RESTIC_CACHE_DIR":     "/cache",
			"RESTIC_REPOSITORY":    repo.URL(),
		},
		Args: append([]string{"restic"}, args...),
	}
}

// failure is the error of a failed command: restic's JSON exit error, or
// else its last meaningful stderr line.
func failure(what string, res Result) error {
	msg := ""
	for _, l := range strings.Split(res.Stderr, "\n") {
		l = strings.TrimSpace(l)
		var e struct {
			MessageType string `json:"message_type"`
			Message     string `json:"message"`
		}
		if json.Unmarshal([]byte(l), &e) == nil && e.MessageType != "" {
			if e.MessageType == "exit_error" {
				msg = e.Message
			}
			continue
		}
		if l != "" && (msg == "" || !strings.HasPrefix(msg, "Fatal:") || strings.HasPrefix(l, "Fatal:")) {
			msg = l // the last Fatal: line, or else the last line
		}
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", res.Code)
	}
	return fmt.Errorf("%s: %s", what, strings.TrimPrefix(msg, "Fatal: "))
}

func (t Tool) run(ctx context.Context, what string, c Call) error {
	res, err := t.Run.Run(ctx, c)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if res.Code != 0 {
		return failure(what, res)
	}
	return nil
}

// TestRemote lists the remote's root through its rclone configuration in
// configDir, with bounded timeouts.
func (t Tool) TestRemote(ctx context.Context, configDir, remote, root string) error {
	return t.run(ctx, "connecting to "+remote, Call{
		Mounts: []engine.Mount{{Source: configDir, Target: RemoteMount, ReadOnly: true}},
		Env:    map[string]string{"RCLONE_CONFIG": RemoteMount + "/rclone.conf"},
		Args:   []string{"rclone", "lsf", "--max-depth", "1", "--contimeout", "15s", "--timeout", "30s", remote + ":" + root},
	})
}

// CheckRepo opens repo: nil when it is there and the key fits, ErrNoRepo or
// ErrWrongKey, or another failure.
func (t Tool) CheckRepo(ctx context.Context, repo Repo) error {
	res, err := t.Run.Run(ctx, t.call(repo, nil, "cat", "config"))
	if err != nil {
		return err
	}
	switch res.Code {
	case 0:
		return nil
	case 10:
		return ErrNoRepo
	case 12:
		return ErrWrongKey
	}
	return failure("opening the repository", res)
}

func (t Tool) Init(ctx context.Context, repo Repo) error {
	return t.run(ctx, "creating the repository", t.call(repo, nil, "init"))
}

// Snapshots lists the repository's snapshots tagged tag.
func (t Tool) Snapshots(ctx context.Context, repo Repo, tag string) ([]Snapshot, error) {
	var out bytes.Buffer
	c := t.call(repo, nil, "snapshots", "--json", "--no-lock", "--tag", tag)
	c.Stdout = &out
	if err := t.run(ctx, "listing snapshots", c); err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out.Bytes(), &snaps); err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}
	return snaps, nil
}

// fileErrors are restic's per-file errors, reported as JSON on stderr with
// --json, split into the harmless and the others. restic lists extended
// attributes through symbolic links, so a link that dangles in the helper
// (a home's links into the desktop image do) is an xattr error that loses
// nothing: those are harmless.
func fileErrors(stderr string) (harmless int, others []string) {
	for _, l := range strings.Split(stderr, "\n") {
		var e progressLine
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &e) != nil || e.MessageType != "error" || e.Error == nil {
			continue
		}
		if strings.Contains(e.Error.Message, "xattr.list") && strings.Contains(e.Error.Message, "no such file or directory") {
			harmless++
			continue
		}
		others = append(others, strings.TrimSpace(e.Error.Message))
	}
	return harmless, others
}

func firstOf(errs []string, n int) string {
	if len(errs) > n {
		return strings.Join(errs[:n], "; ") + fmt.Sprintf(" and %d more", len(errs)-n)
	}
	return strings.Join(errs, "; ")
}

// progressLine is a line of restic's --json output for backup and restore.
type progressLine struct {
	MessageType   string  `json:"message_type"`
	PercentDone   float64 `json:"percent_done"`
	SnapshotID    string  `json:"snapshot_id"`
	TotalFiles    int64   `json:"total_files"`
	FilesDone     int64   `json:"files_done"`
	FilesRestored int64   `json:"files_restored"`
	Item          string  `json:"item"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error"`
	During string `json:"during"`
}

// progressWriter parses restic --json lines as they stream.
type progressWriter struct {
	buf      bytes.Buffer
	progress func(float64, string)
	snapshot string
	errs     []string
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for {
		line, err := p.buf.ReadBytes('\n')
		if err != nil {
			p.buf.Write(line) // incomplete: wait for the rest
			return len(b), nil
		}
		p.line(line)
	}
}

func (p *progressWriter) line(line []byte) {
	var l progressLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	switch l.MessageType {
	case "status":
		if p.progress != nil {
			done := l.FilesDone
			if l.FilesRestored > done {
				done = l.FilesRestored
			}
			p.progress(l.PercentDone, fmt.Sprintf("%d of %d files", done, l.TotalFiles))
		}
	case "summary":
		p.snapshot = l.SnapshotID
	case "error":
		if l.Error != nil && len(p.errs) < 5 {
			p.errs = append(p.errs, strings.TrimSpace(l.Item+": "+l.Error.Message))
		}
	}
}

// Backup backs up paths (inside the helper, from the src mounts) as a new
// snapshot tagged tags, and returns its ID. Files that couldn't be read are
// left out of it and returned as warnings.
func (t Tool) Backup(ctx context.Context, repo Repo, src []engine.Mount, paths, tags []string, progress func(float64, string)) (string, []string, error) {
	args := []string{"backup", "--json", "--host", t.Host, "--exclude-caches", "--retry-lock", "10m"}
	for _, tag := range tags {
		args = append(args, "--tag", tag)
	}
	args = append(args, paths...)
	pw := &progressWriter{progress: progress}
	c := t.call(repo, src, args...)
	c.Stdout = pw
	res, err := t.Run.Run(ctx, c)
	if err != nil {
		return "", nil, fmt.Errorf("backing up: %w", err)
	}
	_, others := fileErrors(res.Stderr)
	others = append(others, pw.errs...)
	switch {
	case res.Code == 3 && pw.snapshot != "":
		// A snapshot was made, without the files that couldn't be read.
		if len(others) > 20 {
			others = append(others[:20], fmt.Sprintf("and %d more", len(others)-20))
		}
		return pw.snapshot, others, nil
	case res.Code != 0:
		if len(others) > 0 {
			return "", nil, fmt.Errorf("%w (%s)", failure("backing up", res), firstOf(others, 3))
		}
		return "", nil, failure("backing up", res)
	case pw.snapshot == "":
		return "", nil, errors.New("backing up: restic reported no snapshot")
	}
	return pw.snapshot, nil, nil
}

// Forget forgets this host's snapshots tagged tag beyond keep.
func (t Tool) Forget(ctx context.Context, repo Repo, tag string, keep Retention) error {
	if err := keep.Validate(); err != nil {
		return err
	}
	args := append([]string{"forget", "--retry-lock", "10m", "--host", t.Host, "--tag", tag}, keep.args()...)
	return t.run(ctx, "forgetting old snapshots", t.call(repo, nil, args...))
}

func (t Tool) Prune(ctx context.Context, repo Repo) error {
	return t.run(ctx, "pruning the repository", t.call(repo, nil, "prune", "--retry-lock", "10m"))
}

// Ls lists the entries directly in dir of snapshot.
func (t Tool) Ls(ctx context.Context, repo Repo, snapshot, dir string) ([]Entry, error) {
	var out bytes.Buffer
	c := t.call(repo, nil, "ls", "--json", "--no-lock", snapshot, dir)
	c.Stdout = &out
	if err := t.run(ctx, "listing files", c); err != nil {
		return nil, err
	}
	var entries []Entry
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e struct {
			Entry
			StructType string `json:"struct_type"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.StructType != "node" || e.Path == dir {
			continue
		}
		entries = append(entries, e.Entry)
	}
	return entries, sc.Err()
}

// Dump writes path of snapshot to w: a file as itself, a folder as a zip.
func (t Tool) Dump(ctx context.Context, repo Repo, snapshot, path string, zip bool, w io.Writer) error {
	args := []string{"dump", "--no-lock"}
	if zip {
		args = append(args, "--archive", "zip")
	}
	c := t.call(repo, nil, append(args, snapshot, path)...)
	c.Stdout = w
	return t.run(ctx, "reading "+path, c)
}

// Restore restores subpath of snapshot into target (mounted at /target):
// only includes (paths relative to subpath) when given, and deleting files
// the snapshot doesn't have when del.
func (t Tool) Restore(ctx context.Context, repo Repo, snapshot, subpath string, target engine.Mount, includes []string, del bool, progress func(float64, string)) error {
	target.Target = "/target"
	args := []string{"restore", "--json", "--retry-lock", "10m", "--target", "/target"}
	for _, inc := range includes {
		args = append(args, "--include", literal(inc))
	}
	if del {
		args = append(args, "--delete")
	}
	args = append(args, snapshot+":"+subpath)
	pw := &progressWriter{progress: progress}
	c := t.call(repo, []engine.Mount{target}, args...)
	c.Stdout = pw
	res, err := t.Run.Run(ctx, c)
	if err != nil {
		return fmt.Errorf("restoring: %w", err)
	}
	if res.Code == 0 {
		return nil
	}
	harmless, others := fileErrors(res.Stderr)
	others = append(others, pw.errs...)
	if len(others) > 0 {
		return fmt.Errorf("restoring: %s", firstOf(others, 5))
	}
	// restic ends a restore that only had per-file errors with exit 1 and
	// "There were N errors". When those were all harmless (links' xattrs,
	// which can't be set on a dangling link), the restore is complete.
	var n int
	if _, err := fmt.Sscanf(exitMessage(res.Stderr), "Fatal: There were %d errors", &n); err == nil && res.Code == 1 && harmless > 0 && n == harmless {
		return nil
	}
	return failure("restoring", res)
}

// exitMessage is restic's JSON exit error message on stderr, or "".
func exitMessage(stderr string) string {
	msg := ""
	for _, l := range strings.Split(stderr, "\n") {
		var e struct {
			MessageType string `json:"message_type"`
			Message     string `json:"message"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &e) == nil && e.MessageType == "exit_error" {
			msg = e.Message
		}
	}
	return msg
}

// literal escapes a path for restic's --include patterns.
func literal(p string) string {
	var b strings.Builder
	for _, c := range p {
		if strings.ContainsRune(`*?[\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}
