package environments

import (
	"context"
	"errors"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"slices"
	"sort"
	"time"
)

// ErrRecoveryRequired prevents edits from silently skipping a consumer: an
// environment's container uses the connection that its definition doesn't select.
var ErrRecoveryRequired = errors.New("an environment uses this VPN connection without selecting it; administrator repair required before editing connection")

// ConnectionReference is an environment using a connection. ID is its name.

type ConnectionReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type RolloutResult struct {
	ConnectionReference
	Error string `json:"error,omitempty"`
}

func (n *Networking) References(id string) []ConnectionReference {
	refs, _ := n.references(context.Background(), id)
	return refs
}
func (n *Networking) references(ctx context.Context, id string) ([]ConnectionReference, error) {
	out := []ConnectionReference{}
	seen := map[string]bool{}
	for _, d := range n.service.Defs.List() {
		if d.Network.Kind == NetworkVPN && d.Network.Connection == id {
			out = append(out, ConnectionReference{d.Name, d.Name})
			seen[d.Name] = true
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cs, err := n.service.Eng.List(ctx, n.service.ownFilter())
	if err != nil {
		return out, errors.New("environment references could not be inspected")
	}
	for _, c := range cs {
		name := c.Labels[LabelName]
		if n.service.owns(c.Labels, name) && c.Labels[vpn.LabelConnection] == id && !seen[name] {
			out = append(out, ConnectionReference{name, name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (n *Networking) DeleteConnection(id string) error {
	n.refs.Lock()
	defer n.refs.Unlock()
	refs, err := n.references(context.Background(), id)
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
	return n.checkUpdate(context.Background(), id, revision, ids)
}
func (n *Networking) checkUpdate(ctx context.Context, id string, revision uint64, ids []string) error {
	c, err := n.Connections.Get(id)
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return vpn.ErrConflict
	}
	want := []string{}
	refs, err := n.references(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if !n.selects(r.Name, id) {
			return ErrRecoveryRequired
		}
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
func (n *Networking) UpdateConnection(ctx context.Context, id string, revision uint64, ids []string, in vpn.Input, progress func(string, float64), commit func(func() error) error) (vpn.Connection, []RolloutResult, error) {
	// Acquire operation locks in name order before reference coordination, matching
	// assignment/removal lock ordering. Recheck after waiting for all operations.
	refs, err := n.references(ctx, id)
	if err != nil {
		return vpn.Connection{}, nil, err
	}
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
	if err := n.refs.LockContext(ctx); err != nil {
		return vpn.Connection{}, nil, err
	}
	if err := n.checkUpdate(ctx, id, revision, ids); err != nil {
		n.refs.Unlock()
		return vpn.Connection{}, nil, err
	}
	var conn vpn.Connection
	// The coordinator serializes cancellation with this durable commit. Even a
	// caller without a job must check cancellation after every admission wait.
	save := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		conn, err = n.Connections.Update(id, in)
		return err
	}
	if commit == nil {
		err = save()
	} else {
		err = commit(save)
	}
	if err == nil {
		ctx = context.WithoutCancel(ctx)
		for _, ref := range refs {
			if !n.selects(ref.Name, id) {
				err = ErrRecoveryRequired
				break
			}
			r := n.transition(ref.Name)
			r.ConnectionRevision = conn.Revision
			// A stopped environment without a container takes the new
			// revision when it is started.
			def, _ := n.service.Defs.Get(ref.Name)
			if _, ce := n.service.Eng.Inspect(ctx, containerName(ref.Name)); !errors.Is(ce, engine.ErrNotFound) || def.Power == PowerRunning {
				r.Pending = true
				r.Repair = false
				r.Error = ""
			}
			if e := n.saveRecord(r); e != nil {
				err = errors.New("connection saved; environment network recovery state could not be saved")
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
		if !n.selects(ref.Name, id) {
			result.Error = ErrRecoveryRequired.Error()
		} else {
			if _, err := n.recover(ctx, n.transition(ref.Name), true); err != nil {
				result.Error = "VPN revision could not be applied; selected policy retained"
			}
		}
		out = append(out, result)
	}
	return conn, out, nil
}

// selects reports whether name's definition selects connection id.
func (n *Networking) selects(name, id string) bool {
	d, ok := n.service.Defs.Get(name)
	return ok && d.Network.Kind == NetworkVPN && d.Network.Connection == id
}
