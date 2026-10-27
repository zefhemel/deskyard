package storage

import (
	"errors"
	"fmt"
	"time"
)

// Schedule is when an item is backed up: Every is hourly (at At's minutes),
// daily (at At) or weekly (on Weekday, 0 being Sunday, at At), in the
// supervisor's local time.
type Schedule struct {
	Every   string `json:"every"`
	At      string `json:"at"`
	Weekday int    `json:"weekday"`
}

// DefaultSchedule is daily at 03:00.
var DefaultSchedule = Schedule{Every: "daily", At: "03:00"}

func (s Schedule) clock() (hour, minute int, err error) {
	at := s.At
	if at == "" {
		at = "00:00"
	}
	t, err := time.Parse("15:04", at)
	if err != nil || len(at) != 5 {
		return 0, 0, fmt.Errorf("the time %q isn't HH:MM", s.At)
	}
	return t.Hour(), t.Minute(), nil
}

func (s Schedule) Validate() error {
	switch s.Every {
	case "hourly", "daily", "weekly":
	default:
		return fmt.Errorf("a schedule is hourly, daily or weekly, not %q", s.Every)
	}
	if s.Weekday < 0 || s.Weekday > 6 {
		return errors.New("the weekday is 0 (Sunday) to 6")
	}
	_, _, err := s.clock()
	return err
}

// Next is the first time after after that s runs.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	hour, minute, err := s.clock()
	if err != nil {
		return time.Time{}
	}
	local := after.In(loc)
	if s.Every == "hourly" {
		t := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), minute, 0, 0, loc)
		for !t.After(after) {
			t = t.Add(time.Hour)
		}
		return t
	}
	// Day by day from after's date: time.Date normalises a time a clock
	// change skips, so a skipped run happens later that day instead.
	for d := 0; d <= 8; d++ {
		t := time.Date(local.Year(), local.Month(), local.Day()+d, hour, minute, 0, 0, loc)
		if !t.After(after) || s.Every == "weekly" && int(t.Weekday()) != s.Weekday {
			continue
		}
		return t
	}
	return time.Time{}
}

// base is when p's schedule counts from: its last attempt, or when it was
// last changed, whichever is later.
func base(p Policy, st RunState) time.Time {
	if st.LastAttempt.After(p.Updated) {
		return st.LastAttempt
	}
	return p.Updated
}

// NextRun is when p runs next, given its state.
func NextRun(p Policy, st RunState, loc *time.Location) time.Time {
	return p.Schedule.Next(base(p, st), loc)
}

// Due reports whether p should run now. A run missed while the supervisor
// was down is due once; a failed one waits for the next slot.
func Due(p Policy, st RunState, now time.Time, loc *time.Location) bool {
	if !p.Enabled {
		return false
	}
	next := NextRun(p, st, loc)
	return !next.IsZero() && !next.After(now)
}
