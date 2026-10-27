package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// fakeRunner answers calls with reply, recording them.
type fakeRunner struct {
	calls []Call
	reply func(c Call) (stdout string, res Result, err error)
}

func (f *fakeRunner) Run(_ context.Context, c Call) (Result, error) {
	f.calls = append(f.calls, c)
	out, res, err := "", Result{}, error(nil)
	if f.reply != nil {
		out, res, err = f.reply(c)
	}
	if c.Stdout != nil {
		_, _ = io.WriteString(c.Stdout, out)
	}
	return res, err
}

func (f *fakeRunner) last() Call { return f.calls[len(f.calls)-1] }

var testRepo = Repo{Remote: "nas", Path: "/volume1/backups/deskyard-backups", ConfigDir: "/data/secret/remotes/nas", KeyDir: "/data/secret/backups/home"}

func testTool(f *fakeRunner) Tool {
	return Tool{Run: f, Host: "deskyard-0123456789ab", CacheDir: "/data/cache/restic"}
}

const secretValue = "sftp-secret"

func noSecrets(t *testing.T, c Call) {
	t.Helper()
	all := strings.Join(c.Args, " ")
	for k, v := range c.Env {
		all += " " + k + "=" + v
	}
	if strings.Contains(all, secretValue) || strings.Contains(all, "pw") && !strings.Contains(all, "PASSWORD_FILE") {
		t.Fatalf("secret in call: %s", all)
	}
	for _, k := range []string{"RESTIC_PASSWORD", "AWS_SECRET_ACCESS_KEY", "RCLONE_PASS"} {
		if _, ok := c.Env[k]; ok {
			t.Fatalf("secret variable %s", k)
		}
	}
}

func TestBackupArgsAndEnv(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return `{"message_type":"summary","snapshot_id":"abcdef0123456789"}` + "\n", Result{}, nil
	}}
	src := []engine.Mount{{Source: "/data/volume/photos", Target: "/deskyard/volume/photos", ReadOnly: true}}
	if _, _, err := testTool(f).Backup(context.Background(), testRepo, src, []string{"/deskyard/volume/photos"}, []string{"deskyard", "volume:photos", "manual"}, nil); err != nil {
		t.Fatal(err)
	}
	c := f.last()
	noSecrets(t, c)
	want := "restic backup --json --host deskyard-0123456789ab --exclude-caches --retry-lock 10m --tag deskyard --tag volume:photos --tag manual /deskyard/volume/photos"
	if got := strings.Join(c.Args, " "); got != want {
		t.Fatalf("args\n got %s\nwant %s", got, want)
	}
	wantEnv := map[string]string{
		"RCLONE_CONFIG":        RemoteMount + "/rclone.conf",
		"RESTIC_PASSWORD_FILE": RepoMount + "/restic-password",
		"RESTIC_CACHE_DIR":     "/cache",
		"RESTIC_REPOSITORY":    "rclone:nas:/volume1/backups/deskyard-backups",
	}
	for k, v := range wantEnv {
		if c.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, c.Env[k], v)
		}
	}
	mounts := map[string]engine.Mount{}
	for _, m := range c.Mounts {
		mounts[m.Target] = m
	}
	if m := mounts[RemoteMount]; m.Source != testRepo.ConfigDir || !m.ReadOnly {
		t.Errorf("remote mount %+v", m)
	}
	if m := mounts[RepoMount]; m.Source != testRepo.KeyDir || !m.ReadOnly {
		t.Errorf("key mount %+v", m)
	}
	if m := mounts["/cache"]; m.Source != "/data/cache/restic" || m.ReadOnly {
		t.Errorf("cache mount %+v", m)
	}
	if m := mounts["/deskyard/volume/photos"]; m != src[0] {
		t.Errorf("source mount %+v", m)
	}
}

func TestBackupParsesSummary(t *testing.T) {
	out := `{"message_type":"status","percent_done":0.25,"total_files":4,"files_done":1,"total_bytes":400,"bytes_done":100}
{"message_type":"status","percent_done":1,"total_files":4,"files_done":4,"total_bytes":400,"bytes_done":400}
{"message_type":"summary","files_new":4,"data_added":420,"total_bytes_processed":400,"snapshot_id":"abcdef0123456789"}
`
	f := &fakeRunner{reply: func(Call) (string, Result, error) { return out, Result{}, nil }}
	var seen []float64
	id, _, err := testTool(f).Backup(context.Background(), testRepo, nil, []string{"/x"}, nil, func(frac float64, msg string) { seen = append(seen, frac) })
	if err != nil || id != "abcdef0123456789" {
		t.Fatalf("id %q err %v", id, err)
	}
	if len(seen) != 2 || seen[0] != 0.25 || seen[1] != 1 {
		t.Fatalf("progress %v", seen)
	}
}

func TestBackupFailureCarriesStderr(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return "", Result{Code: 1, Stderr: "Fatal: unable to open repository at rclone:nas:x: connection refused\n"}, nil
	}}
	_, _, err := testTool(f).Backup(context.Background(), testRepo, nil, []string{"/x"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("got %v", err)
	}
}

const xattrErr = `{"message_type":"error","error":{"message":"incomplete metadata for /x/link: xattr.list /x/link: no such file or directory"},"during":"archival","item":"/x/link"}`

// Homes link into the desktop image, so their links dangle in a helper:
// restic can't list those links' xattrs. That loses nothing and isn't a warning.
func TestBackupIgnoresDanglingSymlinkXattrs(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return `{"message_type":"summary","snapshot_id":"abc"}` + "\n", Result{Code: 3, Stderr: xattrErr + "\n" + `{"message_type":"exit_error","code":3,"message":"Warning: at least one source file could not be read"}` + "\n"}, nil
	}}
	id, warnings, err := testTool(f).Backup(context.Background(), testRepo, nil, []string{"/x"}, nil, nil)
	if err != nil || id != "abc" || len(warnings) != 0 {
		t.Fatalf("%q %v %v", id, warnings, err)
	}
}

// Exit 3 is a snapshot with some files unreadable: it is made, with warnings.
func TestBackupUnreadableFilesWarn(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return `{"message_type":"summary","snapshot_id":"abc"}` + "\n", Result{Code: 3, Stderr: xattrErr + "\n" +
			`{"message_type":"error","error":{"message":"open /x/locked: permission denied"},"during":"archival","item":"/x/locked"}` + "\n"}, nil
	}}
	id, warnings, err := testTool(f).Backup(context.Background(), testRepo, nil, []string{"/x"}, nil, nil)
	if err != nil || id != "abc" || len(warnings) != 1 || !strings.Contains(warnings[0], "permission denied") {
		t.Fatalf("%q %v %v", id, warnings, err)
	}
}

func TestRestoreIgnoresDanglingSymlinkXattrs(t *testing.T) {
	restoreXattr := `{"message_type":"error","error":{"message":"xattr.list /target/.claude/skills/b: no such file or directory"},"during":"restore","item":"/.claude/skills/b"}`
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return "", Result{Code: 1, Stderr: restoreXattr + "\n" + restoreXattr + "\n" + `{"message_type":"exit_error","code":1,"message":"Fatal: There were 2 errors"}` + "\n"}, nil
	}}
	if err := testTool(f).Restore(context.Background(), testRepo, "a", "/x", engine.Mount{Source: "/t"}, nil, false, nil); err != nil {
		t.Fatalf("harmless errors failed the restore: %v", err)
	}
	f.reply = func(Call) (string, Result, error) {
		return "", Result{Code: 1, Stderr: restoreXattr + "\n" + `{"message_type":"error","error":{"message":"write /target/big: no space left on device"},"during":"restore","item":"/big"}` + "\n" + `{"message_type":"exit_error","code":1,"message":"Fatal: There were 2 errors"}` + "\n"}, nil
	}
	err := testTool(f).Restore(context.Background(), testRepo, "a", "/x", engine.Mount{Source: "/t"}, nil, false, nil)
	if err == nil || !strings.Contains(err.Error(), "no space left") || strings.Contains(err.Error(), "xattr") {
		t.Fatalf("got %v", err)
	}
}

// restic --json reports a fatal error as JSON on stderr.
func TestFailureReadsJSONExitError(t *testing.T) {
	err := failure("backing up", Result{Code: 1, Stderr: "rclone: NOTICE: something\n" + `{"message_type":"exit_error","code":1,"message":"Fatal: unable to open repository: 401"}` + "\n"})
	if err == nil || err.Error() != "backing up: unable to open repository: 401" {
		t.Fatalf("got %v", err)
	}
}

func TestRepoExistsCodes(t *testing.T) {
	for code, want := range map[int]error{0: nil, 10: ErrNoRepo, 12: ErrWrongKey, 1: errors.New("other")} {
		f := &fakeRunner{reply: func(Call) (string, Result, error) { return "", Result{Code: code, Stderr: "Fatal: boom"}, nil }}
		err := testTool(f).CheckRepo(context.Background(), testRepo)
		switch {
		case want == nil && err != nil, want != nil && err == nil:
			t.Errorf("code %d: %v", code, err)
		case code == 10 && !errors.Is(err, ErrNoRepo), code == 12 && !errors.Is(err, ErrWrongKey):
			t.Errorf("code %d: %v", code, err)
		}
		if got := strings.Join(f.last().Args, " "); got != "restic cat config" {
			t.Errorf("args %s", got)
		}
	}
}

func TestSnapshotsParse(t *testing.T) {
	out := `[{"time":"2026-10-27T03:00:01.5+01:00","paths":["/deskyard/volume/photos"],"hostname":"deskyard-0123456789ab","tags":["deskyard","volume:photos","scheduled"],"id":"aaa111","short_id":"aaa1","summary":{"data_added":1024,"total_bytes_processed":4096}}]`
	f := &fakeRunner{reply: func(Call) (string, Result, error) { return out, Result{}, nil }}
	snaps, err := testTool(f).Snapshots(context.Background(), testRepo, "deskyard")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.last().Args, " "); got != "restic snapshots --json --no-lock --tag deskyard" {
		t.Errorf("args %s", got)
	}
	if len(snaps) != 1 || snaps[0].ID != "aaa111" || snaps[0].Host != "deskyard-0123456789ab" || snaps[0].Summary.DataAdded != 1024 || snaps[0].Summary.TotalBytesProcessed != 4096 || snaps[0].Time.IsZero() || snaps[0].Tags[1] != "volume:photos" {
		t.Fatalf("%+v", snaps)
	}
}

func TestLsParsesOneLevel(t *testing.T) {
	out := `{"time":"2026-10-27T03:00:01Z","paths":["/deskyard/volume/photos"],"id":"aaa111","message_type":"snapshot","struct_type":"snapshot"}
{"name":"2026","type":"dir","path":"/deskyard/volume/photos/2026","mtime":"2026-10-20T10:00:00Z","message_type":"node","struct_type":"node"}
{"name":"photos","type":"dir","path":"/deskyard/volume/photos","mtime":"2026-10-20T10:00:00Z","message_type":"node","struct_type":"node"}
{"name":"a.jpg","type":"file","path":"/deskyard/volume/photos/a.jpg","size":12,"mtime":"2026-10-21T10:00:00Z","message_type":"node","struct_type":"node"}
`
	f := &fakeRunner{reply: func(Call) (string, Result, error) { return out, Result{}, nil }}
	entries, err := testTool(f).Ls(context.Background(), testRepo, "aaa111", "/deskyard/volume/photos")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.last().Args, " "); got != "restic ls --json --no-lock aaa111 /deskyard/volume/photos" {
		t.Errorf("args %s", got)
	}
	if len(entries) != 2 || entries[0].Name != "2026" || entries[0].Type != "dir" || entries[1].Name != "a.jpg" || entries[1].Size != 12 {
		t.Fatalf("%+v", entries)
	}
}

func TestForgetArgs(t *testing.T) {
	f := &fakeRunner{}
	if err := testTool(f).Forget(context.Background(), testRepo, "volume:photos", DefaultRetention); err != nil {
		t.Fatal(err)
	}
	want := "restic forget --retry-lock 10m --host deskyard-0123456789ab --tag volume:photos --keep-daily 7 --keep-weekly 4 --keep-monthly 12"
	if got := strings.Join(f.last().Args, " "); got != want {
		t.Fatalf("args\n got %s\nwant %s", got, want)
	}
	if err := testTool(f).Forget(context.Background(), testRepo, "volume:photos", Retention{}); err == nil {
		t.Fatal("forgot everything")
	}
}

func TestRestoreArgs(t *testing.T) {
	f := &fakeRunner{}
	target := engine.Mount{Source: "/data/home/alice", Target: "/target"}
	if err := testTool(f).Restore(context.Background(), testRepo, "aaa111", "/deskyard/environment/alice/home", target, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.last().Args, " "); got != "restic restore --json --retry-lock 10m --target /target --delete aaa111:/deskyard/environment/alice/home" {
		t.Errorf("args %s", got)
	}
	if err := testTool(f).Restore(context.Background(), testRepo, "aaa111", "/deskyard/volume/photos", target, []string{"/a.jpg", "/2026"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.last().Args, " "); got != "restic restore --json --retry-lock 10m --target /target --include /a.jpg --include /2026 aaa111:/deskyard/volume/photos" {
		t.Errorf("args %s", got)
	}
}

func TestTestRemoteArgs(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) {
		return "", Result{Code: 1, Stderr: "2026/10/27 ERROR : : error listing: couldn't connect SSH: ssh: handshake failed\n"}, nil
	}}
	err := testTool(f).TestRemote(context.Background(), "/tmp/conf", "nas", "/volume1")
	if err == nil || !strings.Contains(err.Error(), "handshake failed") {
		t.Fatalf("got %v", err)
	}
	if got := strings.Join(f.last().Args, " "); got != "rclone lsf --max-depth 1 --contimeout 15s --timeout 30s nas:/volume1" {
		t.Errorf("args %s", got)
	}
}
