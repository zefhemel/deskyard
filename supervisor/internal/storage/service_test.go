package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

func TestCreateRepositoryInitShowsKey(t *testing.T) {
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		if c.Args[1] == "cat" {
			return "", Result{Code: 10}, nil
		}
		return "", Result{}, nil
	}}
	s, _ := testService(t, f)
	key, err := s.CreateRepository(context.Background(), "offsite", "nas", "deskyard-2", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(key) < 40 {
		t.Fatalf("recovery key %q", key)
	}
	if got := strings.Join(argsOf(f), ","); got != "restic cat,restic init" {
		t.Fatalf("calls %s", got)
	}
	if f.last().Env["RESTIC_REPOSITORY"] != "rclone:nas:/volume1/backups/deskyard-2" {
		t.Fatalf("repository %s", f.last().Env["RESTIC_REPOSITORY"])
	}
	if got, err := s.RecoveryKey("offsite"); err != nil || got != key {
		t.Fatalf("stored key %q %v", got, err)
	}
	if st, err := os.Stat(filepath.Join(s.keyDir("offsite"), "restic-password")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", st, err)
	}
	if _, ok := s.Store.Repository("offsite"); !ok {
		t.Fatal("not saved")
	}
}

func TestCreateRepositoryExistingNeedsKey(t *testing.T) {
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		key, _ := os.ReadFile(filepath.Join(c.Mounts[1].Source, "restic-password"))
		if string(key) == "the-key" {
			return "", Result{}, nil
		}
		return "", Result{Code: 12}, nil
	}}
	s, _ := testService(t, f)
	if _, err := s.CreateRepository(context.Background(), "offsite", "nas", "old", ""); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Store.Repository("offsite"); ok {
		t.Fatal("saved without a key")
	}
	key, err := s.CreateRepository(context.Background(), "offsite", "nas", "old", "the-key")
	if err != nil || key != "" {
		t.Fatalf("connect: %q %v", key, err)
	}
	for _, c := range f.calls {
		if c.Args[1] == "init" {
			t.Fatal("initialised an existing repository")
		}
	}
	if got, _ := s.RecoveryKey("offsite"); got != "the-key" {
		t.Fatalf("key %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(s.keyDir("offsite")))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary key folder left: %s", e.Name())
		}
	}
}

func TestCreateRepositoryWrongKey(t *testing.T) {
	f := &fakeRunner{reply: func(Call) (string, Result, error) { return "", Result{Code: 12}, nil }}
	s, _ := testService(t, f)
	if _, err := s.CreateRepository(context.Background(), "offsite", "nas", "old", "nope"); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("got %v", err)
	}
	f.reply = func(Call) (string, Result, error) { return "", Result{Code: 10}, nil }
	if _, err := s.CreateRepository(context.Background(), "offsite", "nas", "empty", "a-key"); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("a key for a missing repository: %v", err)
	}
}

func policyOf(it Item) Policy {
	return Policy{Item: it, Repository: "home", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention}
}

func TestBackUpEnvironmentMountsHomeAndDefinition(t *testing.T) {
	f := &fakeRunner{reply: resticReply}
	s, env := testService(t, f)
	env.addEnvironment(t, "alice", true, environments.Settings{Image: "img"})
	it := Item{KindEnvironment, "alice"}
	if err := s.Store.PutPolicy(policyOf(it)); err != nil {
		t.Fatal(err)
	}
	var defSeen environments.Definition
	f.reply = func(c Call) (string, Result, error) {
		if c.Args[1] == "backup" {
			for _, m := range c.Mounts {
				if m.Target == "/deskyard/environment/alice/definition.json" {
					b, _ := os.ReadFile(m.Source)
					_ = json.Unmarshal(b, &defSeen)
				}
			}
		}
		return resticReply(c)
	}
	if err := s.BackUp(context.Background(), it, "manual", nil); err != nil {
		t.Fatal(err)
	}
	var backup Call
	for _, c := range f.calls {
		if c.Args[1] == "backup" {
			backup = c
		}
	}
	args := strings.Join(backup.Args, " ")
	for _, want := range []string{"--tag deskyard", "--tag environment:alice", "--tag manual", "--host deskyard-0123456789ab", " /deskyard/environment/alice"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	mounts := map[string]engine.Mount{}
	for _, m := range backup.Mounts {
		mounts[m.Target] = m
	}
	if m := mounts["/deskyard/environment/alice/home"]; m.Source != env.layout.HomeOf("alice") || !m.ReadOnly || !m.MustExist {
		t.Errorf("home mount %+v", m)
	}
	if m := mounts["/deskyard/environment/alice/definition.json"]; !m.ReadOnly || defSeen.Name != "alice" || defSeen.Image.Ref != "img" {
		t.Errorf("definition mount %+v, %+v", m, defSeen)
	} else if _, err := os.Stat(m.Source); !os.IsNotExist(err) {
		t.Error("definition copy left behind")
	}
	st := s.State.Get(it)
	if st.LastSuccess.IsZero() || st.Error != "" || st.Snapshot != "snap1" {
		t.Fatalf("state %+v", st)
	}
	if got := strings.Join(argsOf(f), ","); !strings.Contains(got, "restic backup,restic forget,restic prune") {
		t.Fatalf("calls %s", got)
	}
}

func TestBackUpFailureRecorded(t *testing.T) {
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		if c.Args[1] == "backup" {
			return "", Result{Code: 1, Stderr: "Fatal: unable to open repository: connection refused"}, nil
		}
		return resticReply(c)
	}}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	it := Item{KindVolume, "photos"}
	_ = s.Store.PutPolicy(policyOf(it))
	err := s.BackUp(context.Background(), it, "scheduled", nil)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("got %v", err)
	}
	st := s.State.Get(it)
	if st.LastAttempt.IsZero() || !st.LastSuccess.IsZero() || !strings.Contains(st.Error, "connection refused") {
		t.Fatalf("state %+v", st)
	}
	for _, c := range f.calls {
		if c.Args[1] == "forget" || c.Args[1] == "prune" {
			t.Fatal("forgot after a failed backup")
		}
	}
}

func TestBackUpForgetsAndPrunesWeekly(t *testing.T) {
	f := &fakeRunner{reply: resticReply}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	it := Item{KindVolume, "photos"}
	_ = s.Store.PutPolicy(policyOf(it))
	now := time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	for i := range 3 {
		if err := s.BackUp(context.Background(), it, "scheduled", nil); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour * time.Duration(i+3)) // +3d, then +4d: the third is a week later
	}
	prunes, forgets := 0, 0
	for _, c := range f.calls {
		switch c.Args[1] {
		case "prune":
			prunes++
		case "forget":
			forgets++
		}
	}
	if forgets != 3 || prunes != 2 {
		t.Fatalf("forgets %d prunes %d: %v", forgets, prunes, argsOf(f))
	}
}

func TestHoldRefusesSecondOperation(t *testing.T) {
	f := &fakeRunner{reply: resticReply}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	it := Item{KindVolume, "photos"}
	release, err := s.Hold(it, "backing up")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Busy(KindVolume, "photos"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	if _, err := s.Hold(it, "restoring"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second hold: %v", err)
	}
	release()
	if err := s.Busy(KindVolume, "photos"); err != nil {
		t.Fatalf("released: %v", err)
	}
}

func TestSchedulerRunsDueOnce(t *testing.T) {
	f := &fakeRunner{reply: resticReply}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	it := Item{KindVolume, "photos"}
	p := policyOf(it)
	_ = s.Store.PutPolicy(p)
	p, _ = s.Store.Policy(it)
	now := p.Updated.Add(48 * time.Hour)
	s.Now = func() time.Time { return now }
	s.RunDue(context.Background())
	s.RunDue(context.Background())
	backups := 0
	for _, c := range f.calls {
		if c.Args[1] == "backup" {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("%d backups", backups)
	}
	// A policy of an item that no longer exists is skipped, not failed in a loop.
	_ = s.Store.PutPolicy(policyOf(Item{KindVolume, "gone"}))
	now = now.Add(72 * time.Hour)
	s.RunDue(context.Background())
	if st := s.State.Get(Item{KindVolume, "gone"}); st.Error == "" {
		t.Fatalf("missing item not reported: %+v", st)
	}
}

func TestDeleteRemoteInUse(t *testing.T) {
	s, _ := testService(t, &fakeRunner{})
	if err := s.DeleteRemote("nas"); !errors.Is(err, ErrInUse) {
		t.Fatalf("got %v", err)
	}
	if err := s.RemoveRepository("home"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.keyDir("home")); !os.IsNotExist(err) {
		t.Fatal("recovery key left behind")
	}
	if err := s.DeleteRemote("nas"); err != nil {
		t.Fatal(err)
	}
}

func TestUnsavedRemoteTestCleansUp(t *testing.T) {
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		conf, _ := os.ReadFile(filepath.Join(c.Mounts[0].Source, "rclone.conf"))
		if !strings.Contains(string(conf), "[cloud]") {
			return "", Result{Code: 1, Stderr: "no config"}, nil
		}
		return "", Result{}, nil
	}}
	s, _ := testService(t, f)
	v := validRemotes()[WebDAV]
	if err := s.TestRemote(context.Background(), v.r, v.sec); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.layout.Secret, "remotes"))
	for _, e := range entries {
		if e.Name() != "nas" {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
	// A failing test refuses to save.
	f.reply = func(Call) (string, Result, error) { return "", Result{Code: 1, Stderr: "401 Unauthorized"}, nil }
	if err := s.CreateRemote(context.Background(), v.r, v.sec); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Remotes.Get("cloud"); ok {
		t.Fatal("saved a remote whose test failed")
	}
}

func TestSnapshotsAreThisHostsAndItems(t *testing.T) {
	out := `[
{"time":"2026-10-26T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","volume:photos","scheduled"],"id":"a1","short_id":"a1"},
{"time":"2026-10-27T03:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","volume:photos","manual"],"id":"a2","short_id":"a2"},
{"time":"2026-10-27T03:00:00Z","hostname":"deskyard-ffffffffffff","tags":["deskyard","volume:photos","scheduled"],"id":"b1","short_id":"b1"},
{"time":"2026-10-27T04:00:00Z","hostname":"deskyard-0123456789ab","tags":["deskyard","environment:bob","scheduled"],"id":"c1","short_id":"c1"}]`
	f := &fakeRunner{reply: func(Call) (string, Result, error) { return out, Result{}, nil }}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	snaps, err := s.Snapshots(context.Background(), Item{KindVolume, "photos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].ID != "a2" || snaps[0].Reason != "manual" || snaps[0].Repository != "home" {
		t.Fatalf("%+v", snaps)
	}
	cat, errs := s.Catalog(context.Background())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(cat) != 3 {
		t.Fatalf("catalog %+v", cat)
	}
	for _, c := range cat {
		local := c.Host == s.Tool.Host && c.Item == (Item{KindVolume, "photos"})
		if c.Local != local {
			t.Errorf("%+v local=%v", c, c.Local)
		}
	}
	if calls := len(f.calls); calls != 1 {
		t.Fatalf("listing not cached: %d calls", calls)
	}
}

func TestBackUpRecordsWarnings(t *testing.T) {
	f := &fakeRunner{reply: func(c Call) (string, Result, error) {
		if c.Args[1] == "backup" {
			return `{"message_type":"summary","snapshot_id":"snap1"}` + "\n", Result{Code: 3, Stderr: `{"message_type":"error","error":{"message":"open /x/locked: permission denied"},"during":"archival","item":"/x/locked"}` + "\n"}, nil
		}
		return resticReply(c)
	}}
	s, env := testService(t, f)
	env.addVolume(t, "photos")
	it := Item{KindVolume, "photos"}
	_ = s.Store.PutPolicy(policyOf(it))
	if err := s.BackUp(context.Background(), it, ReasonManual, nil); err != nil {
		t.Fatal(err)
	}
	st := s.State.Get(it)
	if st.Snapshot != "snap1" || st.Error != "" || len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "permission denied") {
		t.Fatalf("state %+v", st)
	}
}
