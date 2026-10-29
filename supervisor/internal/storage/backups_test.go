package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
)

func TestParseItem(t *testing.T) {
	if it, err := ParseItem("computer", "alice"); err != nil || it.Tag() != "computer:alice" {
		t.Fatalf("%+v %v", it, err)
	}
	if it, err := ParseTag("volume:photos"); err != nil || it != (Item{"volume", "photos"}) {
		t.Fatalf("%+v %v", it, err)
	}
	for _, bad := range [][2]string{{"home", "alice"}, {"volume", "A"}, {"computer", ""}, {"environment", "alice"}} {
		if _, err := ParseItem(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backups.json")
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreRepositoryInUse(t *testing.T) {
	s, path := testStore(t)
	repo := Repository{Name: "home", Remote: "nas", Folder: "deskyard-backups", Created: time.Now().UTC().Truncate(time.Second)}
	if err := s.AddRepository(repo); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepository(repo); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	p := Policy{Item: Item{"volume", "photos"}, Repository: "home", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention}
	if err := s.PutPolicy(p); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRepository("home"); !errors.Is(err, ErrInUse) {
		t.Fatalf("removed a repository in use: %v", err)
	}
	if got := s.RepositoriesOnRemote("nas"); len(got) != 1 || got[0] != "home" {
		t.Fatalf("on remote: %v", got)
	}
	again, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := again.Policy(p.Item); !ok || got.Repository != "home" || got.Schedule != DefaultSchedule || got.Updated.IsZero() {
		t.Fatalf("reloaded %+v", got)
	}
	if err := again.DeletePolicy(p.Item); err != nil {
		t.Fatal(err)
	}
	if err := again.RemoveRepository("home"); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyNeedsRepository(t *testing.T) {
	s, _ := testStore(t)
	p := Policy{Item: Item{"volume", "photos"}, Repository: "nowhere", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention}
	if err := s.PutPolicy(p); err == nil {
		t.Fatal("policy of an unknown repository saved")
	}
	_ = s.AddRepository(Repository{Name: "home", Remote: "nas", Folder: "x", Created: time.Now()})
	p.Repository = "home"
	p.Retention = Retention{}
	if err := s.PutPolicy(p); err == nil {
		t.Fatal("policy keeping nothing saved")
	}
	p.Retention = DefaultRetention
	p.Schedule = Schedule{Every: "never"}
	if err := s.PutPolicy(p); err == nil {
		t.Fatal("bad schedule saved")
	}
}

func TestStoreStrictLoad(t *testing.T) {
	for name, body := range map[string]string{
		"version":       `{"version":2}`,
		"unknown field": `{"version":1,"repositories":[{"name":"home","remote":"nas","folder":"x","created":"2026-10-27T00:00:00Z","color":"red"}],"policies":[]}`,
		"bad item":      `{"version":1,"repositories":[{"name":"home","remote":"nas","folder":"x","created":"2026-10-27T00:00:00Z"}],"policies":[{"item":"home:alice","repository":"home","enabled":true,"schedule":{"every":"daily","at":"03:00"},"retention":{"daily":7}}]}`,
	} {
		path := filepath.Join(t.TempDir(), "backups.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadStore(path); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestStateRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "backups.json")
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	it := Item{KindComputer, "alice"}
	now := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	if err := st.Record(it, RunState{LastAttempt: now, LastSuccess: now, Snapshot: "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPrune("home", now); err != nil {
		t.Fatal(err)
	}
	again, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get(it); !got.LastSuccess.Equal(now) || got.Snapshot != "abc" {
		t.Fatalf("%+v", got)
	}
	if !again.LastPrune("home").Equal(now) {
		t.Fatal("prune time lost")
	}
	if err := again.Forget(it); err != nil || !again.Get(it).LastAttempt.IsZero() {
		t.Fatal("forget")
	}
}

// Snapshots taken before computers were called computers carry the tag
// environment:NAME; it reads as the computer.
func TestParseTagLegacyEnvironment(t *testing.T) {
	it, err := ParseTag("environment:alice")
	if err != nil || it != (Item{Kind: KindComputer, Name: "alice"}) {
		t.Fatalf("%+v %v", it, err)
	}
	if it.Tag() != "computer:alice" {
		t.Fatalf("tag %s", it.Tag())
	}
	if got := it.Tags(); len(got) != 2 || got[0] != "computer:alice" || got[1] != "environment:alice" {
		t.Fatalf("tags %v", got)
	}
	if got := (Item{KindVolume, "photos"}).Tags(); len(got) != 1 || got[0] != "volume:photos" {
		t.Fatalf("volume tags %v", got)
	}
	if _, err := ParseTag("environment:"); err == nil {
		t.Fatal("accepted an empty name")
	}
}

func TestBackupStoreMigratesKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backups.json")
	legacy := `{"version":1,"repositories":[{"name":"home","remote":"nas","folder":"x","created":"2026-10-27T00:00:00Z"}],` +
		`"policies":[{"item":"environment:alice","repository":"home","enabled":true,"schedule":{"every":"daily","at":"03:00"},"retention":{"daily":7},"updated":"2026-10-27T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	alice := Item{KindComputer, "alice"}
	if p, ok := s.Policy(alice); !ok || p.Repository != "home" || p.Retention.Daily != 7 {
		t.Fatalf("policy %+v %v", p, ok)
	}
	if err := s.RemoveRepository("home"); !errors.Is(err, ErrInUse) {
		t.Fatalf("remove: %v", err)
	}
	if err := s.PutPolicy(Policy{Item: Item{KindVolume, "photos"}, Repository: "home", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "environment:") || !strings.Contains(string(b), `"item": "computer:alice"`) {
		t.Fatalf("saved %s", b)
	}
	again, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Policy(alice); !ok {
		t.Fatal("policy lost on reload")
	}
}

// The run state, keyed by item tag, keeps a computer's last run across the
// rename.
func TestBackupStateMigratesKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backups.json")
	legacy := `{"items":{"environment:alice":{"lastAttempt":"2026-10-27T03:00:00Z","lastSuccess":"2026-10-27T03:00:00Z","snapshot":"old"},` +
		`"environment:bob":{"lastAttempt":"2026-10-26T03:00:00Z","lastSuccess":"0001-01-01T00:00:00Z","snapshot":"stale"},` +
		`"computer:bob":{"lastAttempt":"2026-10-28T03:00:00Z","lastSuccess":"2026-10-28T03:00:00Z","snapshot":"new"},` +
		`"volume:photos":{"lastAttempt":"2026-10-27T03:00:00Z","lastSuccess":"2026-10-27T03:00:00Z"}},"prunes":{}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(Item{KindComputer, "alice"}); got.Snapshot != "old" {
		t.Fatalf("alice %+v", got)
	}
	if got := st.Get(Item{KindComputer, "bob"}); got.Snapshot != "new" {
		t.Fatalf("bob %+v", got)
	}
	if got := st.Get(Item{KindVolume, "photos"}); got.LastSuccess.IsZero() {
		t.Fatalf("photos %+v", got)
	}
	if err := st.RecordPrune("home", time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "environment:") {
		t.Fatalf("saved %s", b)
	}
}

// Snapshots of a computer taken before the rename are tagged
// environment:NAME. They are listed, found for browsing and restoring,
// counted with the new ones in the catalog, and covered by retention; new
// snapshots are tagged computer:NAME, and no tag in the repository is
// rewritten.
func TestListSnapshotsIncludesLegacyTag(t *testing.T) {
	out := `[
{"time":"2026-10-26T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","environment:alice","scheduled"],"id":"old1","short_id":"old1"},
{"time":"2026-10-28T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","computer:alice","scheduled"],"id":"new1","short_id":"new1"},
{"time":"2026-10-28T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","environment:bob","scheduled"],"id":"bob1","short_id":"bob1"}]`
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		if c.Args[1] == "snapshots" {
			return out, Result{}, nil
		}
		return resticReply(c)
	}}
	s, env := testService(t, f)
	env.addComputer(t, "alice", false, computers.Settings{Image: "img"})
	alice := Item{KindComputer, "alice"}
	snaps, err := s.Snapshots(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].ID != "new1" || snaps[1].ID != "old1" {
		t.Fatalf("snapshots %+v", snaps)
	}
	if _, err := s.snapshotOf(context.Background(), "home", alice, "old1"); err != nil {
		t.Fatalf("old snapshot not found: %v", err)
	}
	if _, err := s.snapshotOf(context.Background(), "home", alice, "bob1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another computer's snapshot: %v", err)
	}
	cat, errs := s.Catalog(context.Background())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var aliceRows []CatalogItem
	for _, c := range cat {
		if c.Item == alice {
			aliceRows = append(aliceRows, c)
		}
		if strings.HasPrefix(c.Item.Tag(), "environment:") {
			t.Errorf("legacy item in the catalog: %+v", c)
		}
	}
	if len(aliceRows) != 1 || aliceRows[0].Snapshots != 2 || aliceRows[0].LatestID != "new1" || !aliceRows[0].Local {
		t.Fatalf("catalog %+v", cat)
	}

	if err := s.Store.PutPolicy(policyOf(alice)); err != nil {
		t.Fatal(err)
	}
	if err := s.BackUp(context.Background(), alice, ReasonManual, nil); err != nil {
		t.Fatal(err)
	}
	var backup, forget []string
	for _, c := range f.calls {
		switch c.Args[1] {
		case "backup":
			backup = c.Args
		case "forget":
			forget = c.Args
		case "tag":
			t.Fatalf("rewrote tags: %v", c.Args)
		}
	}
	if b := strings.Join(backup, " "); !strings.Contains(b, "--tag computer:alice") || strings.Contains(b, "environment:alice") {
		t.Fatalf("backup %s", b)
	}
	// Repeated --tag flags select snapshots with either tag.
	if fg := strings.Join(forget, " "); !strings.Contains(fg, "--tag computer:alice --tag environment:alice") || strings.Contains(fg, "computer:alice,environment:alice") {
		t.Fatalf("forget %s", fg)
	}
}
