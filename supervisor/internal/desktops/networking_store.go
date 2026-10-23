package desktops

import (
	"errors"
	"io/fs"
	"slices"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// Full recovery snapshot is private (it includes the Caddy token) and never
// serialized by an API. Records also retain LAN settings after explicit disable.
type networkIntent struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Token           string    `json:"token"`
	Created         time.Time `json:"created"`
	Settings        Settings  `json:"settings"`
	Running         bool      `json:"running"`
	Revision        uint64    `json:"revision"`
	AppliedRevision uint64    `json:"appliedRevision"`
	RuntimeID       string    `json:"runtimeId"`
	Pending         bool      `json:"pending"`
	Removing        bool      `json:"removing,omitempty"`
	DeleteFiles     bool      `json:"deleteFiles,omitempty"`
	Error           string    `json:"error,omitempty"`
}

func cloneSettings(s Settings) Settings {
	s.LANExceptions = slices.Clone(s.LANExceptions)
	for i := range s.LANExceptions {
		s.LANExceptions[i].Ports = slices.Clone(s.LANExceptions[i].Ports)
	}
	return s
}
func (n *Networking) load() error {
	if err := store.ReadJSON(n.path, &n.records); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errors.New("invalid desktop network recovery state")
	}
	if n.records == nil {
		n.records = map[string]networkIntent{}
	}
	names := map[string]bool{}
	for id, r := range n.records {
		if id != r.ID || id == "" || ValidName(r.Name) != nil || names[r.Name] || r.Token == "" || r.Settings.Image == "" {
			return errors.New("invalid desktop network recovery state")
		}
		names[r.Name] = true
	}
	return nil
}
func (n *Networking) all() []networkIntent {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]networkIntent, 0, len(n.records))
	for _, r := range n.records {
		r.Settings = cloneSettings(r.Settings)
		out = append(out, r)
	}
	return out
}
func (n *Networking) record(name string) (networkIntent, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, r := range n.records {
		if r.Name == name {
			r.Settings = cloneSettings(r.Settings)
			return r, true
		}
	}
	return networkIntent{}, false
}
func (n *Networking) saveRecord(r networkIntent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	next := make(map[string]networkIntent, len(n.records)+1)
	for k, v := range n.records {
		next[k] = v
	}
	r.Settings = cloneSettings(r.Settings)
	next[r.ID] = r
	if err := store.WriteJSON(n.path, next); err != nil {
		return err
	}
	n.records = next
	return nil
}
func (n *Networking) forget(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	next := make(map[string]networkIntent, len(n.records))
	for k, v := range n.records {
		if k != id {
			next[k] = v
		}
	}
	if err := store.WriteJSON(n.path, next); err != nil {
		return err
	}
	n.records = next
	delete(n.statuses, id)
	return nil
}
func (n *Networking) setStatus(id string, status vpn.Status) {
	n.mu.Lock()
	defer n.mu.Unlock()
	old := n.statuses[id]
	if status.LastExitIP == "" {
		status.LastExitIP = old.LastExitIP
		status.ExitIPObservedAt = old.ExitIPObservedAt
	}
	if status.State == old.State {
		status.ChangedAt = old.ChangedAt
	}
	if status.ChangedAt.IsZero() {
		status.ChangedAt = time.Now().UTC()
	}
	n.statuses[id] = status
}
