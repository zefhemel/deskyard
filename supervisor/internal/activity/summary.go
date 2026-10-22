package activity

import (
	"slices"
	"sort"
	"time"
)

// Presence is an agent that is on a desktop right now.
type Presence struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Owner      string    `json:"owner"`
	Inside     bool      `json:"inside"`
	Color      string    `json:"color"`
	Active     bool      `json:"active"`
	LastActive time.Time `json:"lastActive"`
}

// NowItem is something an agent is doing at this moment.
type NowItem struct {
	Kind  string    `json:"kind"`           // "command", "browser", "forward"
	Text  string    `json:"text,omitempty"` // command display form
	Since time.Time `json:"since"`
}

// RecentItem is one line of what an agent did lately.
type RecentItem struct {
	Kind         string    `json:"kind"` // "command", "screen", "browser", "copy"
	T            time.Time `json:"t"`    // end time (command, browser) or time of the newest merged action
	Text         string    `json:"text,omitempty"`
	Exit         *int      `json:"exit,omitempty"`
	Disconnected bool      `json:"disconnected,omitempty"`
	DurationMs   int64     `json:"durationMs,omitempty"`
	Action       string    `json:"action,omitempty"`
	Count        int       `json:"count,omitempty"`
	Direction    string    `json:"direction,omitempty"`
	Path         string    `json:"path,omitempty"`
	Size         int64     `json:"size,omitempty"`
}

// maxRecent is how many lines Recent keeps.
const maxRecent = 20

// Summary is an agent's presence plus what it is doing and has just done.
type Summary struct {
	Presence
	Now    []NowItem    `json:"now"`    // never nil
	Recent []RecentItem `json:"recent"` // never nil, at most 20, newest first
}

// event is a recorded item, remembered with the agent it belongs to.
type event struct {
	agent string
	item  RecentItem
}

// recentFor lists an agent's items inside the window, newest first, with
// adjacent screen actions of the same kind merged, cut to maxRecent.
func recentFor(events []event, agent string, cutoff time.Time) []RecentItem {
	var mine []RecentItem
	for _, e := range events {
		if e.agent == agent && !e.item.T.Before(cutoff) {
			mine = append(mine, e.item)
		}
	}
	slices.Reverse(mine) // later records first among equal times
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].T.After(mine[j].T) })
	out := []RecentItem{}
	for _, it := range mine {
		if n := len(out); n > 0 && it.Kind == "screen" && out[n-1].Kind == "screen" && out[n-1].Action == it.Action {
			out[n-1].Count += max(it.Count, 1)
			continue
		}
		if it.Kind == "screen" {
			it.Count = max(it.Count, 1)
		}
		out = append(out, it)
	}
	return out[:min(len(out), maxRecent)]
}
