package environments

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
		Mounts:        []HostMount{{Source: "/srv/photos", Target: "/mnt/photos", ReadOnly: true}},
		Camera:        "/dev/video10",
		MemoryReclaim: true,
		Power:         PowerRunning,
	}
}

func TestDefinitionsRoundTripSpecKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environments.json")
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
	envs := raw["environments"].([]any)
	if len(envs) != 2 {
		t.Fatalf("environments = %v", envs)
	}
	first := envs[0].(map[string]any)
	if first["name"] != "alice" {
		t.Fatalf("environments are saved sorted by name, got %v first", first["name"])
	}
	for _, key := range []string{"name", "created", "revision", "image", "limits", "network", "mounts", "dockerAccess", "camera", "memoryReclaim", "power"} {
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
	if m := first["mounts"].([]any)[0].(map[string]any); m["source"] != "/srv/photos" || m["target"] != "/mnt/photos" || m["readOnly"] != true {
		t.Errorf("mounts = %v", m)
	}
	if first["created"] != "2026-10-26T10:00:00Z" || first["power"] != "running" || first["camera"] != "/dev/video10" {
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
	path := filepath.Join(t.TempDir(), "environments.json")
	example := `{
  "version": 1,
  "environments": [
    {
      "name": "alice",
      "created": "2026-10-26T10:00:00Z",
      "revision": 4,
      "image": {"ref": "ghcr.io/x/deskyard-xfce:latest", "id": "sha256:1"},
      "limits": {"memoryBytes": 0, "nanoCPUs": 0},
      "network": {"kind": "isolated"},
      "mounts": [{"source": "/srv/photos", "target": "/mnt/photos", "readOnly": true}],
      "dockerAccess": false,
      "camera": "",
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
	if !ok || d.Revision != 4 || !d.MemoryReclaim || d.Power != PowerRunning || len(d.Mounts) != 1 || !d.Mounts[0].ReadOnly {
		t.Fatalf("alice = %+v, %v", d, ok)
	}
}

func TestDefinitionsLoadRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environments.json")
	for _, body := range []string{`{"version": 2, "environments": []}`, `{"environments": []}`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefinitions(path); err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("%s: err = %v", body, err)
		}
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
		"bad mount":     func(d *Definition) { d.Mounts = []HostMount{{Source: "relative", Target: "/mnt/x"}} },
		"bad camera":    func(d *Definition) { d.Camera = "/dev/sda" },
		"unknown power": func(d *Definition) { d.Power = "paused" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := valid
			d.Mounts = append([]HostMount(nil), valid.Mounts...)
			mutate(&d)
			path := filepath.Join(t.TempDir(), "environments.json")
			b, _ := json.Marshal(definitionsFile{Version: 1, Environments: []Definition{d}})
			if err := os.WriteFile(path, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadDefinitions(path); err == nil {
				t.Fatal("malformed entry loaded")
			}
			defs, err := LoadDefinitions(filepath.Join(t.TempDir(), "environments.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := defs.Put(d); err == nil {
				t.Fatal("Put accepted a malformed definition")
			}
		})
	}

	path := filepath.Join(t.TempDir(), "environments.json")
	for _, body := range []string{
		`not json`,
		`{"version": 1, "environments": [{"name": "alice", "unknown": 1}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDefinitions(path); err == nil {
			t.Errorf("%s loaded", body)
		}
	}
	b, _ := json.Marshal(definitionsFile{Version: 1, Environments: []Definition{valid, valid}})
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
	defs, err := LoadDefinitions(filepath.Join(dir, "environments.json"))
	if err != nil {
		t.Fatal(err)
	}
	good := defs.path
	defs.path = filepath.Join(parent, "environments.json")
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
	defs.path = filepath.Join(parent, "environments.json")
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
	path := filepath.Join(t.TempDir(), "environments.json")
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
	defs, err := LoadDefinitions(filepath.Join(t.TempDir(), "environments.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := testDefinition("alice")
	if err := defs.Put(d); err != nil {
		t.Fatal(err)
	}
	d.Mounts[0].Target = "/mnt/changed"
	got, _ := defs.Get("alice")
	got.Mounts[0].Source = "/changed"
	if again, _ := defs.Get("alice"); again.Mounts[0].Target != "/mnt/photos" || again.Mounts[0].Source != "/srv/photos" {
		t.Fatalf("stored definition shares memory with callers: %+v", again.Mounts)
	}
}

func TestDefinitionsDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environments.json")
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
		"mounts":        func(d *Definition) { d.Mounts = nil },
		"mount mode":    func(d *Definition) { d.Mounts = []HostMount{{Source: "/srv/photos", Target: "/mnt/photos"}} },
		"docker access": func(d *Definition) { d.DockerAccess = true },
		"camera":        func(d *Definition) { d.Camera = "" },
	}
	for name, mutate := range affecting {
		d := base
		d.Mounts = append([]HostMount(nil), base.Mounts...)
		mutate(&d)
		if !ContainerAffecting(base, d) || !ContainerAffecting(d, base) {
			t.Errorf("%s change is not container-affecting", name)
		}
	}
	for name, mutate := range map[string]func(d *Definition){
		"memoryReclaim": func(d *Definition) { d.MemoryReclaim = !d.MemoryReclaim },
		"power":         func(d *Definition) { d.Power = PowerStopped },
		"revision":      func(d *Definition) { d.Revision++ },
		"empty mounts":  func(d *Definition) { d.Mounts = nil },
	} {
		a, b := base, base
		if name == "empty mounts" {
			a.Mounts = []HostMount{}
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
	want := Settings{Image: d.Image.Ref, MemoryBytes: 1 << 30, NanoCPUs: 2e9, Camera: "/dev/video10", VPNConnectionID: "conn-1", LANExceptions: d.Network.LANExceptions, Mounts: d.Mounts, DockerAccess: true}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("Settings() = %+v", s)
	}
	s.Mounts[0].Source = "/changed"
	s.LANExceptions[0].Ports[0].Start = 1
	if d.Mounts[0].Source != "/srv/photos" || d.Network.LANExceptions[0].Ports[0].Start != 22 {
		t.Fatal("Settings() shares memory with the definition")
	}
}
