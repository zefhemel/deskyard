package environments

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

// ReconcileInterval is how often Run reconciles without an engine event.
const ReconcileInterval = 30 * time.Second

// reconcileTimeout bounds how long a pass spends bringing one environment up
// (a container built from a pinned image that has to be pulled again), so a
// stalled pull doesn't stall the others. A build cut short is a failed build.
var reconcileTimeout = 10 * time.Minute

// Why containers of this instance aren't matched to an environment.
const (
	ConflictUnknown   = "unknown"   // no definition has the name it claims
	ConflictDuplicate = "duplicate" // several containers claim one environment
	ConflictMisnamed  = "misnamed"  // one container claims it under another name
)

// Conflict is a visible mismatch between this instance's containers and the
// definitions. The supervisor neither adopts nor removes its containers.
type Conflict struct {
	Name       string   `json:"name"`       // the environment name claimed
	Reason     string   `json:"reason"`     // ConflictUnknown, ConflictDuplicate or ConflictMisnamed
	Containers []string `json:"containers"` // the claiming containers' names, sorted
}

func (c Conflict) String() string {
	switch c.Reason {
	case ConflictDuplicate:
		return fmt.Sprintf("several containers claim this environment (%s); remove all but %s", strings.Join(c.Containers, ", "), containerName(c.Name))
	case ConflictMisnamed:
		return fmt.Sprintf("%s claims this environment under another name; rename it to %s or remove it", strings.Join(c.Containers, ", "), containerName(c.Name))
	}
	return fmt.Sprintf("%s claims environment %s, which isn't defined", strings.Join(c.Containers, ", "), c.Name)
}

// Report is what a reconcile pass found: conflicts, the environments it
// could not bring to their desired power, and the pinned images pulled again
// since the previous pass.
type Report struct {
	Conflicts []Conflict
	Errors    []error
	Repins    []Repin
}

// Repin is a pinned image found gone from the engine and pulled again by its
// reference, recorded as a new revision.
type Repin struct {
	Name     string `json:"name"`
	Ref      string `json:"ref"`
	From     string `json:"from"`
	To       string `json:"to"`
	Revision uint64 `json:"revision"`
}

func (r Report) Err() error { return errors.Join(r.Errors...) }

// Reconcile runs at startup. In a container, it reconnects the supervisor to
// each of its own environments' networks (e.g. after the supervisor's
// container was recreated). Interrupted replacements and network transitions
// are recovered, and an environment whose camera has gone from the host (a
// reboot without the module) is recreated without it, which is logged, and
// started when its power is running. Conflicts are found first, so a duplicate blocks these
// steps as any other. Then a first ReconcileOnce pass runs.
func (s *Service) Reconcile(ctx context.Context) error {
	var errs []error
	if _, err := s.findConflicts(ctx); err != nil {
		errs = append(errs, err)
	}
	if s.Cfg.InContainer {
		nets, err := s.Eng.Networks(ctx, s.ownFilter())
		if err != nil {
			errs = append(errs, err)
		}
		for _, n := range nets {
			if name := n.Labels[LabelName]; !s.owns(n.Labels, name) || n.Name != containerName(name) {
				continue // another supervisor's, or not an environment's
			}
			if err := s.Eng.ConnectNetwork(ctx, n.Name, s.Cfg.SelfID, nil); err != nil && !isAlreadyConnected(err) {
				s.Log.Warn("reconnecting to network", "network", n.Name, "err", err)
			}
		}
	}
	// Replacements are recovered first: one under a definition that now
	// selects a VPN is discarded, and the networking coordinator applies it.
	if err := s.recoverReplacements(ctx); err != nil {
		errs = append(errs, err)
	}
	if s.Networking != nil {
		if err := s.Networking.Reconcile(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range list {
		if c.Container.ID == "" || c.Conflict != "" {
			continue // created when started; a conflict waits to be resolved
		}
		if c.Settings.VPNConnectionID != "" && s.Networking == nil {
			_ = s.Eng.UpdateRestartPolicy(ctx, c.Container.ID, "no")
			_ = s.Eng.Stop(ctx, c.Container.ID, stopTimeout)
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, errVPNUnavailable))
			continue
		}
		if s.Networking != nil && s.Networking.selected(c.Name) {
			continue
		}
		if c.Settings.Camera == "" || !s.cameraGone(c.Settings.Camera) {
			continue
		}
		if err := s.withoutCamera(ctx, c, c.Power == PowerRunning); err != nil {
			s.Log.Warn("recreating an environment without its gone camera", "environment", c.Name, "err", err)
			errs = append(errs, err)
		}
	}
	if err := s.ReconcileOnce(ctx).Err(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Run reconciles every ReconcileInterval and whenever trigger fires (the
// server fires it on engine die, destroy and create events), until ctx ends.
func (s *Service) Run(ctx context.Context, trigger <-chan struct{}) {
	ticker := time.NewTicker(ReconcileInterval)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-trigger:
		}
		// Errors that persist are logged once.
		if err := s.ReconcileOnce(ctx).Err(); err != nil && err.Error() != last {
			s.Log.Warn("reconciling environments", "err", err)
			last = err.Error()
		} else if err == nil {
			last = ""
		}
	}
}

// ReconcileOnce matches this instance's containers to the definitions by
// their name label. Containers claiming an undefined (or invalid) name,
// several claiming one, and one claiming it under another container name are
// reported as conflicts and left alone; a duplicate or misnamed one blocks
// its environment's lifecycle until resolved. Another instance's containers
// are not considered. A definition whose home folder is missing is an error,
// whatever its power; the folder is never made.
//
// An environment that should run is brought up as a start would: without a
// container one is created from its definition, an exited one is started.
// An existing container is only started, never replaced: a definition ahead
// of it (a failed change) is applied by a start, restart or edit. Environments
// that are busy, recovering a replacement or conflicted are left, as is one
// without a container whose build failed; VPN environments are the
// networking coordinator's.
func (s *Service) ReconcileOnce(ctx context.Context) Report {
	var rep Report
	claims, err := s.findConflicts(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err)
		return rep
	}
	rep.Conflicts = s.Conflicts()
	busy := s.Busy()
	for _, def := range s.Defs.List() {
		if _, isBusy := busy[def.Name]; isBusy {
			continue
		}
		err := s.checkHome(def.Name)
		if err == nil {
			err = s.reconcileOne(ctx, def, claims[def.Name])
		}
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Errorf("%s: %w", def.Name, err))
		}
	}
	s.mu.Lock()
	rep.Repins, s.repins = s.repins, nil
	s.mu.Unlock()
	if len(rep.Repins) > 0 && s.Repinned != nil {
		s.Repinned(rep.Repins)
	}
	return rep
}

// findConflicts records the conflicts among this instance's containers and
// returns their claims. Busy environments (being created, or replacing their
// container) are not judged again: they keep the conflict they had, as do
// those that were busy when the containers were listed. A claim of an
// undefined name is looked at again, so a removal that finished meanwhile
// isn't reported.
func (s *Service) findConflicts(ctx context.Context) (map[string][]engine.Container, error) {
	busy := s.Busy()
	claims, err := s.claims(ctx)
	if err != nil {
		return nil, err
	}
	for name, state := range s.Busy() {
		busy[name] = state
	}
	var conflicts []Conflict
	for _, c := range s.Conflicts() {
		if _, isBusy := busy[c.Name]; isBusy {
			conflicts = append(conflicts, c)
		}
	}
	for name, cs := range claims {
		if _, isBusy := busy[name]; isBusy {
			continue
		}
		names := make([]string, 0, len(cs))
		for _, c := range cs {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		_, defined := s.Defs.Get(name)
		switch {
		case !defined:
			if !s.stillListed(ctx, cs) {
				continue
			}
			conflicts = append(conflicts, Conflict{Name: name, Reason: ConflictUnknown, Containers: names})
		case len(cs) > 1:
			conflicts = append(conflicts, Conflict{Name: name, Reason: ConflictDuplicate, Containers: names})
		case cs[0].Name != containerName(name):
			conflicts = append(conflicts, Conflict{Name: name, Reason: ConflictMisnamed, Containers: names})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Name < conflicts[j].Name })
	s.setConflicts(conflicts)
	return claims, nil
}

// stillListed reports whether any of cs is still on the engine.
func (s *Service) stillListed(ctx context.Context, cs []engine.Container) bool {
	return slices.ContainsFunc(cs, func(c engine.Container) bool {
		_, err := s.Eng.Inspect(ctx, c.ID)
		return !errors.Is(err, engine.ErrNotFound)
	})
}

// claims groups this instance's environment containers by the name they
// claim; an invalid name is kept as it is, to be reported. A parked container
// of a replacement being recovered is the replacement's, not a claim.
func (s *Service) claims(ctx context.Context) (map[string][]engine.Container, error) {
	cs, err := s.Eng.List(ctx, s.ownFilter())
	if err != nil {
		return nil, err
	}
	out := map[string][]engine.Container{}
	for _, c := range cs {
		name := c.Labels[LabelName]
		if !s.owns(c.Labels, name) {
			continue
		}
		if ValidName(name) == nil && c.Name == parkedName(name) && s.noPendingReplacement(name) != nil {
			continue
		}
		out[name] = append(out[name], c)
	}
	return out, nil
}

// reconcileOne brings def's environment to its desired power; cs are the
// containers that claim it.
func (s *Service) reconcileOne(ctx context.Context, def Definition, cs []engine.Container) error {
	name := def.Name
	switch {
	case len(cs) > 1, def.Power != PowerRunning, def.Network.Kind == NetworkVPN:
		return nil
	case s.Networking != nil && s.Networking.selected(name):
		return nil
	case len(cs) == 0 && s.hasFailedBuild(name):
		return nil // a failed build waits for a start or edit
	case len(cs) == 1 && (cs[0].Name != containerName(name) || cs[0].Running()):
		return nil // running, or claimed under another name: nothing to start
	}
	release, err := s.Reserve(name, "starting")
	if err != nil {
		return nil // busy, or recovering a replacement
	}
	defer release()
	// Looked at again under the reservation.
	env, err := s.Get(ctx, name)
	if err != nil || env.Conflict != "" || env.Power != PowerRunning || env.Container.Running() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, reconcileTimeout)
	defer cancel()
	if env.Container.ID == "" {
		if s.hasFailedBuild(name) {
			return nil
		}
		s.Log.Info("creating the container of an environment that should run", "environment", name)
		return s.power(ctx, name, "start")
	}
	return s.power(ctx, name, "resume")
}

// setConflicts records the latest conflicts; a change is logged and
// announced with ConflictsChanged.
func (s *Service) setConflicts(cs []Conflict) {
	s.mu.Lock()
	changed := !reflect.DeepEqual(s.conflicts, cs)
	prev := s.conflicts
	s.conflicts = cs
	s.mu.Unlock()
	if !changed {
		return
	}
	for _, c := range cs {
		if !slices.ContainsFunc(prev, func(p Conflict) bool { return reflect.DeepEqual(p, c) }) {
			s.Log.Warn("container conflict", "environment", c.Name, "reason", c.Reason, "containers", strings.Join(c.Containers, ","))
		}
	}
	if s.ConflictsChanged != nil {
		s.ConflictsChanged()
	}
}

// Conflicts are those the latest reconcile pass found.
func (s *Service) Conflicts() []Conflict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.conflicts)
}

// duplicate is why name's lifecycle is blocked by several containers
// claiming it, or one claiming it under another name, or "".
func (s *Service) duplicate(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conflicts {
		if c.Name == name && (c.Reason == ConflictDuplicate || c.Reason == ConflictMisnamed) {
			return c.String()
		}
	}
	return ""
}

// blocked refuses a lifecycle operation on a duplicated or misnamed environment.
func (s *Service) blocked(name string) error {
	if d := s.duplicate(name); d != "" {
		return fmt.Errorf("%w: %s", ErrConflict, d)
	}
	return nil
}

// repin makes sure name's pinned image is on the engine. One that has gone
// is pulled again by its reference; the image it resolves to is recorded as
// a new revision, which is logged and reported by the next reconcile pass.
func (s *Service) repin(ctx context.Context, name string) (Definition, error) {
	def, ok := s.Defs.Get(name)
	if !ok {
		return Definition{}, ErrNotFound
	}
	if def.Image.ID == "" {
		return def, nil
	}
	if _, err := s.Eng.ImageInspect(ctx, def.Image.ID); err == nil || !errors.Is(err, engine.ErrNotFound) {
		return def, err
	}
	img, err := s.EnsureImage(ctx, def.Image.Ref, func(string, float64) {})
	if err != nil {
		return Definition{}, fmt.Errorf("the pinned image %s is gone: %w", def.Image.ID, err)
	}
	gone := def.Image.ID
	def, err = s.Defs.Update(name, func(d *Definition) error {
		if d.Image.ID == gone && img.ID != gone {
			d.Image.ID = img.ID
			d.Revision++
		}
		return nil
	})
	if err != nil {
		return Definition{}, err
	}
	if def.Image.ID != gone {
		s.mu.Lock()
		s.repins = append(s.repins, Repin{Name: name, Ref: def.Image.Ref, From: gone, To: def.Image.ID, Revision: def.Revision})
		s.mu.Unlock()
	}
	s.Log.Warn("pinned image gone from the engine; pulled its reference as a new revision",
		"environment", name, "ref", def.Image.Ref, "was", gone, "now", def.Image.ID, "revision", def.Revision)
	return def, nil
}

// CheckLegacyContainers refuses (config.ErrLegacyLayout) Deskyard containers
// from before environment definitions, those without an instance label, that
// mount this data folder. They are neither adopted nor removed. Helpers and
// VPN candidate tests are ignored. Only Deskyard-labelled containers are
// listed, not every container on the engine.
func (s *Service) CheckLegacyContainers(ctx context.Context) error {
	var cs []engine.Container
	seen := map[string]bool{}
	for _, filter := range []map[string]string{{LabelManaged: "true"}, {vpn.LabelRole: ""}} {
		found, err := s.Eng.List(ctx, filter)
		if err != nil {
			return err
		}
		for _, c := range found {
			if !seen[c.ID] {
				seen[c.ID] = true
				cs = append(cs, c)
			}
		}
	}
	dirs := []string{filepath.Clean(s.Layout.Data)}
	if real, err := filepath.EvalSymlinks(s.Layout.Data); err == nil && real != dirs[0] {
		dirs = append(dirs, real)
	}
	var old []string
	for _, c := range cs {
		l := c.Labels
		deskyard := l[LabelManaged] == "true" || l[vpn.LabelRole] != ""
		if !deskyard || l[LabelInstance] != "" || l[vpn.LabelRole] == "test" || l["deskyard.helper"] != "" {
			continue
		}
		if slices.ContainsFunc(c.Mounts, func(m engine.Mount) bool {
			return slices.ContainsFunc(dirs, func(dir string) bool { return within(m.Source, dir) })
		}) {
			old = append(old, c.Name)
		}
	}
	if len(old) == 0 {
		return nil
	}
	sort.Strings(old)
	return fmt.Errorf("%w: containers from before environment definitions use %s: %s; remove them (docker rm -f %s), or choose another DESKYARD_DATA_DIR (they are not migrated)",
		config.ErrLegacyLayout, s.Layout.Data, strings.Join(old, ", "), strings.Join(old, " "))
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
