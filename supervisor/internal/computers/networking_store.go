package computers

import (
	"errors"
	"io/fs"
	"slices"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// networkTransition is the private transition state of a computer whose
// networking the VPN coordinator manages: those whose definition selects a
// VPN, and those still leaving one. It holds no settings; the definition does.
// It is never serialized by an API.
type networkTransition struct {
	Name string `json:"name"`
	// ConnectionRevision is the VPN connection revision being applied, and
	// AppliedConnectionRevision the one the runtime was last built from.
	ConnectionRevision        uint64 `json:"connectionRevision"`
	AppliedConnectionRevision uint64 `json:"appliedConnectionRevision"`
	// AppliedRevision is the definition revision last applied; a definition
	// ahead of it is applied, fail closed, before the computer runs.
	AppliedRevision uint64 `json:"appliedRevision"`
	RuntimeID       string `json:"runtimeId"`
	Pending         bool   `json:"pending"`
	// Repair is set when the transition repairs the applied state (a lost
	// runtime or container) rather than applying a change; the poll retries
	// a failed repair, never a failed change.
	Repair      bool   `json:"repair,omitempty"`
	Removing    bool   `json:"removing,omitempty"`
	DeleteFiles bool   `json:"deleteFiles,omitempty"`
	Error       string `json:"error,omitempty"`
}

// transitionsFile is state/network/transitions.json.
type transitionsFile struct {
	Version     int                 `json:"version"`
	Transitions []networkTransition `json:"transitions"`
}

const transitionsVersion = 1

var errTransitions = errors.New("invalid computer network recovery state")

func (n *Networking) load() error {
	var f transitionsFile
	if err := store.ReadJSON(n.path, &f); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil || f.Version != transitionsVersion {
		return errTransitions
	}
	for _, r := range f.Transitions {
		if _, dup := n.records[r.Name]; dup || ValidName(r.Name) != nil {
			return errTransitions
		}
		n.records[r.Name] = r
	}
	return nil
}

// names returns the computers the coordinator manages: those with a
// transition, and those whose definition selects a VPN.
func (n *Networking) names() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range n.all() {
		seen[r.Name] = true
		out = append(out, r.Name)
	}
	for _, d := range n.service.Defs.List() {
		if d.Network.Kind == NetworkVPN && !seen[d.Name] {
			out = append(out, d.Name)
		}
	}
	slices.Sort(out)
	return out
}
func (n *Networking) all() []networkTransition {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]networkTransition, 0, len(n.records))
	for _, r := range n.records {
		out = append(out, r)
	}
	return out
}
func (n *Networking) record(name string) (networkTransition, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	r, ok := n.records[name]
	return r, ok
}

// transition is name's transition, or a fresh one that has applied nothing.
func (n *Networking) transition(name string) networkTransition {
	if r, ok := n.record(name); ok {
		return r
	}
	return networkTransition{Name: name}
}
func (n *Networking) write(next map[string]networkTransition) error {
	f := transitionsFile{Version: transitionsVersion, Transitions: make([]networkTransition, 0, len(next))}
	for _, r := range next {
		f.Transitions = append(f.Transitions, r)
	}
	slices.SortFunc(f.Transitions, func(a, b networkTransition) int {
		if a.Name < b.Name {
			return -1
		} else if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if err := store.WriteJSON(n.path, f); err != nil {
		return errors.New("could not persist computer network recovery state; retry to recover")
	}
	return nil
}
func (n *Networking) saveRecord(r networkTransition) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	next := make(map[string]networkTransition, len(n.records)+1)
	for k, v := range n.records {
		next[k] = v
	}
	next[r.Name] = r
	if err := n.write(next); err != nil {
		return err
	}
	n.records = next
	return nil
}
func (n *Networking) forget(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.records[name]; !ok {
		delete(n.statuses, name)
		return nil
	}
	next := make(map[string]networkTransition, len(n.records))
	for k, v := range n.records {
		if k != name {
			next[k] = v
		}
	}
	if err := n.write(next); err != nil {
		return err
	}
	n.records = next
	delete(n.statuses, name)
	return nil
}
func (n *Networking) setStatus(name string, status vpn.Status) {
	n.mu.Lock()
	defer n.mu.Unlock()
	old := n.statuses[name]
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
	n.statuses[name] = status
}
