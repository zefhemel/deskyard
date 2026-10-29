package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
)

const snapsJSON = `[
{"time":"2026-10-26T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","environment:alice","scheduled"],"id":"e1","short_id":"e1"},
{"time":"2026-10-26T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","volume:photos","scheduled"],"id":"v1","short_id":"v1"}]`

// restoreReply answers like a repository holding snapsJSON; restores fail
// when failRestore.
func restoreReply(def computers.Definition, failRestore *bool) func(Call) (string, Result, error) {
	return func(c Call) (string, Result, error) {
		switch c.Args[1] {
		case "snapshots":
			return snapsJSON, Result{}, nil
		case "dump":
			b, _ := json.Marshal(def)
			return string(b), Result{}, nil
		case "restore":
			if failRestore != nil && *failRestore {
				return "", Result{Code: 1, Stderr: "Fatal: connection lost"}, nil
			}
		}
		return resticReply(c)
	}
}

func restoreCalls(f *fakeRunner) []Call {
	var out []Call
	for _, c := range f.calls {
		if len(c.Args) > 1 && c.Args[1] == "restore" {
			out = append(out, c)
		}
	}
	return out
}

func TestRestoreHereComputerStopsAndStarts(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addComputer(t, "alice", true, computers.Settings{Image: "img"})
	it := Item{KindComputer, "alice"}
	_ = s.Store.PutPolicy(policyOf(it))
	if _, err := s.Restore(context.Background(), it, RestoreRequest{Snapshot: "e1", Mode: "here"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.events(); got != "reserve alice,stop alice,start alice" {
		t.Fatalf("lifecycle %s", got)
	}
	rs := restoreCalls(f)
	if len(rs) != 1 {
		t.Fatalf("restores %d", len(rs))
	}
	args := strings.Join(rs[0].Args, " ")
	if !strings.Contains(args, "--delete") || !strings.HasSuffix(args, "e1:/deskyard/environment/alice/home") {
		t.Fatalf("args %s", args)
	}
	for _, m := range rs[0].Mounts {
		if m.Target == "/target" && (m.Source != env.layout.HomeOf("alice") || m.ReadOnly) {
			t.Fatalf("target %+v", m)
		}
	}
	if env.reserved["alice"] {
		t.Fatal("reservation not released")
	}
}

func TestRestoreHereStoppedStaysStopped(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addComputer(t, "alice", false, computers.Settings{Image: "img"})
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "here"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.events(); got != "reserve alice" {
		t.Fatalf("lifecycle %s", got)
	}
}

func TestRestoreHereFailureLeavesStopped(t *testing.T) {
	fail := true
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, &fail)
	env.addComputer(t, "alice", true, computers.Settings{Image: "img"})
	it := Item{KindComputer, "alice"}
	_ = s.Store.PutPolicy(policyOf(it))
	_, err := s.Restore(context.Background(), it, RestoreRequest{Snapshot: "e1", Mode: "here"}, nil)
	if err == nil || !strings.Contains(err.Error(), "connection lost") || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("got %v", err)
	}
	if got := env.events(); got != "reserve alice,stop alice" || env.running["alice"] {
		t.Fatalf("lifecycle %s", got)
	}
}

func TestRestoreHereBackupFirst(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addComputer(t, "alice", false, computers.Settings{Image: "img"})
	it := Item{KindComputer, "alice"}
	_ = s.Store.PutPolicy(policyOf(it))
	if _, err := s.Restore(context.Background(), it, RestoreRequest{Snapshot: "e1", Mode: "here", BackupFirst: true}, nil); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range f.calls {
		switch c.Args[1] {
		case "backup":
			if !slices.Contains(c.Args, ReasonPreRestore) {
				t.Fatalf("pre-restore backup not tagged: %v", c.Args)
			}
			order = append(order, "backup")
		case "restore":
			order = append(order, "restore")
		}
	}
	if strings.Join(order, ",") != "backup,restore" {
		t.Fatalf("order %v", order)
	}
}

func TestRestoreHereVolumeStopsUsers(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addVolume(t, "photos")
	env.addComputer(t, "alice", true, computers.Settings{Image: "img", Volumes: []computers.VolumeGrant{{Name: "photos"}}})
	env.addComputer(t, "bob", false, computers.Settings{Image: "img", Volumes: []computers.VolumeGrant{{Name: "photos"}}})
	it := Item{KindVolume, "photos"}
	if _, err := s.Restore(context.Background(), it, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "here"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.events(); !strings.Contains(got, "stop alice") || !strings.Contains(got, "start alice") || strings.Contains(got, "start bob") || strings.Contains(got, "stop bob") {
		t.Fatalf("lifecycle %s", got)
	}
	args := strings.Join(restoreCalls(f)[0].Args, " ")
	if !strings.Contains(args, "--delete") || !strings.HasSuffix(args, "v1:/deskyard/volume/photos") {
		t.Fatalf("args %s", args)
	}
}

func TestRestoreAsNewComputerDropsMachineSettings(t *testing.T) {
	def := computers.Definition{Name: "alice", Image: computers.ImageRef{Ref: "ghcr.io/x/desk:1", ID: "sha256:1"}, Limits: computers.Limits{MemoryBytes: 2 << 30, NanoCPUs: 2e9},
		Network: computers.Network{Kind: computers.NetworkVPN, Connection: "vpn-1"}, DockerAccess: true,
		Volumes: []computers.VolumeGrant{{Name: "photos", Path: "/mnt/p", ReadOnly: true}, {Name: "gone", Path: "/share/gone"}}}
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(def, nil)
	env.addVolume(t, "photos")
	res, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "alice-copy"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := env.events(); got != "reserve alice-copy,create alice-copy,stop alice-copy" {
		t.Fatalf("lifecycle %s", got)
	}
	set := env.created["alice-copy"]
	if set.Image != "ghcr.io/x/desk:1" || set.MemoryBytes != 2<<30 || set.NanoCPUs != 2e9 || set.VPNConnectionID != "" || set.DockerAccess ||
		len(set.Volumes) != 1 || set.Volumes[0] != (computers.VolumeGrant{Name: "photos", Path: "/mnt/p", ReadOnly: true}) {
		t.Fatalf("settings %+v", set)
	}
	dropped := strings.Join(res.Dropped, ";")
	for _, want := range []string{"VPN", "Docker", "gone"} {
		if !strings.Contains(dropped, want) {
			t.Errorf("dropped %q lacks %s", dropped, want)
		}
	}
	if res.Created != "alice-copy" {
		t.Fatalf("result %+v", res)
	}
	rs := restoreCalls(f)
	if len(rs) != 1 || !strings.HasSuffix(strings.Join(rs[0].Args, " "), "e1:/deskyard/environment/alice/home") || slices.Contains(rs[0].Args, "--delete") {
		t.Fatalf("restore %+v", rs)
	}
	for _, m := range rs[0].Mounts {
		if m.Target == "/target" && m.Source != env.layout.HomeOf("alice-copy") {
			t.Fatalf("target %+v", m)
		}
	}
	// A chosen image replaces the saved one.
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "alice-two", NewImage: "other:2"}, nil); err != nil {
		t.Fatal(err)
	}
	if env.created["alice-two"].Image != "other:2" {
		t.Fatalf("image %+v", env.created["alice-two"])
	}
}

func TestRestoreAsNewRefusesExistingName(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addComputer(t, "bob", false, computers.Settings{Image: "img"})
	env.addVolume(t, "music")
	for _, tc := range []struct {
		it   Item
		name string
	}{{Item{KindComputer, "alice"}, "bob"}, {Item{KindVolume, "photos"}, "music"}, {Item{KindComputer, "alice"}, "api"}} {
		if _, err := s.Restore(context.Background(), tc.it, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: tc.name}, nil); err == nil {
			t.Errorf("restored %s as %s", tc.it, tc.name)
		}
	}
	// A leftover folder of the name is in the way too.
	if err := os.MkdirAll(env.layout.HomeOf("carol"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "carol"}, nil); err == nil {
		t.Error("restored over an existing folder")
	}
	if len(restoreCalls(f)) != 0 {
		t.Fatal("restored anyway")
	}
}

func TestRestoreAsNewVolume(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	res, err := s.Restore(context.Background(), Item{KindVolume, "photos"}, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "new", NewName: "photos-old"}, nil)
	if err != nil || res.Created != "photos-old" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := env.events(); got != "create volume photos-old" {
		t.Fatalf("lifecycle %s", got)
	}
}

func TestRestoreToFolderNeverDeletes(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addComputer(t, "alice", true, computers.Settings{Image: "img"})
	it := Item{KindComputer, "alice"}
	first := "Restored 2026-10-26 0300"
	if err := os.MkdirAll(filepath.Join(env.layout.HomeOf("alice"), first), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := s.Restore(context.Background(), it, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "folder", Paths: []string{"docs/a.txt", "photos"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Folder != first+" (2)" {
		t.Fatalf("folder %q", res.Folder)
	}
	if got := env.events(); got != "" {
		t.Fatalf("lifecycle %s", got)
	}
	rs := restoreCalls(f)
	args := strings.Join(rs[0].Args, " ")
	if slices.Contains(rs[0].Args, "--delete") || !strings.Contains(args, "--include /docs/a.txt --include /photos") || !strings.HasSuffix(args, "e1:/deskyard/environment/alice/home") {
		t.Fatalf("args %s", args)
	}
	for _, m := range rs[0].Mounts {
		if m.Target == "/target" && m.Source != filepath.Join(env.layout.HomeOf("alice"), first+" (2)") {
			t.Fatalf("target %+v", m)
		}
	}
	if _, err := s.Restore(context.Background(), it, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "folder"}, nil); err == nil {
		t.Fatal("restored to a folder with nothing selected")
	}
}

// noHelpers answers like restoreReply but fails any root helper command
// (install, chown) the supervisor should have no need of.
func noHelpers(def computers.Definition) func(Call) (string, Result, error) {
	inner := restoreReply(def, nil)
	return func(c Call) (string, Result, error) {
		if c.Args[0] == "install" || c.Args[0] == "chown" {
			return "", Result{Code: 1, Stderr: "no helper expected"}, nil
		}
		return inner(c)
	}
}

func TestRestoreAsNewWithoutHelper(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = noHelpers(computers.Definition{})
	if _, err := s.Restore(context.Background(), Item{KindVolume, "photos"}, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "new", NewName: "photos-old"}, nil); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(env.layout.VolumeOf("photos-old")); err != nil || !st.IsDir() {
		t.Fatalf("no folder: %v", err)
	}
}

func TestRestoreToFolderLocalWithoutHelper(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = noHelpers(computers.Definition{})
	env.addVolume(t, "photos")
	res, err := s.Restore(context.Background(), Item{KindVolume, "photos"}, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "folder", Paths: []string{"a.txt"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(env.layout.VolumeOf("photos"), res.Folder))
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Fatalf("restore folder %v %v", st, err)
	}
}

func TestRestoreToFolderHostUsesHelper(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	host := t.TempDir()
	env.addHostVolume(t, "photos", host)
	res, err := s.Restore(context.Background(), Item{KindVolume, "photos"}, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "folder", Paths: []string{"a.txt"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range f.calls {
		if c.Args[0] == "install" && slices.Contains(c.Args, "-o") && slices.Contains(c.Args, "1000") && c.Args[len(c.Args)-1] == "/item/"+res.Folder &&
			len(c.Mounts) == 1 && c.Mounts[0].Source == host {
			found = true
		}
	}
	if !found {
		t.Fatalf("no install -d -o 1000 helper call: %+v", f.calls)
	}
}

func TestCleanSnapshotPath(t *testing.T) {
	for _, bad := range []string{"../x", "/etc", "a\nb", "a/../../b", "a\x00", ".."} {
		if _, err := cleanSnapshotPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for in, want := range map[string]string{"docs/a.txt": "docs/a.txt", "": "", "./docs/": "docs", "a/./b": "a/b"} {
		if got, err := cleanSnapshotPath(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
}

func TestBrowseAndDownload(t *testing.T) {
	ls := `{"struct_type":"snapshot"}
{"name":"docs","type":"dir","path":"/deskyard/environment/alice/home/docs","struct_type":"node"}
{"name":"a.txt","type":"file","path":"/deskyard/environment/alice/home/docs/a.txt","size":5,"struct_type":"node"}
`
	f := &fakeRunner{}
	s, env := testService(t, f)
	base := restoreReply(computers.Definition{}, nil)
	f.reply = func(c Call) (string, Result, error) {
		switch c.Args[1] {
		case "ls":
			return ls, Result{}, nil
		case "dump":
			return "hello", Result{}, nil
		}
		return base(c)
	}
	env.addComputer(t, "alice", true, computers.Settings{Image: "img"})
	it := Item{KindComputer, "alice"}
	entries, err := s.Browse(context.Background(), it, "home", "e1", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.last().Args, " "); !strings.HasSuffix(got, "e1 /deskyard/environment/alice/home/docs") {
		t.Fatalf("ls %s", got)
	}
	if len(entries) != 1 || entries[0].Path != "docs/a.txt" {
		t.Fatalf("entries %+v", entries)
	}
	if _, err := s.Browse(context.Background(), it, "home", "e1", "../.."); err == nil {
		t.Fatal("browsed outside the item")
	}
	var out bytes.Buffer
	name := ""
	open := func(n string) io.Writer { name = n; return &out }
	err = s.Download(context.Background(), it, "home", "e1", "docs/a.txt", false, open)
	if err != nil || name != "a.txt" || out.String() != "hello" {
		t.Fatalf("download %q %q %v", name, out.String(), err)
	}
	err = s.Download(context.Background(), it, "home", "e1", "docs", true, open)
	if err != nil || name != "docs.zip" || !slices.Contains(f.last().Args, "zip") {
		t.Fatalf("folder download %q %v %v", name, err, f.last().Args)
	}
	_ = s.Download(context.Background(), it, "home", "e1", "", true, open)
	if name != "alice-2026-10-26.zip" {
		t.Fatalf("whole download %q", name)
	}
	name = ""
	if err := s.Download(context.Background(), it, "home", "e1", "../x", false, open); err == nil || name != "" {
		t.Fatal("downloaded outside the item")
	}
}

// Every computer using the volume is reserved before any is stopped: one
// that is busy stops nothing.
func TestRestoreHereVolumeBusyUserStopsNothing(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{}, nil)
	env.addVolume(t, "photos")
	env.addComputer(t, "alice", true, computers.Settings{Image: "img", Volumes: []computers.VolumeGrant{{Name: "photos"}}})
	env.addComputer(t, "bob", true, computers.Settings{Image: "img", Volumes: []computers.VolumeGrant{{Name: "photos"}}})
	env.reserved["bob"], env.reserved["alice"] = false, false
	// Whichever comes second is busy.
	release, _ := env.Reserve("bob", "upgrading")
	defer release()
	_, err := s.Restore(context.Background(), Item{KindVolume, "photos"}, RestoreRequest{Repository: "home", Snapshot: "v1", Mode: "here"}, nil)
	if err == nil {
		t.Fatal("restored with a busy user")
	}
	if strings.Contains(env.events(), "stop") || !env.running["alice"] || len(restoreCalls(f)) != 0 {
		t.Fatalf("lifecycle %s", env.events())
	}
}

// The new computer's name is reserved while its home is restored and it
// is created, as the create API does.
func TestRestoreAsNewReservesName(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{Image: computers.ImageRef{Ref: "img"}}, nil)
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "alice-copy"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.events(); got != "reserve alice-copy,create alice-copy,stop alice-copy" {
		t.Fatalf("lifecycle %s", got)
	}
	release, _ := env.Reserve("carol", "creating")
	defer release()
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "carol"}, nil); err == nil {
		t.Fatal("restored into a name being created")
	}
	if _, err := os.Stat(env.layout.HomeOf("carol")); !os.IsNotExist(err) {
		t.Fatal("made a folder for a busy name")
	}
}

// Grants a failed removal of the name left behind must not carry over to the
// computer restored under it: the restore forgets them under the
// reservation, and stops when it can't.
func TestRestoreAsNewForgetsTheNamesGrants(t *testing.T) {
	f := &fakeRunner{}
	s, env := testService(t, f)
	f.reply = restoreReply(computers.Definition{Image: computers.ImageRef{Ref: "img"}}, nil)
	var forgot []string
	s.ForgetName = func(name string) error {
		forgot = append(forgot, name+" after "+env.events())
		return nil
	}
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "alice-copy"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(forgot) != 1 || forgot[0] != "alice-copy after reserve alice-copy" {
		t.Fatalf("forgot %q", forgot)
	}
	s.ForgetName = func(string) error { return errors.New("disk full") }
	if _, err := s.Restore(context.Background(), Item{KindComputer, "alice"}, RestoreRequest{Repository: "home", Snapshot: "e1", Mode: "new", NewName: "alice-two"}, nil); err == nil {
		t.Fatal("restored although its grants couldn't be forgotten")
	}
	if _, ok := env.created["alice-two"]; ok || len(restoreCalls(f)) != 1 {
		t.Fatal("restored anyway")
	}
}
