package server

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/storage"
	"github.com/zefhemel/deskyard/supervisor/internal/terminal"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
)

type settingsJSON struct {
	VPNConnectionID *string         `json:"vpnConnectionId"`
	LANExceptions   []vpn.Exception `json:"lanExceptions"`
	MemoryBytes     int64           `json:"memoryBytes"`
	NanoCPUs        int64           `json:"nanoCpus"`
	DockerAccess    bool            `json:"dockerAccess"`
	// Volumes is never null, like LANExceptions.
	Volumes []computers.VolumeGrant `json:"volumes"`
}

type statsJSON struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
	MemLimit   uint64  `json:"memLimit"`
}

type computerJSON struct {
	VPN             computers.VPNStatus `json:"vpn"`
	Terminal        bool                `json:"terminal"`
	Terminals       []terminal.Session  `json:"terminals"` // never null; empty unless running
	MemoryReclaim   bool                `json:"memoryReclaim"`
	WebDAV          bool                `json:"webdav"`
	Name            string              `json:"name"`
	State           string              `json:"state"`
	Revision        uint64              `json:"revision"`
	AppliedRevision uint64              `json:"appliedRevision"`
	Power           computers.Power     `json:"power"`
	Error           string              `json:"error"`
	Conflict        string              `json:"conflict"`
	StartedAt       *time.Time          `json:"startedAt"`
	Created         *time.Time          `json:"created"`
	Job             *string             `json:"job"`
	Folder          string              `json:"folder"`
	Image           string              `json:"image"`
	ImageTitle      string              `json:"imageTitle"`
	ImageVersion    string              `json:"imageVersion"`
	UpdateAvailable *string             `json:"updateAvailable"`
	Settings        settingsJSON        `json:"settings"`
	Level           auth.Level          `json:"level"`
	Access          []accessGrant       `json:"access"`
	Viewers         []string            `json:"viewers"`
	Observers       []string            `json:"observers"`
	Mics            []string            `json:"mics"`
	Forwards        []forwardJSON       `json:"forwards"`
	Agents          []activity.Presence `json:"agents"`
	Stats           *statsJSON          `json:"stats"`
}

func (s *Server) computerJSON(ctx context.Context, c computers.Computer, a accounts.Account) computerJSON {
	j := computerJSON{
		Terminal:      terminalCapable(c),
		Terminals:     s.terminalSessions.forComputer(c),
		MemoryReclaim: c.MemoryReclaim,
		WebDAV:        c.WebDAV,
		Name:          c.Name, State: c.State, Folder: s.Computers.Layout.ToHost(c.Folder), Image: c.Image,
		Revision: c.Revision, AppliedRevision: c.AppliedRevision, Power: c.Power, Error: c.Error, Conflict: c.Conflict,
		ImageTitle: c.ImageLabels["org.opencontainers.image.title"], ImageVersion: c.ImageLabels["org.opencontainers.image.version"],
		VPN:      c.VPN,
		Settings: settingsJSON{MemoryBytes: c.Settings.MemoryBytes, NanoCPUs: c.Settings.NanoCPUs, LANExceptions: c.Settings.LANExceptions, DockerAccess: c.Settings.DockerAccess, Volumes: c.Settings.Volumes},
		Level:    a.Level(c.Name),
		Access:   []accessGrant{}, Viewers: s.viewers.names(c.Name), Observers: s.viewers.observers(c.Name),
		Mics: s.viewers.mics(c.Name), Forwards: s.forwards.list(c.Name), Agents: s.activity.Present(c.Name),
	}
	if c.Settings.VPNConnectionID != "" {
		id := c.Settings.VPNConnectionID
		j.Settings.VPNConnectionID = &id
	}
	if j.Settings.LANExceptions == nil {
		j.Settings.LANExceptions = []vpn.Exception{}
	}
	if j.Settings.Volumes == nil {
		j.Settings.Volumes = []computers.VolumeGrant{}
	}
	if !c.Created.IsZero() {
		j.Created = &c.Created
	}
	if id, ok := s.jobs.forComputer(c.Name); ok {
		j.Job = &id
	}
	if c.State == "running" {
		j.StartedAt = since(c.StartedAt)
		if st, ok := s.stats.latest(c.Name); ok {
			j.Stats = &st
		}
	}
	if a.Admin && c.Revision != 0 { // defined, not still being created
		levels := s.accessOf(c.Name)
		for _, u := range slices.Sorted(maps.Keys(levels)) {
			j.Access = append(j.Access, accessGrant{Username: u, Level: levels[u]})
		}
	}
	if c.Revision != 0 {
		j.UpdateAvailable = s.images.updateFor(c)
	}
	if j.Level == auth.LevelObserve {
		j.observed()
	}
	return j
}

// observed blanks what an observer may not see: the computer's
// configuration and what full users do in it.
func (j *computerJSON) observed() {
	j.Folder, j.MemoryReclaim, j.WebDAV, j.Job = "", false, false, nil
	j.Settings = settingsJSON{LANExceptions: []vpn.Exception{}, Volumes: []computers.VolumeGrant{}}
	j.VPN = computers.VPNStatus{Status: vpn.Status{State: "disabled"}}
	j.Access, j.Terminals, j.Forwards, j.Agents = []accessGrant{}, []terminal.Session{}, []forwardJSON{}, []activity.Presence{}
}

// conflictJSON is a container of this supervisor's that it can't match to
// a definition: one claiming an undefined name ("unknown"), several claiming
// one ("duplicate"), or one claiming it under another name ("misnamed").
type conflictJSON struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Containers []string `json:"containers"`
	Message    string   `json:"message"`
}

// conflictsJSON are the latest reconcile pass's conflicts; only admins see
// them, since they name containers outside any grant.
func (s *Server) conflictsJSON(a accounts.Account) []conflictJSON {
	out := []conflictJSON{}
	if !a.Admin {
		return out
	}
	for _, c := range s.Computers.Conflicts() {
		out = append(out, conflictJSON{Name: c.Name, Kind: c.Reason, Containers: c.Containers, Message: c.String()})
	}
	return out
}

func (s *Server) handleListComputers(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	list, err := s.Computers.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []computerJSON{}
	for _, c := range list {
		if auth.Can(a, auth.Observe, c.Name) {
			out = append(out, s.computerJSON(r.Context(), c, a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out, "conflicts": s.conflictsJSON(a)})
}

func (s *Server) handleGetComputer(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.computerFor(w, r, auth.Observe)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.computerJSON(r.Context(), c, a))
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.View)
	if !ok {
		return
	}
	bytes, complete := s.Computers.FolderSize(c.Name)
	out := map[string]any{"path": s.Computers.Layout.ToHost(c.Folder), "bytes": bytes, "complete": complete}
	if free, size, ok := computers.DiskSpace(c.Folder); ok {
		out["diskFree"], out["diskSize"] = free, size
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	info, _ := s.Eng.Info(r.Context())
	list, _ := s.Computers.List(r.Context())
	var running, total int
	var memUsed uint64
	for _, c := range list {
		if !auth.Can(a, auth.Observe, c.Name) {
			continue
		}
		total++
		if c.State == "running" {
			running++
			if st, ok := s.stats.latest(c.Name); ok {
				memUsed += st.MemBytes
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cpus": info.CPUs, "memBytes": info.MemBytes, "running": running, "total": total, "memUsed": memUsed,
		"memoryReclaim": s.memoryReclaim,
	})
}

func (s *Server) handleValidateName(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	name := r.URL.Query().Get("name")
	res := map[string]any{"ok": true, "folderExists": false}
	if err := computers.ValidNewName(name); err != nil {
		res["ok"], res["error"] = false, err.Error()
	} else if _, err := s.Computers.Get(r.Context(), name); err == nil {
		res["ok"], res["error"] = false, "a computer with that name already exists"
	} else {
		res["folderExists"] = s.Computers.FolderExists(name)
		res["folder"] = s.Computers.Layout.ToHost(s.Computers.Layout.HomeOf(name))
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCreateComputer(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var req struct {
		VPNConnectionID optionalVPN             `json:"vpnConnectionId"`
		LANExceptions   []vpn.Exception         `json:"lanExceptions"`
		Name            string                  `json:"name"`
		Image           string                  `json:"image"`
		MemoryBytes     int64                   `json:"memoryBytes"`
		NanoCPUs        int64                   `json:"nanoCpus"`
		Access          []accessGrant           `json:"access"`
		Mounts          json.RawMessage         `json:"mounts"`
		DockerAccess    bool                    `json:"dockerAccess"`
		Volumes         []computers.VolumeGrant `json:"volumes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !refuseHostFolders(w, req.Mounts) || !s.checkVolumeGrants(w, nil, req.Volumes) {
		return
	}
	if err := computers.ValidNewName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Computers.Get(r.Context(), req.Name); err == nil {
		writeError(w, http.StatusConflict, "a computer with that name already exists")
		return
	}
	if req.Image == "" {
		req.Image = s.images.defaultRef()
	}
	if req.MemoryBytes < 0 || req.NanoCPUs < 0 {
		writeError(w, http.StatusBadRequest, "limits can't be negative")
		return
	}
	access, ok := s.accessLevels(w, req.Access, "it can only have the computer it was shared for")
	if !ok {
		return
	}
	set, err := s.Computers.ValidateNetworkSettings(computers.Settings{Image: req.Image, MemoryBytes: req.MemoryBytes, NanoCPUs: req.NanoCPUs, VPNConnectionID: req.VPNConnectionID.ID, LANExceptions: req.LANExceptions, DockerAccess: req.DockerAccess, Volumes: req.Volumes})
	if err != nil {
		vpnError(w, err)
		return
	}
	release, err := s.Computers.Reserve(req.Name, "creating")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	// Grants a removal of this name failed to purge must not
	// carry over to the new computer. Checked again under the
	// reservation: one created meanwhile keeps its own.
	if _, ok := s.Computers.Defs.Get(req.Name); ok {
		release()
		writeError(w, http.StatusConflict, "a computer with that name already exists")
		return
	}
	if err := s.forgetAccess(req.Name); err != nil {
		release()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.activity.ForgetDesktop(req.Name)
	id := s.jobs.start(a.Username, req.Name, func(ctx context.Context, progress func(string, float64)) error {
		defer func() {
			release()
			s.publishComputer(ctx, req.Name)
		}()
		_, err := s.Computers.Create(ctx, req.Name, set, progress)
		if err != nil {
			s.Log.Warn("create failed", "computer", req.Name, "err", err)
			// A failure that leaves the computer defined (one not ready
			// in time, or a VPN creation kept for recovery) can still come
			// up: it gets its requested access.
			if _, ok := s.Computers.Defs.Get(req.Name); ok {
				return errors.Join(err, s.Accounts.SetAccess(req.Name, access))
			}
			return err
		}
		s.Log.Info("created", "computer", req.Name, "by", a.Username)
		return s.Accounts.SetAccess(req.Name, access)
	})
	// After the job starts, so the event carries its id.
	s.publishComputer(context.Background(), req.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleUpdateComputer(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.computerFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		VPNConnectionID optionalVPN              `json:"vpnConnectionId"`
		LANExceptions   *[]vpn.Exception         `json:"lanExceptions"`
		Image           *string                  `json:"image"`
		MemoryBytes     *int64                   `json:"memoryBytes"`
		NanoCPUs        *int64                   `json:"nanoCpus"`
		Mounts          json.RawMessage          `json:"mounts"`
		DockerAccess    *bool                    `json:"dockerAccess"`
		Volumes         *[]computers.VolumeGrant `json:"volumes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	set := c.Settings
	if req.DockerAccess != nil {
		set.DockerAccess = *req.DockerAccess
	}
	if !refuseHostFolders(w, req.Mounts) {
		return
	}
	if req.Volumes != nil {
		set.Volumes = *req.Volumes
	}
	if !s.checkVolumeGrants(w, c.Settings.Volumes, set.Volumes) {
		return
	}
	if req.VPNConnectionID.Set {
		set.VPNConnectionID = req.VPNConnectionID.ID
	}
	if req.LANExceptions != nil {
		set.LANExceptions = *req.LANExceptions
	}
	var err error
	set, err = s.Computers.ValidateNetworkSettings(set)
	if err != nil {
		vpnError(w, err)
		return
	}
	if req.Image != nil {
		set.Image = *req.Image
	}
	if req.MemoryBytes != nil {
		set.MemoryBytes = *req.MemoryBytes
	}
	if req.NanoCPUs != nil {
		set.NanoCPUs = *req.NanoCPUs
	}
	if set.MemoryBytes < 0 || set.NanoCPUs < 0 {
		writeError(w, http.StatusBadRequest, "limits can't be negative")
		return
	}
	s.startRecreate(w, a, c.Name, set, "upgrading")
}

// startRecreate runs Recreate as a job (settings changes and upgrades).
func (s *Server) startRecreate(w http.ResponseWriter, a accounts.Account, name string, set computers.Settings, state string) {
	release, err := s.Computers.Reserve(name, state)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	id := s.jobs.start(a.Username, name, func(ctx context.Context, progress func(string, float64)) error {
		defer func() {
			release()
			s.publishComputer(ctx, name)
		}()
		s.viewers.closeAll(name)
		_, err := s.Computers.Recreate(ctx, name, set, progress)
		if err == nil {
			s.Log.Info("recreated", "computer", name, "image", set.Image, "by", a.Username)
		}
		return err
	})
	s.publishComputer(context.Background(), name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

// computerRemoved purges what the server keeps of a computer whose
// definition a removal deleted (Service.Removed): its grants (which closes
// the connections they allowed) and activity, and tells clients it is gone.
func (s *Server) computerRemoved(name string) error {
	if err := s.storage.ItemGone(storage.Item{Kind: storage.KindComputer, Name: name}); err != nil {
		s.Log.Error("dropping removed computer's backup policy", "computer", name, "err", err)
	}
	err := s.forgetAccess(name)
	if err != nil {
		s.Log.Error("persisting removed computer access cleanup", "computer", name, "err", err)
	}
	s.sweep() // the computer's shares end with it
	s.activity.ForgetDesktop(name)
	s.davLocks.forget(name)
	s.events.publish(event{kind: "removed", computer: name, data: map[string]string{"name": name}})
	return err
}

// forgetAccess purges the grants of the named computer.
func (s *Server) forgetAccess(name string) error {
	return s.Accounts.Forget(name)
}

func (s *Server) handleRemoveComputer(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.computerFor(w, r, auth.Manage)
	if !ok {
		return
	}
	release, err := s.Computers.Reserve(c.Name, "removing")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	s.viewers.closeAll(c.Name)
	ctx, cancel := removalContext(r)
	defer cancel()
	if err := s.Computers.Remove(ctx, c.Name, r.URL.Query().Get("files") == "1"); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("removed", "computer", c.Name, "files", r.URL.Query().Get("files") == "1", "by", a.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "keys":
		s.handleKeys(w, r)
		return
	case "upgrade":
		c, a, ok := s.computerFor(w, r, auth.Manage)
		if !ok {
			return
		}
		set := c.Settings
		set.Image = c.Image
		s.startRecreate(w, a, c.Name, set, "upgrading")
		return
	case "start", "stop", "restart":
	default:
		writeError(w, http.StatusNotFound, "no such action")
		return
	}
	c, a, ok := s.computerFor(w, r, auth.Power)
	if !ok {
		return
	}
	release, err := s.Computers.Reserve(c.Name, "")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	switch action {
	case "start":
		err = s.Computers.Start(ctx, c.Name)
	case "stop":
		s.viewers.closeAll(c.Name)
		err = s.Computers.Stop(ctx, c.Name)
	case "restart":
		s.viewers.closeAll(c.Name)
		err = s.Computers.Restart(ctx, c.Name)
	}
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info(action, "computer", c.Name, "by", a.Username)
	s.publishComputer(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// accessGrant is a computer's grant to an account.
type accessGrant struct {
	Username string     `json:"username"`
	Level    auth.Level `json:"level"`
}

// accessLevels checks a computer's grants to regular accounts and maps
// them by username, writing 400 for an unknown account, a share (shareMsg
// says why), an invalid level or an account listed twice.
func (s *Server) accessLevels(w http.ResponseWriter, grants []accessGrant, shareMsg string) (map[string]auth.Level, bool) {
	out := map[string]auth.Level{}
	for _, g := range grants {
		a, ok := s.Accounts.Get(g.Username)
		switch {
		case !ok:
			writeError(w, http.StatusBadRequest, "no account "+g.Username)
		case a.IsShare():
			writeError(w, http.StatusBadRequest, a.Username+" is a share; "+shareMsg)
		case !auth.ValidLevel(g.Level):
			writeError(w, http.StatusBadRequest, "the level of "+a.Username+" is full or observe")
		case out[a.Username] != auth.LevelNone:
			writeError(w, http.StatusBadRequest, a.Username+" is listed twice")
		default:
			out[a.Username] = g.Level
			continue
		}
		return nil, false
	}
	return out, true
}

func (s *Server) handleSetAccess(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.ManageAccounts)
	if !ok {
		return
	}
	var req struct {
		Grants []accessGrant `json:"grants"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	access, ok := s.accessLevels(w, req.Grants, "change it under Shares on the Access tab")
	if !ok {
		return
	}
	s.revoking.Lock()
	defer s.revoking.Unlock()
	before := map[string]accounts.Account{}
	for _, a := range s.Accounts.List() {
		before[a.Username] = a
	}
	if err := s.Accounts.SetAccess(c.Name, access); err != nil {
		// What it revokes ends although it isn't saved.
		for u, a := range before {
			if want := access[u]; !a.Admin && !a.IsShare() && len(accounts.Lowered(a.Grants, accounts.Capped(a.Grants, map[string]auth.Level{c.Name: want}))) > 0 {
				s.revokeUnsaved(unsavedRevocation{username: u, id: a.ID, caps: map[string]auth.Level{c.Name: want}})
			}
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Saved, these levels decide the computer's grants anew: what an
	// earlier change that couldn't be saved revoked of them no longer
	// applies.
	for u := range before {
		s.unsaved.forget(u, func(u *unsavedRevocation) { delete(u.caps, c.Name) })
	}
	s.events.refresh()
	s.publishComputer(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// accessOf maps the regular accounts (not admins, not shares) with access to
// the named computer to their level, as their access stands: with what
// couldn't be saved applied, so what the Access tab saves back keeps it.
func (s *Server) accessOf(name string) map[string]auth.Level {
	out := map[string]auth.Level{}
	for _, a := range s.accountStates() {
		if l := a.Level(name); !a.Admin && !a.IsShare() && l != auth.LevelNone {
			out[a.Username] = l
		}
	}
	return out
}

// execOutput runs a command as the computer's user and returns its stdout.
func (s *Server) execOutput(ctx context.Context, c computers.Computer, cmd ...string) ([]byte, error) {
	// Queries are small; allow image-sized binary output only for screenshots.
	limit := int64(1 << 20)
	if len(cmd) == 3 && cmd[0] == "deskyard" && cmd[1] == "screenshot" && cmd[2] == "-" {
		limit = 32 << 20
	}
	result, err := engine.Run(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: cmd, User: "user", WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: limit, StderrLimit: 4096})
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, &execError{result.Code, strings.TrimSpace(string(result.Stderr))}
	}
	return result.Stdout, nil
}

type execError struct {
	code   int
	stderr string
}

func (e *execError) Error() string {
	if e.stderr != "" {
		return e.stderr
	}
	return "command failed"
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.Power)
	if !ok {
		return
	}
	var req struct {
		Keys string `json:"keys"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Keys) == "" {
		writeError(w, http.StatusBadRequest, "keys required")
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, computers.ErrStopped.Error())
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	if _, err := s.execOutput(ctx, c, append([]string{"deskyard", "key"}, strings.Fields(req.Keys)...)...); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.computerFor(w, r, auth.View)
	if !ok {
		return
	}
	titles := []string{"PID", "USER", "%CPU", "RSS", "ELAPSED", "COMMAND"}
	rows := [][]string{}
	if c.Running() {
		// ps inside the computer: the engine's own "top" runs the host's ps,
		// which differs between engines.
		ctx, cancel := timeout(r, 10*time.Second)
		defer cancel()
		out, err := s.execOutput(ctx, c, "ps", "-eo", "pid=,user=,pcpu=,rss=,etime=,args=", "--sort=-pcpu")
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			if len(f) < 6 || f[5] == "ps" {
				continue
			}
			rows = append(rows, append(f[:5:5], strings.Join(f[5:], " ")))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": titles, "rows": rows})
}
