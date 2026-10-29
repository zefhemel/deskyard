package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/storage"
)

// storageRunner plays rclone and restic: every command works, and a
// repository's config is missing (so creating one initialises it).
type storageRunner struct{ calls []storage.Call }

func (r *storageRunner) Run(_ context.Context, c storage.Call) (storage.Result, error) {
	r.calls = append(r.calls, c)
	out := ""
	res := storage.Result{}
	if len(c.Args) > 1 {
		switch c.Args[1] {
		case "cat":
			res.Code = 10
		case "backup":
			out = `{"message_type":"summary","snapshot_id":"snap1"}` + "\n"
		case "snapshots":
			out = `[{"time":"2026-10-27T03:00:00Z","hostname":"` + c.Env["HOST"] + `","tags":["deskyard","volume:photos","manual"],"id":"snap1","short_id":"snap1"}]`
		}
	}
	if c.Stdout != nil {
		_, _ = io.WriteString(c.Stdout, out)
	}
	return res, nil
}

const remoteBody = `{"remote":{"name":"nas","type":"sftp","host":"nas.local","user":"deskyard","root":"/backups"},"secrets":{"password":"sftp-secret"}}`

func newStorageServer(t *testing.T) (*Server, func(client *http.Client, method, path, body string) (int, []byte), *http.Client) {
	s, ts, send := newVolumeServer(t)
	s.storage.Tool.Run = &storageRunner{}
	return s, send, login(t, ts, "admin")
}

func TestRemotesAdminOnly(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	s.storage.Tool.Run = &storageRunner{}
	anna := login(t, ts, "anna")
	for _, r := range [][2]string{{"GET", "/api/remotes"}, {"POST", "/api/remotes"}, {"GET", "/api/backups"}, {"POST", "/api/backups/repositories"}, {"GET", "/api/backups/volume/photos"}, {"POST", "/api/backups/volume/photos/run"}} {
		if code, b := send(anna, r[0], r[1], "{}"); code != 403 {
			t.Errorf("%s %s: %d %s", r[0], r[1], code, b)
		}
	}
}

func TestRemoteResponsesHaveNoSecrets(t *testing.T) {
	_, send, admin := newStorageServer(t)
	if code, b := send(admin, "POST", "/api/remotes/test", remoteBody); code != 204 {
		t.Fatalf("test: %d %s", code, b)
	}
	code, b := send(admin, "POST", "/api/remotes", remoteBody)
	if code != 201 || strings.Contains(string(b), "sftp-secret") {
		t.Fatalf("create: %d %s", code, b)
	}
	for _, path := range []string{"/api/remotes", "/api/remotes/nas"} {
		code, b := send(admin, "GET", path, "")
		if code != 200 || strings.Contains(string(b), "sftp-secret") || !strings.Contains(string(b), `"secretsSet":["password"]`) {
			t.Fatalf("GET %s: %d %s", path, code, b)
		}
	}
	if code, b := send(admin, "POST", "/api/remotes", remoteBody); code != 409 {
		t.Fatalf("duplicate: %d %s", code, b)
	}
	if code, b := send(admin, "PUT", "/api/remotes/nas", `{"remote":{"name":"nas","type":"sftp","host":"nas2.local","user":"deskyard","root":"/backups"},"secrets":{}}`); code != 200 || !strings.Contains(string(b), "nas2.local") {
		t.Fatalf("update: %d %s", code, b)
	}
	if code, b := send(admin, "DELETE", "/api/remotes/nas", ""); code != 204 {
		t.Fatalf("delete: %d %s", code, b)
	}
}

func TestCreateRepositoryReturnsKeyOnce(t *testing.T) {
	_, send, admin := newStorageServer(t)
	if code, b := send(admin, "POST", "/api/remotes", remoteBody); code != 201 {
		t.Fatalf("remote: %d %s", code, b)
	}
	code, b := send(admin, "POST", "/api/backups/repositories", `{"name":"home","remote":"nas"}`)
	var created struct {
		Repository  storage.Repository `json:"repository"`
		RecoveryKey string             `json:"recoveryKey"`
	}
	if err := json.Unmarshal(b, &created); code != 201 || err != nil || created.RecoveryKey == "" || created.Repository.Folder != storage.DefaultFolder {
		t.Fatalf("create: %d %s", code, b)
	}
	code, b = send(admin, "GET", "/api/backups", "")
	if code != 200 || strings.Contains(string(b), created.RecoveryKey) || !strings.Contains(string(b), `"name":"home"`) {
		t.Fatalf("overview: %d %s", code, b)
	}
	code, b = send(admin, "GET", "/api/backups/repositories/home/recovery-key", "")
	if code != 200 || !strings.Contains(string(b), created.RecoveryKey) {
		t.Fatalf("reveal: %d %s", code, b)
	}
	if code, b := send(admin, "DELETE", "/api/remotes/nas", ""); code != 409 {
		t.Fatalf("deleted a remote with a repository: %d %s", code, b)
	}
}

func setUpBackups(t *testing.T, s *Server, send func(*http.Client, string, string, string) (int, []byte), admin *http.Client) {
	t.Helper()
	if code, b := send(admin, "POST", "/api/remotes", remoteBody); code != 201 {
		t.Fatalf("remote: %d %s", code, b)
	}
	if code, b := send(admin, "POST", "/api/backups/repositories", `{"name":"home","remote":"nas"}`); code != 201 {
		t.Fatalf("repository: %d %s", code, b)
	}
	if err := s.Computers.Vols.Add(computers.Volume{Name: "photos", Created: time.Now().UTC(), Kind: computers.VolumeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Computers.Layout.VolumeOf("photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	policy := `{"repository":"home","enabled":true,"schedule":{"every":"daily","at":"03:00"},"retention":{"daily":7,"weekly":4,"monthly":12}}`
	if code, b := send(admin, "PUT", "/api/backups/volume/photos", policy); code != 200 {
		t.Fatalf("policy: %d %s", code, b)
	}
}

func TestBackupRunIsJob(t *testing.T) {
	s, send, admin := newStorageServer(t)
	setUpBackups(t, s, send, admin)
	code, b := send(admin, "POST", "/api/backups/volume/photos/run", "")
	if code != 202 {
		t.Fatalf("run: %d %s", code, b)
	}
	waitJob(t, s, b)
	code, b = send(admin, "GET", "/api/backups/volume/photos", "")
	if code != 200 || !strings.Contains(string(b), `"snapshot":"snap1"`) || !strings.Contains(string(b), `"nextRun"`) {
		t.Fatalf("state: %d %s", code, b)
	}
	if code, b := send(admin, "PUT", "/api/backups/volume/nothing", `{"repository":"home","enabled":true,"schedule":{"every":"daily","at":"03:00"},"retention":{"daily":7}}`); code != 404 {
		t.Fatalf("policy of a missing volume: %d %s", code, b)
	}
	if code, b := send(admin, "PUT", "/api/backups/home/x", `{}`); code != 400 {
		t.Fatalf("bad kind: %d %s", code, b)
	}
}

func TestRestorePathRejected(t *testing.T) {
	s, send, admin := newStorageServer(t)
	setUpBackups(t, s, send, admin)
	for _, path := range []string{
		"/api/backups/volume/photos/snapshots/snap1/files?path=../x",
		"/api/backups/volume/photos/snapshots/snap1/download?path=%2Fetc%2Fpasswd",
	} {
		if code, b := send(admin, "GET", path, ""); code != 400 {
			t.Errorf("%s: %d %s", path, code, b)
		}
	}
	if code, b := send(admin, "POST", "/api/backups/volume/photos/restore", `{"snapshot":"snap1","mode":"folder","paths":["../../etc"]}`); code != 400 {
		t.Errorf("restore path: %d %s", code, b)
	}
	if code, b := send(admin, "POST", "/api/backups/volume/photos/restore", `{"snapshot":"snap1","mode":"sideways"}`); code != 400 {
		t.Errorf("restore mode: %d %s", code, b)
	}
}

func TestComputerRemoveDeletesPolicy(t *testing.T) {
	s, send, admin := newStorageServer(t)
	setUpBackups(t, s, send, admin)
	if err := s.storage.Store.PutPolicy(storage.Policy{Item: storage.Item{Kind: storage.KindComputer, Name: "alice"}, Repository: "home", Enabled: true, Schedule: storage.DefaultSchedule, Retention: storage.DefaultRetention}); err != nil {
		t.Fatal(err)
	}
	_ = s.computerRemoved("alice")
	if _, ok := s.storage.Store.Policy(storage.Item{Kind: storage.KindComputer, Name: "alice"}); ok {
		t.Fatal("policy of a removed computer kept")
	}
	if code, b := send(admin, "DELETE", "/api/volumes/photos", ""); code != 204 {
		t.Fatalf("delete volume: %d %s", code, b)
	}
	if _, ok := s.storage.Store.Policy(storage.Item{Kind: "volume", Name: "photos"}); ok {
		t.Fatal("policy of a deleted volume kept")
	}
}

func TestRemoveRefusedDuringBackup(t *testing.T) {
	s, send, admin := newStorageServer(t)
	release, err := s.storage.Hold(storage.Item{Kind: storage.KindComputer, Name: "alice"}, "backing up")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if code, b := send(admin, "DELETE", "/api/environments/alice", ""); code != 409 || !strings.Contains(string(b), "backing up") {
		t.Fatalf("remove during backup: %d %s", code, b)
	}
}

// A restore as a new computer forgets the grants a failed removal of its
// name left behind, as creating one does.
func TestRestoreAsNewForgetsLeftoverGrants(t *testing.T) {
	s, _, _ := newStorageServer(t)
	if err := s.Accounts.SetAccess("carol", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	if s.storage.ForgetName == nil {
		t.Fatal("the restore doesn't forget the name's grants")
	}
	if err := s.storage.ForgetName("carol"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Accounts.Get("anna"); a.Level("carol") != auth.LevelNone {
		t.Errorf("anna kept her grant of the removed carol: %v", a.Grants)
	}
}
