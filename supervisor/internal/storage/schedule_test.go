package storage

import (
	"testing"
	"time"
)

func TestScheduleNext(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, ams)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct {
		s           Schedule
		after, want string
	}{
		{Schedule{Every: "daily", At: "03:00"}, "2026-10-20 02:00", "2026-10-20 03:00"},
		{Schedule{Every: "daily", At: "03:00"}, "2026-10-20 03:00", "2026-10-21 03:00"},
		{Schedule{Every: "daily", At: "03:00"}, "2026-10-20 10:00", "2026-10-21 03:00"},
		{Schedule{Every: "hourly", At: "00:15"}, "2026-10-20 10:20", "2026-10-20 11:15"},
		{Schedule{Every: "hourly", At: "00:15"}, "2026-10-20 10:10", "2026-10-20 10:15"},
		{Schedule{Every: "hourly"}, "2026-10-20 10:10", "2026-10-20 11:00"},
		// Weekday 0 is Sunday; 2026-10-24 is a Saturday.
		{Schedule{Every: "weekly", At: "03:00", Weekday: 0}, "2026-10-24 12:00", "2026-10-25 03:00"},
		{Schedule{Every: "weekly", At: "03:00", Weekday: 6}, "2026-10-24 12:00", "2026-10-31 03:00"},
		// The clocks went back on 2026-10-25 at 03:00: 02:30 happens twice, the run once.
		{Schedule{Every: "daily", At: "02:30"}, "2026-10-24 12:00", "2026-10-25 02:30"},
	} {
		got := tc.s.Next(at(tc.after), ams)
		if want := at(tc.want); !got.Equal(want) {
			t.Errorf("%+v after %s: %s, want %s", tc.s, tc.after, got, want)
		}
	}
	// Spring forward: 02:30 doesn't exist on 2026-03-29; the run still happens that day.
	got := Schedule{Every: "daily", At: "02:30"}.Next(at("2026-03-28 12:00"), ams)
	if y, m, d := got.In(ams).Date(); y != 2026 || m != 3 || d != 29 {
		t.Errorf("spring forward: %s", got)
	}
}

func TestScheduleValidate(t *testing.T) {
	for _, s := range []Schedule{{Every: "daily", At: "03:00"}, {Every: "hourly", At: "00:45"}, {Every: "weekly", At: "23:59", Weekday: 6}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	for _, s := range []Schedule{{Every: "monthly", At: "03:00"}, {Every: "daily", At: "3am"}, {Every: "daily", At: "24:00"}, {Every: "weekly", At: "03:00", Weekday: 7}} {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v accepted", s)
		}
	}
}

func TestDueMissedRunOnce(t *testing.T) {
	loc := time.UTC
	p := Policy{Item: Item{"volume", "photos"}, Repository: "home", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention,
		Updated: time.Date(2026, 10, 1, 0, 0, 0, 0, loc)}
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, loc)
	st := RunState{LastAttempt: time.Date(2026, 10, 26, 3, 0, 0, 0, loc)}
	if !Due(p, st, now, loc) {
		t.Fatal("missed run not due")
	}
	// Attempted at 10:00 (and failed): not due again until tomorrow 03:00.
	st = RunState{LastAttempt: now, Error: "connection refused"}
	if Due(p, st, now.Add(time.Hour), loc) {
		t.Fatal("a failure is retried before the next slot")
	}
	if !Due(p, st, time.Date(2026, 10, 28, 3, 0, 0, 0, loc), loc) {
		t.Fatal("next slot not due")
	}
	p.Enabled = false
	if Due(p, RunState{}, now, loc) {
		t.Fatal("disabled policy due")
	}
	// A new policy waits for its first slot rather than running at once.
	fresh := Policy{Item: p.Item, Repository: "home", Enabled: true, Schedule: DefaultSchedule, Retention: DefaultRetention, Updated: now}
	if Due(fresh, RunState{}, now.Add(time.Minute), loc) {
		t.Fatal("a new policy ran at once")
	}
	if got := NextRun(fresh, RunState{}, loc); !got.Equal(time.Date(2026, 10, 28, 3, 0, 0, 0, loc)) {
		t.Fatalf("next run %s", got)
	}
}
