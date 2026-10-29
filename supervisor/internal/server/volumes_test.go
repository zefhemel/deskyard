package server

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// volumeEngine is hostSettingsEngine whose helper containers make and
// delete folders, as the real alpine helper would.
type volumeEngine struct{ hostSettingsEngine }

func (e *volumeEngine) Create(ctx context.Context, s engine.ContainerSpec) (string, error) {
	if s.Labels[computers.LabelManaged] != "true" && len(s.Mounts) == 1 && len(s.Cmd) > 0 {
		root, target := s.Mounts[0].Source, s.Mounts[0].Target
		path := filepath.Join(root, strings.TrimPrefix(s.Cmd[len(s.Cmd)-1], target))
		switch s.Cmd[0] {
		case "install":
			_ = os.MkdirAll(path, 0o755)
		case "rm":
			_ = os.RemoveAll(path)
		}
	}
	return e.hostSettingsEngine.Create(ctx, s)
}

func newVolumeServer(t *testing.T) (*Server, *httptest.Server, func(client *http.Client, method, path, body string) (int, []byte)) {
	e := &volumeEngine{hostSettingsEngine{current: alice}}
	e.current.Labels = maps.Clone(alice.Labels)
	e.current.Image = "img"
	e.current.Env = map[string]string{computers.TokenEnv: "token"}
	s, ts := newTestServerWith(t, e)
	send := func(client *http.Client, method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return resp.StatusCode, buf.Bytes()
	}
	return s, ts, send
}

// waitJob waits for the job a 202 response names to finish, and fails unless
// it succeeded.
func waitJob(t *testing.T, s *Server, body []byte) {
	t.Helper()
	var res struct{ Job string }
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	j := s.jobs.get(res.Job)
	select {
	case <-j.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job timed out")
	}
	if st := j.get(); st.Status != "done" {
		t.Fatalf("job: %+v", st)
	}
}

type volumeUserTest struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
}

type volumeTest struct {
	Name      string           `json:"name"`
	Created   time.Time        `json:"created"`
	Kind      string           `json:"kind"`
	Folder    string           `json:"folder"`
	Computers []volumeUserTest `json:"environments"`
}

func TestVolumesAdminOnly(t *testing.T) {
	_, ts, send := newVolumeServer(t)
	anna := login(t, ts, "anna")
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/api/volumes", ""},
		{"POST", "/api/volumes", `{"name":"photos"}`},
		{"DELETE", "/api/volumes/photos", ""},
		{"GET", "/api/volumes/photos/usage", ""},
	} {
		if code, b := send(anna, r.method, r.path, r.body); code != 403 {
			t.Errorf("%s %s: %d %s", r.method, r.path, code, b)
		}
	}
}

func TestVolumesCreateListDelete(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	code, b := send(admin, "GET", "/api/volumes", "")
	if code != 200 || strings.TrimSpace(string(b)) != `{"volumes":[]}` {
		t.Fatalf("empty list: %d %s", code, b)
	}
	for _, bad := range []string{`{"name":"A"}`, `{"name":"api"}`, `{"name":"volumes"}`, `nonsense`} {
		if code, b := send(admin, "POST", "/api/volumes", bad); code != 400 {
			t.Errorf("POST %s: %d %s", bad, code, b)
		}
	}

	folder := s.Computers.Layout.VolumeOf("photos")
	code, b = send(admin, "POST", "/api/volumes", `{"name":"photos"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	var created struct {
		Volume  volumeTest `json:"volume"`
		Adopted bool       `json:"adopted"`
	}
	if err := json.Unmarshal(b, &created); err != nil {
		t.Fatal(err)
	}
	if created.Adopted || created.Volume.Name != "photos" || created.Volume.Kind != "local" ||
		created.Volume.Folder != folder || created.Volume.Created.IsZero() || created.Volume.Computers == nil {
		t.Fatalf("created: %s", b)
	}
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		t.Fatalf("folder not made: %v", err)
	}
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 409 {
		t.Fatalf("duplicate: %d %s", code, b)
	}

	// A folder already there is adopted.
	if err := os.MkdirAll(s.Computers.Layout.VolumeOf("music"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, b = send(admin, "POST", "/api/volumes", `{"name":"music"}`)
	if code != 201 || !strings.Contains(string(b), `"adopted":true`) {
		t.Fatalf("adopt: %d %s", code, b)
	}

	code, b = send(admin, "GET", "/api/volumes", "")
	var list struct{ Volumes []volumeTest }
	if err := json.Unmarshal(b, &list); code != 200 || err != nil {
		t.Fatalf("list: %d %s", code, b)
	}
	if len(list.Volumes) != 2 || list.Volumes[0].Name != "music" || list.Volumes[1].Name != "photos" ||
		!reflect.DeepEqual(list.Volumes[1], created.Volume) {
		t.Fatalf("list: %s", b)
	}

	if code, b := send(admin, "DELETE", "/api/volumes/photos?deleteFiles=maybe", ""); code != 400 {
		t.Fatalf("bad deleteFiles: %d %s", code, b)
	}
	if code, b := send(admin, "DELETE", "/api/volumes/photos", ""); code != 204 {
		t.Fatalf("delete: %d %s", code, b)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatal("files not kept by default")
	}
	if code, _ := send(admin, "DELETE", "/api/volumes/photos", ""); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
	if code, b := send(admin, "DELETE", "/api/volumes/music?deleteFiles=true", ""); code != 204 {
		t.Fatalf("delete with files: %d %s", code, b)
	}
	if _, err := os.Stat(s.Computers.Layout.VolumeOf("music")); !os.IsNotExist(err) {
		t.Fatalf("files not deleted: %v", err)
	}
}

func TestDeleteGrantedVolume409(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"photos","readOnly":true}]}`)
	if code != 202 {
		t.Fatalf("PATCH: %d %s", code, b)
	}
	waitJob(t, s, b)

	code, b = send(admin, "GET", "/api/volumes", "")
	if code != 200 || !strings.Contains(string(b), `"environments":[{"name":"alice","readOnly":true}]`) {
		t.Fatalf("list: %d %s", code, b)
	}
	code, b = send(admin, "DELETE", "/api/volumes/photos?deleteFiles=true", "")
	if code != 409 {
		t.Fatalf("delete granted: %d %s", code, b)
	}
	var res struct {
		Error     string
		Computers []struct{ ID, Name string } `json:"environments"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	if res.Error != "photos is used by alice" || len(res.Computers) != 1 ||
		res.Computers[0].ID != "alice" || res.Computers[0].Name != "alice" {
		t.Fatalf("409 body: %s", b)
	}
	if _, ok := s.Computers.Vols.Get("photos"); !ok {
		t.Fatal("volume deleted")
	}
	if _, err := os.Stat(s.Computers.Layout.VolumeOf("photos")); err != nil {
		t.Fatal("folder deleted")
	}
}

// A container still mounting the volume after its definition dropped the
// grant, as when a replacement failed, is a 409 and keeps the files.
func TestDeleteMountedVolume409(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"photos"}]}`)
	if code != 202 {
		t.Fatalf("PATCH: %d %s", code, b)
	}
	waitJob(t, s, b)
	// The fake engine doesn't record mounts; give alice's container the grant's.
	e := s.Computers.Eng.(*volumeEngine)
	e.mu.Lock()
	e.current.Mounts = []engine.Mount{{Source: s.Computers.Layout.VolumeOf("photos"), Target: "/share/photos"}}
	e.mu.Unlock()
	if _, err := s.Computers.Defs.Update("alice", func(d *computers.Definition) error {
		d.Volumes = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, b = send(admin, "DELETE", "/api/volumes/photos?deleteFiles=true", "")
	if code != 409 || !strings.Contains(string(b), "still mounted by the container of alice") {
		t.Fatalf("delete mounted: %d %s", code, b)
	}
	if _, ok := s.Computers.Vols.Get("photos"); !ok {
		t.Fatal("volume deleted")
	}
	if _, err := os.Stat(s.Computers.Layout.VolumeOf("photos")); err != nil {
		t.Fatal("folder deleted")
	}
}

func TestPatchVolumesUnknown400(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("alice", fullAccess("anna")); err != nil {
		t.Fatal(err)
	}
	code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"ghost"}]}`)
	if code != 400 || !strings.Contains(string(b), "no volume ghost") {
		t.Fatalf("unknown: %d %s", code, b)
	}
	if code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"A"}]}`); code != 400 {
		t.Fatalf("invalid: %d %s", code, b)
	}
	code, b = send(admin, "POST", "/api/environments", `{"name":"new-desktop","volumes":[{"name":"ghost"}]}`)
	if code != 400 || !strings.Contains(string(b), "no volume ghost") {
		t.Fatalf("create unknown: %d %s", code, b)
	}
	if code, _ := send(anna, "PATCH", "/api/environments/alice", `{"volumes":[]}`); code != 403 {
		t.Fatalf("non-admin: %d", code)
	}
}

func TestComputerJSONHasVolumes(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	code, b := send(admin, "GET", "/api/environments/alice", "")
	if code != 200 || !strings.Contains(string(b), `"volumes":[]`) {
		t.Fatalf("no volumes: %d %s", code, b)
	}
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	code, b = send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"photos"}]}`)
	if code != 202 {
		t.Fatalf("PATCH: %d %s", code, b)
	}
	waitJob(t, s, b)
	code, b = send(admin, "GET", "/api/environments/alice", "")
	if code != 200 || !strings.Contains(string(b), `"volumes":[{"name":"photos","path":"/share/photos","readOnly":false}]`) {
		t.Fatalf("grant not exposed: %d %s", code, b)
	}
	// Omitting volumes keeps them.
	code, b = send(admin, "PATCH", "/api/environments/alice", `{"nanoCpus":1000000000}`)
	if code != 202 {
		t.Fatalf("PATCH: %d %s", code, b)
	}
	waitJob(t, s, b)
	c, _ := s.Computers.Get(context.Background(), "alice")
	if want := []computers.VolumeGrant{{Name: "photos", Path: "/share/photos"}}; !reflect.DeepEqual(c.Settings.Volumes, want) {
		t.Fatalf("omitting volumes lost them: %+v", c.Settings.Volumes)
	}
}

// TestPatchKeepsDanglingGrant checks that the server leaves a grant of a
// volume since deleted to the service, as Definitions does: a change that
// keeps it is accepted, and its job reports the missing volume, while
// re-adding it after dropping it is a 400.
func TestPatchKeepsDanglingGrant(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"photos"}]}`)
	if code != 202 {
		t.Fatalf("grant: %d %s", code, b)
	}
	waitJob(t, s, b)
	// As though volumes.json were edited by hand.
	if err := s.Computers.Vols.Delete("photos"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"memoryBytes":1073741824}`, `{"volumes":[{"name":"photos","readOnly":true}]}`} {
		code, b := send(admin, "PATCH", "/api/environments/alice", body)
		if code != 202 {
			t.Fatalf("%s: %d %s", body, code, b)
		}
		var res struct{ Job string }
		if err := json.Unmarshal(b, &res); err != nil {
			t.Fatal(err)
		}
		j := s.jobs.get(res.Job)
		select {
		case <-j.done:
		case <-time.After(5 * time.Second):
			t.Fatal("job timed out")
		}
		// The container can't be replaced without the volume's folder.
		if st := j.get(); st.Status != "failed" || !strings.Contains(st.Error, "uses volume photos, which doesn't exist") {
			t.Fatalf("%s job: %+v", body, st)
		}
	}
	if code, b := send(admin, "PATCH", "/api/environments/alice", `{"volumes":[]}`); code != 202 {
		t.Fatalf("drop: %d %s", code, b)
	} else {
		waitJob(t, s, b)
	}
	code, b = send(admin, "PATCH", "/api/environments/alice", `{"volumes":[{"name":"photos"}]}`)
	if code != 400 || !strings.Contains(string(b), "no volume photos") {
		t.Fatalf("re-add: %d %s", code, b)
	}
}

func TestCreateVolumeFileInTheWay409(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	folder := s.Computers.Layout.VolumeOf("photos")
	if err := os.MkdirAll(filepath.Dir(folder), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(folder, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`)
	if code != 409 || !strings.Contains(string(b), "isn't a folder") {
		t.Fatalf("file in the way: %d %s", code, b)
	}
}

func TestVolumeUsage(t *testing.T) {
	s, ts, send := newVolumeServer(t)
	admin := login(t, ts, "admin")
	if code, b := send(admin, "GET", "/api/volumes/photos/usage", ""); code != 404 {
		t.Fatalf("unknown volume: %d %s", code, b)
	}
	if code, b := send(admin, "POST", "/api/volumes", `{"name":"photos"}`); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	folder := s.Computers.Layout.VolumeOf("photos")
	if err := os.MkdirAll(filepath.Join(folder, "trip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "trip", "a.jpg"), make([]byte, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	code, b := send(admin, "GET", "/api/volumes/photos/usage", "")
	var usage struct {
		Bytes    int64  `json:"bytes"`
		Complete bool   `json:"complete"`
		DiskFree uint64 `json:"diskFree"`
		DiskSize uint64 `json:"diskSize"`
	}
	if err := json.Unmarshal(b, &usage); code != 200 || err != nil {
		t.Fatalf("usage: %d %s", code, b)
	}
	if usage.Bytes != 3000 || !usage.Complete || usage.DiskSize == 0 || usage.DiskFree > usage.DiskSize {
		t.Fatalf("usage: %s", b)
	}
}

// TestFoldersShownAsHostPaths: the folders the API names are where the files
// are on the engine host, not the supervisor's own /data paths.
func TestFoldersShownAsHostPaths(t *testing.T) {
	e := &volumeEngine{hostSettingsEngine{current: alice}}
	e.current.Labels = maps.Clone(alice.Labels)
	e.current.Image = "img"
	e.current.Env = map[string]string{computers.TokenEnv: "token"}
	s, ts := newTestServerCfg(t, e, computers.Config{Subnet: "10.0.0.0/16", DataHost: "/srv/deskyard"})
	admin := login(t, ts, "admin")
	get := func(path string, v any) {
		t.Helper()
		resp, err := admin.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Computers.CreateVolume(context.Background(), "photos", ""); err != nil {
		t.Fatal(err)
	}
	var vols struct{ Volumes []volumeTest }
	get("/api/volumes", &vols)
	if len(vols.Volumes) != 1 || vols.Volumes[0].Folder != "/srv/deskyard/volume/photos" {
		t.Errorf("volume folder: %+v", vols.Volumes)
	}
	var env struct{ Folder string }
	get("/api/environments/alice", &env)
	if env.Folder != "/srv/deskyard/home/alice" {
		t.Errorf("computer folder: %q", env.Folder)
	}
	var folder struct{ Path string }
	get("/api/environments/alice/folder", &folder)
	if folder.Path != "/srv/deskyard/home/alice" {
		t.Errorf("folder path: %q", folder.Path)
	}
	var check struct{ Folder string }
	get("/api/validate-name?name=bob", &check)
	if check.Folder != "/srv/deskyard/home/bob" {
		t.Errorf("validate-name folder: %q", check.Folder)
	}
}
