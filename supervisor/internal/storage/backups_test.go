package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseItem(t *testing.T) {
	if it, err := ParseItem("environment", "alice"); err != nil || it.Tag() != "environment:alice" {
		t.Fatalf("%+v %v", it, err)
	}
	if it, err := ParseTag("volume:photos"); err != nil || it != (Item{"volume", "photos"}) {
		t.Fatalf("%+v %v", it, err)
	}
	for _, bad := range [][2]string{{"home", "alice"}, {"volume", "A"}, {"environment", ""}} {
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
	it := Item{"environment", "alice"}
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
