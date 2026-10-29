package computers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

func testDefinition(name string) Definition {
	return Definition{
		Name:          name,
		Created:       time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC),
		Revision:      4,
		Image:         ImageRef{Ref: "ghcr.io/zefhemel/deskyard-xfce:latest", ID: "sha256:abc"},
		Limits:        Limits{MemoryBytes: 1 << 30, NanoCPUs: 2e9},
		Network:       Network{Kind: NetworkIsolated},
		Volumes:       []VolumeGrant{{Name: "photos", Path: "/mnt/photos", ReadOnly: true}},
		MemoryReclaim: true,
		Power:         PowerRunning,
	}
}

func TestDefinitionsRoundTripSpecKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := defs.List(); len(got) != 0 {
		t.Fatalf("a missing file loads empty, got %v", got)
	}
	bob := testDefinition("bob")
	bob.Network = Network{Kind: NetworkVPN, Connection: "conn-1", LANExceptions: []vpn.Exception{{Destination: "192.168.1.0/24", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 22, End: 22}}}}}
	bob.DockerAccess = true
	bob.Power = PowerStopped
	alice := testDefinition("alice")
	if err := defs.Put(bob); err != nil {
		t.Fatal(err)
	}
	if err := defs.Put(alice); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"] != float64(1) {
		t.Fatalf("version = %v", raw["version"])
	}
	envs := raw["computers"].([]any)
	if len(envs) != 2 {
		t.Fatalf("computers = %v", envs)
	}
	first := envs[0].(map[string]any)
	if first["name"] != "alice" {
		t.Fatalf("computers are saved sorted by name, got %v first", first["name"])
	}
	for _, key := range []string{"name", "created", "revision", "image", "limits", "network", "volumes", "dockerAccess", "memoryReclaim", "webdav", "power"} {
		if _, ok := first[key]; !ok {
			t.Errorf("missing key %q in %s", key, b)
		}
	}
	if len(first) != 11 {
		t.Errorf("unexpected keys in %v", first)
	}
	if img := first["image"].(map[string]any); img["ref"] != alice.Image.Ref || img["id"] != "sha256:abc" {
		t.Errorf("image = %v", img)
	}
	if l := first["limits"].(map[string]any); l["memoryBytes"] != float64(1<<30) || l["nanoCPUs"] != float64(2e9) {
		t.Errorf("limits = %v", l)
	}
	if n := first["network"].(map[string]any); len(n) != 1 || n["kind"] != "isolated" {
		t.Errorf("isolated network = %v", n)
	}
	if m := first["volumes"].([]any)[0].(map[string]any); m["name"] != "photos" || m["path"] != "/mnt/photos" || m["readOnly"] != true {
		t.Errorf("volumes = %v", m)
	}
	if first["created"] != "2026-10-26T10:00:00Z" || first["power"] != "running" {
		t.Errorf("entry = %v", first)
	}
	if n := envs[1].(map[string]any)["network"].(map[string]any); n["kind"] != "vpn" || n["connection"] != "conn-1" || n["lanExceptions"] == nil {
		t.Errorf("vpn network = %v", n)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", fi.Mode().Perm(), err)
	}

	again, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); !reflect.DeepEqual(got, []Definition{alice, bob}) {
		t.Fatalf("reloaded = %+v", got)
	}
	got, ok := again.Get("bob")
	if !ok || !reflect.DeepEqual(got, bob) {
		t.Fatalf("Get(bob) = %+v, %v", got, ok)
	}
}

func TestDefinitionsSpecExampleLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	example := `{
  "version": 1,
  "computers": [
    {
      "name": "alice",
      "created": "2026-10-26T10:00:00Z",
      "revision": 4,
      "image": {"ref": "ghcr.io/x/deskyard-xfce:latest", "id": "sha256:1"},
      "limits": {"memoryBytes": 0, "nanoCPUs": 0},
      "network": {"kind": "isolated"},
      "volumes": [{"name": "photos", "path": "/mnt/photos", "readOnly": true}],
      "dockerAccess": false,
      "memoryReclaim": true,
      "power": "running"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := defs.Get("alice")
	if !ok || d.Revision != 4 || !d.MemoryReclaim || d.Power != PowerRunning || len(d.Volumes) != 1 || !d.Volumes[0].ReadOnly || d.Volumes[0].Path != "/mnt/photos" {
		t.Fatalf("alice = %+v, %v", d, ok)
	}
}

// Files written before camera support was removed carry a "camera" field:
// it loads, and the next save leaves it out. Other unknown fields still fail.
func TestDefinitionsLoadDropsLegacyCamera(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	legacy := `{"version": 1, "computers": [
    {"name": "alice", "created": "2026-10-26T10:00:00Z", "revision": 4,
     "image": {"ref": "ghcr.io/x/deskyard-xfce:latest", "id": "sha256:1"},
     "limits": {"memoryBytes": 0, "nanoCPUs": 0}, "network": {"kind": "isolated"},
     "mounts": [], "volumes": [], "dockerAccess": false, "camera": "/dev/video10",
     "memoryReclaim": false, "power": "running"},
    {"name": "bob", "created": "2026-10-26T10:00:00Z", "revision": 1,
     "image": {"ref": "ghcr.io/x/deskyard-xfce:latest", "id": "sha256:1"},
     "limits": {"memoryBytes": 0, "nanoCPUs": 0}, "network": {"kind": "isolated"},
     "mounts": [], "volumes": [], "dockerAccess": false, "camera": "",
     "memoryReclaim": false, "power": "stopped"}
  ]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := defs.Get("alice"); !ok || a.Revision != 4 || a.Power != PowerRunning {
		t.Fatalf("alice = %+v, %v", a, ok)
	}
	if _, err := defs.Update("bob", func(d *Definition) error { d.MemoryReclaim = true; return nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "camera") {
		t.Fatalf("the camera field was written back: %s", b)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(legacy, `"camera"`, `"webcam"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefinitions(path); err == nil || !strings.Contains(err.Error(), "webcam") {
		t.Fatalf("an unknown field loaded: %v", err)
	}
}

// Host folders were replaced by host volumes without a migration: a
// definition that still has them stops loading, naming the computer.
func TestLoadDefinitionsRejectsHostFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	def := `{"version": 1, "computers": [
    {"name": "alice", "created": "2026-10-26T10:00:00Z", "revision": 1,
     "image": {"ref": "img", "id": "sha256:1"},
     "limits": {"memoryBytes": 0, "nanoCPUs": 0}, "network": {"kind": "isolated"},
     "mounts": MOUNTS, "volumes": [], "dockerAccess": false,
     "memoryReclaim": false, "power": "running"}]}`
	write := func(mounts string) {
		if err := os.WriteFile(path, []byte(strings.Replace(def, "MOUNTS", mounts, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"source": "/srv/photos", "target": "/mnt/photos", "readOnly": true}]`)
	if _, err := LoadDefinitions(path); err == nil || !strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "host volume") {
		t.Fatalf("loaded host folders: %v", err)
	}
	for _, empty := range []string{`[]`, `null`} {
		write(empty)
		if _, err := LoadDefinitions(path); err != nil {
			t.Fatalf("%s: %v", empty, err)
		}
	}
}

// A grant saved without a path is mounted at /share/<volume>, and gets that
// path when loaded.
func TestLoadDefinitionsDefaultsGrantPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	body := `{"version": 1, "computers": [
    {"name": "alice", "created": "2026-10-26T10:00:00Z", "revision": 1,
     "image": {"ref": "img", "id": "sha256:1"},
     "limits": {"memoryBytes": 0, "nanoCPUs": 0}, "network": {"kind": "isolated"},
     "volumes": [{"name": "photos", "readOnly": false}], "dockerAccess": false,
     "memoryReclaim": false, "power": "running"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := defs.Get("alice"); len(d.Volumes) != 1 || d.Volumes[0].Path != "/share/photos" {
		t.Fatalf("%+v", d.Volumes)
	}
}

func TestDefinitionsLoadRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	for _, body := range []string{`{"version": 2, "computers": []}`, `{"computers": []}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefinitions(path); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}

func TestDefinitionsLoadExplainsNewlyReservedName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	b, _ := json.Marshal(definitionsFile{Version: 1, Computers: []Definition{testDefinition("alice"), testDefinition("volumes")}})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDefinitions(path)
	if err == nil || !strings.Contains(err.Error(), `"volumes"`) || !strings.Contains(err.Error(), "before upgrading") {
		t.Fatalf("err = %v", err)
	}
}

func TestDefinitionsLoadRejectsMalformedEntries(t *testing.T) {
	valid := testDefinition("alice")
	cases := map[string]func(d *Definition){
		"bad name":           func(d *Definition) { d.Name = "A" },
		"reserved name":      func(d *Definition) { d.Name = "api" },
		"no created":         func(d *Definition) { d.Created = time.Time{} },
		"zero revision":      func(d *Definition) { d.Revision = 0 },
		"no image":           func(d *Definition) { d.Image.Ref = "" },
		"negative memory":    func(d *Definition) { d.Limits.MemoryBytes = -1 },
		"negative cpus":      func(d *Definition) { d.Limits.NanoCPUs = -1 },
		"unknown network":    func(d *Definition) { d.Network.Kind = "bridge" },
		"vpn without conn":   func(d *Definition) { d.Network = Network{Kind: NetworkVPN} },
		"isolated with conn": func(d *Definition) { d.Network.Connection = "conn-1" },
		"public exception": func(d *Definition) {
			d.Network = Network{Kind: NetworkVPN, Connection: "c", LANExceptions: []vpn.Exception{{Destination: "8.8.8.8", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 53}}}}}
		},
		"bad grant path": func(d *Definition) { d.Volumes = []VolumeGrant{{Name: "photos", Path: "relative"}} },
		"unknown power":  func(d *Definition) { d.Power = "paused" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := valid
			d.Volumes = append([]VolumeGrant(nil), valid.Volumes...)
			mutate(&d)
			path := filepath.Join(t.TempDir(), "computers.json")
			b, _ := json.Marshal(definitionsFile{Version: 1, Computers: []Definition{d}})
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadDefinitions(path); err == nil {
				t.Fatal("malformed entry loaded")
			}
			defs, err := LoadDefinitions(filepath.Join(t.TempDir(), "computers.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := defs.Put(d); err == nil {
				t.Fatal("Put accepted a malformed definition")
			}
		})
	}

	path := filepath.Join(t.TempDir(), "computers.json")
	for _, body := range []string{
		`not json`,
		`{"version": 1, "computers": [{"name": "alice", "unknown": 1}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefinitions(path); err == nil {
			t.Errorf("%s loaded", body)
		}
	}
	b, _ := json.Marshal(definitionsFile{Version: 1, Computers: []Definition{valid, valid}})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefinitions(path); err == nil {
		t.Error("duplicate names loaded")
	}
}

func TestDefinitionsPutFailureKeepsMemory(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadDefinitions(filepath.Join(dir, "computers.json"))
	if err != nil {
		t.Fatal(err)
	}
	good := defs.path
	defs.path = filepath.Join(parent, "computers.json")
	if err := defs.Put(testDefinition("alice")); err == nil {
		t.Fatal("Put succeeded under a file")
	}
	if _, ok := defs.Get("alice"); ok {
		t.Fatal("failed Put changed memory")
	}

	defs.path = good
	alice := testDefinition("alice")
	if err := defs.Put(alice); err != nil {
		t.Fatal(err)
	}
	defs.path = filepath.Join(parent, "computers.json")
	changed := alice
	changed.Revision++
	if err := defs.Put(changed); err == nil {
		t.Fatal("Put succeeded under a file")
	}
	if err := defs.Delete("alice"); err == nil {
		t.Fatal("Delete succeeded under a file")
	}
	if got, _ := defs.Get("alice"); got.Revision != alice.Revision {
		t.Fatalf("failed writes changed memory: %+v", got)
	}
}

// Every save syncs the directory, so the rename outlives a power loss.
func TestDefinitionsSaveSyncsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	sync := syncDefinitionsDir
	t.Cleanup(func() { syncDefinitionsDir = sync })
	var synced []string
	syncDefinitionsDir = func(dir string) error {
		synced = append(synced, dir)
		return sync(dir)
	}
	if err := defs.Put(testDefinition("alice")); err != nil {
		t.Fatal(err)
	}
	if err := defs.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 2 || synced[0] != filepath.Dir(path) || synced[1] != filepath.Dir(path) {
		t.Fatalf("synced %v", synced)
	}
}

func TestDefinitionsCopiesAreIndependent(t *testing.T) {
	defs, err := LoadDefinitions(filepath.Join(t.TempDir(), "computers.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := testDefinition("alice")
	if err := defs.Put(d); err != nil {
		t.Fatal(err)
	}
	d.Volumes[0].Path = "/mnt/changed"
	got, _ := defs.Get("alice")
	got.Volumes[0].Name = "changed"
	if again, _ := defs.Get("alice"); again.Volumes[0].Path != "/mnt/photos" || again.Volumes[0].Name != "photos" {
		t.Fatalf("stored definition shares memory with callers: %+v", again.Volumes)
	}
}

func TestDefinitionsDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "computers.json")
	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := defs.Put(testDefinition("alice")); err != nil {
		t.Fatal(err)
	}
	if err := defs.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if err := defs.Delete("alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete = %v", err)
	}
	again, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get("alice"); ok {
		t.Fatal("deleted definition reloaded")
	}
}

func TestDefinitionContainerAffecting(t *testing.T) {
	base := testDefinition("alice")
	affecting := map[string]func(d *Definition){
		"image ref":    func(d *Definition) { d.Image.Ref = "other:latest" },
		"image id":     func(d *Definition) { d.Image.ID = "sha256:def" },
		"limits":       func(d *Definition) { d.Limits.MemoryBytes++ },
		"network kind": func(d *Definition) { d.Network = Network{Kind: NetworkVPN, Connection: "c"} },
		"lan exceptions": func(d *Definition) {
			d.Network = Network{Kind: NetworkVPN, Connection: "c", LANExceptions: []vpn.Exception{{Destination: "10.0.0.1/32", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 22, End: 22}}}}}
		},
		"grants":        func(d *Definition) { d.Volumes = nil },
		"grant mode":    func(d *Definition) { d.Volumes = []VolumeGrant{{Name: "photos", Path: "/mnt/photos"}} },
		"grant path":    func(d *Definition) { d.Volumes = []VolumeGrant{{Name: "photos", Path: "/mnt/other", ReadOnly: true}} },
		"docker access": func(d *Definition) { d.DockerAccess = true },
	}
	for name, mutate := range affecting {
		d := base
		d.Volumes = append([]VolumeGrant(nil), base.Volumes...)
		mutate(&d)
		if !ContainerAffecting(base, d) || !ContainerAffecting(d, base) {
			t.Errorf("%s change is not container-affecting", name)
		}
	}
	for name, mutate := range map[string]func(d *Definition){
		"memoryReclaim": func(d *Definition) { d.MemoryReclaim = !d.MemoryReclaim },
		"power":         func(d *Definition) { d.Power = PowerStopped },
		"revision":      func(d *Definition) { d.Revision++ },
		"empty grants":  func(d *Definition) { d.Volumes = nil },
		"default path":  func(d *Definition) { d.Volumes = []VolumeGrant{{Name: "docs"}} },
	} {
		a, b := base, base
		switch name {
		case "empty grants":
			a.Volumes = []VolumeGrant{}
		case "default path":
			a.Volumes = []VolumeGrant{{Name: "docs", Path: "/share/docs"}}
		}
		mutate(&b)
		if ContainerAffecting(a, b) {
			t.Errorf("%s change is container-affecting", name)
		}
	}
	vpnA := base
	vpnA.Network = Network{Kind: NetworkVPN, Connection: "c", LANExceptions: []vpn.Exception{}}
	vpnB := vpnA
	vpnB.Network.LANExceptions = nil
	if ContainerAffecting(vpnA, vpnB) {
		t.Error("nil and empty LAN exceptions differ")
	}
}

func TestDefinitionSettings(t *testing.T) {
	d := testDefinition("alice")
	d.Network = Network{Kind: NetworkVPN, Connection: "conn-1", LANExceptions: []vpn.Exception{{Destination: "10.0.0.1/32", Protocol: "tcp", Ports: []vpn.PortRange{{Start: 22, End: 22}}}}}
	d.DockerAccess = true
	s := d.Settings()
	want := Settings{Image: d.Image.Ref, MemoryBytes: 1 << 30, NanoCPUs: 2e9, VPNConnectionID: "conn-1", LANExceptions: d.Network.LANExceptions, Volumes: d.Volumes, DockerAccess: true}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("Settings() = %+v", s)
	}
	s.Volumes[0].Name = "changed"
	s.LANExceptions[0].Ports[0].Start = 1
	if d.Volumes[0].Name != "photos" || d.Network.LANExceptions[0].Ports[0].Start != 22 {
		t.Fatal("Settings() shares memory with the definition")
	}
}

func TestContainerAffectingVolumes(t *testing.T) {
	base := testDefinition("alice")
	base.Volumes = []VolumeGrant{{Name: "photos", ReadOnly: true}, {Name: "docs"}}
	for name, volumes := range map[string][]VolumeGrant{
		"grant":   {{Name: "photos", ReadOnly: true}, {Name: "docs"}, {Name: "music"}},
		"revoke":  {{Name: "photos", ReadOnly: true}},
		"mode":    {{Name: "photos"}, {Name: "docs"}},
		"reorder": {{Name: "docs"}, {Name: "photos", ReadOnly: true}},
	} {
		d := base
		d.Volumes = volumes
		if !ContainerAffecting(base, d) || !ContainerAffecting(d, base) {
			t.Errorf("%s is not container-affecting", name)
		}
	}
	a, b := base, base
	a.Volumes, b.Volumes = nil, []VolumeGrant{}
	if ContainerAffecting(a, b) {
		t.Error("nil and empty grants differ")
	}
	if s := base.Settings(); !reflect.DeepEqual(s.Volumes, base.Volumes) {
		t.Fatalf("Settings() lost the grants: %+v", s.Volumes)
	}
}

func TestValidateVolumeGrants(t *testing.T) {
	for _, grants := range [][]VolumeGrant{nil, {}, {{Name: "photos", ReadOnly: true}, {Name: "docs"}}} {
		if err := ValidateVolumeGrants(grants); err != nil {
			t.Fatalf("%+v: %v", grants, err)
		}
	}
	for name, grants := range map[string][]VolumeGrant{
		"duplicate": {{Name: "photos"}, {Name: "photos", ReadOnly: true}},
		"bad name":  {{Name: "A"}},
		"no name":   {{}},
	} {
		if ValidateVolumeGrants(grants) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	d := testDefinition("alice")
	d.Volumes = []VolumeGrant{{Name: "photos"}, {Name: "photos"}}
	if d.validate() == nil {
		t.Error("a definition with duplicate grants is valid")
	}
}
