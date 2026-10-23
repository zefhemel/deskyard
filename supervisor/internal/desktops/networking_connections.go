package desktops

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"slices"
	"sort"
	"time"
)

type ConnectionReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type RolloutResult struct {
	ConnectionReference
	Error string `json:"error,omitempty"`
}

func (n *Networking) References(id string) []ConnectionReference {
	refs, _ := n.references(id)
	return refs
}
func (n *Networking) references(id string) ([]ConnectionReference, error) {
	out := []ConnectionReference{}
	records := n.all()
	for _, r := range records {
		if r.Settings.VPNConnectionID == id {
			out = append(out, ConnectionReference{r.ID, r.Name})
		}
	}
	seen := map[string]bool{}
	for _, r := range records {
		seen[r.ID] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs, err := n.service.Eng.List(ctx, map[string]string{LabelManaged: "true"})
	if err != nil {
		return out, errors.New("desktop references could not be inspected")
	}
	for _, c := range cs {
		if IsDesktop(c.Labels) && c.Labels[vpn.LabelConnection] == id && !seen[c.Labels[LabelID]] {
			out = append(out, ConnectionReference{c.Labels[LabelID], c.Labels[LabelName]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (n *Networking) DeleteConnection(id string) error {
	n.refs.Lock()
	defer n.refs.Unlock()
	refs, err := n.references(id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		return vpn.ErrConflict
	}
	return n.Connections.Delete(id)
}

// CheckUpdate verifies the preview still matches the revision and reference set.
func (n *Networking) CheckUpdate(id string, revision uint64, ids []string) error {
	c, err := n.Connections.Get(id)
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return vpn.ErrConflict
	}
	want := []string{}
	refs, err := n.references(id)
	if err != nil {
		return err
	}
	for _, r := range refs {
		want = append(want, r.ID)
	}
	got := slices.Clone(ids)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(want, got) {
		return vpn.ErrConflict
	}
	return nil
}
func (n *Networking) UpdateConnection(ctx context.Context, id string, revision uint64, ids []string, in vpn.Input, progress func(string, float64)) (vpn.Connection, []RolloutResult, error) {
	// Acquire operation locks in name order before reference coordination, matching
	// assignment/removal lock ordering. Recheck after waiting for all operations.
	refs := n.References(id)
	unlocks := []func(){}
	defer func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}()
	for _, r := range refs {
		unlock, err := n.lock(ctx, r.Name)
		if err != nil {
			return vpn.Connection{}, nil, err
		}
		unlocks = append(unlocks, unlock)
	}
	n.refs.Lock()
	if err := n.CheckUpdate(id, revision, ids); err != nil {
		n.refs.Unlock()
		return vpn.Connection{}, nil, err
	}
	conn, err := n.Connections.Update(id, in)
	if err == nil {
		for _, ref := range refs {
			r, ok := n.record(ref.Name)
			if !ok {
				continue
			}
			r.Revision = conn.Revision
			r.Pending = true
			r.Error = ""
			if e := n.saveRecord(r); e != nil {
				err = errors.New("connection saved; desktop network recovery state could not be saved")
				break
			}
		}
	}
	n.refs.Unlock()
	if err != nil {
		return conn, nil, err
	}
	out := []RolloutResult{}
	for i, ref := range refs {
		progress("Applying VPN revision to "+ref.Name, float64(i)/float64(len(refs)))
		result := RolloutResult{ConnectionReference: ref}
		r, ok := n.record(ref.Name)
		if ok {
			if err := n.recover(ctx, r); err != nil {
				result.Error = "VPN revision could not be applied; selected policy retained"
			}
		}
		out = append(out, result)
	}
	return conn, out, nil
}
