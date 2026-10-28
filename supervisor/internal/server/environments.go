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
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
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
	Volumes []environments.VolumeGrant `json:"volumes"`
}

type statsJSON struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
	MemLimit   uint64  `json:"memLimit"`
}

type environmentJSON struct {
	VPN             environments.VPNStatus `json:"vpn"`
	Terminal        bool                   `json:"terminal"`
	Terminals       []terminal.Session     `json:"terminals"` // never null; empty unless running
	MemoryReclaim   bool                   `json:"memoryReclaim"`
	WebDAV          bool                   `json:"webdav"`
	Name            string                 `json:"name"`
	State           string                 `json:"state"`
	Revision        uint64                 `json:"revision"`
	AppliedRevision uint64                 `json:"appliedRevision"`
	Power           environments.Power     `json:"power"`
	Error           string                 `json:"error"`
	Conflict        string                 `json:"conflict"`
	StartedAt       *time.Time             `json:"startedAt"`
	Created         *time.Time             `json:"created"`
	Job             *string                `json:"job"`
	Folder          string                 `json:"folder"`
	Image           string                 `json:"image"`
	ImageTitle      string                 `json:"imageTitle"`
	ImageVersion    string                 `json:"imageVersion"`
	UpdateAvailable *string                `json:"updateAvailable"`
	Settings        settingsJSON           `json:"settings"`
	Level           auth.Level             `json:"level"`
	Access          []accessGrant          `json:"access"`
	Viewers         []string               `json:"viewers"`
	Observers       []string               `json:"observers"`
	Mics            []string               `json:"mics"`
	Forwards        []forwardJSON          `json:"forwards"`
	Agents          []activity.Presence    `json:"agents"`
	Stats           *statsJSON             `json:"stats"`
}

func (s *Server) environmentJSON(ctx context.Context, c environments.Environment, a accounts.Account) environmentJSON {
	j := environmentJSON{
		Terminal:      terminalCapable(c),
		Terminals:     s.terminalSessions.forEnvironment(c),
		MemoryReclaim: c.MemoryReclaim,
		WebDAV:        c.WebDAV,
		Name:          c.Name, State: c.State, Folder: s.Environments.Layout.ToHost(c.Folder), Image: c.Image,
		Revision: c.Revision, AppliedRevision: c.AppliedRevision, Power: c.Power, Error: c.Error, Conflict: c.Conflict,
		ImageTitle: c.ImageLabels["org.opencontainers.image.title"], ImageVersion: c.ImageLabels["org.opencontainers.image.version"],
		VPN:      c.VPN,
		Settings: settingsJSON{MemoryBytes: c.Settings.MemoryBytes, NanoCPUs: c.Settings.NanoCPUs, LANExceptions: c.Settings.LANExceptions, DockerAccess: c.Settings.DockerAccess, Volumes: c.Settings.Volumes},
		Level:    a.Level(c.Name),
		Access:   []accessGrant{}, Viewers: s.conns.names(c.Name), Observers: s.conns.observers(c.Name),
		Mics: s.conns.mics(c.Name), Forwards: s.forwards.list(c.Name), Agents: s.activity.Present(c.Name),
	}
	if c.Settings.VPNConnectionID != "" {
		id := c.Settings.VPNConnectionID
		j.Settings.VPNConnectionID = &id
	}
	if j.Settings.LANExceptions == nil {
		j.Settings.LANExceptions = []vpn.Exception{}
	}
	if j.Settings.Volumes == nil {
		j.Settings.Volumes = []environments.VolumeGrant{}
	}
	if !c.Created.IsZero() {
		j.Created = &c.Created
	}
	if id, ok := s.jobs.forEnvironment(c.Name); ok {
		j.Job = &id
	}
	if c.State == "running" {
		j.StartedAt = since(c.StartedAt)
		if st, ok := s.stats.latest(c.Name); ok {
			j.Stats = &st
		}
	}
	if a.Admin && c.Revision != 0 { // defined, not still being created
		levels := s.Accounts.WithAccess(c.Name)
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

// observed blanks what an observer may not see: the environment's
// configuration and what full users do in it.
func (j *environmentJSON) observed() {
	j.Folder, j.MemoryReclaim, j.WebDAV, j.Job = "", false, false, nil
	j.Settings = settingsJSON{LANExceptions: []vpn.Exception{}, Volumes: []environments.VolumeGrant{}}
	j.VPN = environments.VPNStatus{Status: vpn.Status{State: "disabled"}}
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
	for _, c := range s.Environments.Conflicts() {
		out = append(out, conflictJSON{Name: c.Name, Kind: c.Reason, Containers: c.Containers, Message: c.String()})
	}
	return out
}

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	list, err := s.Environments.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []environmentJSON{}
	for _, c := range list {
		if auth.Can(a, auth.Observe, c.Name) {
			out = append(out, s.environmentJSON(r.Context(), c, a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out, "conflicts": s.conflictsJSON(a)})
}

func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Observe)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.environmentJSON(r.Context(), c, a))
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	bytes, complete := s.Environments.FolderSize(c.Name)
	out := map[string]any{"path": s.Environments.Layout.ToHost(c.Folder), "bytes": bytes, "complete": complete}
	if free, size, ok := environments.DiskSpace(c.Folder); ok {
		out["diskFree"], out["diskSize"] = free, size
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	info, _ := s.Eng.Info(r.Context())
	list, _ := s.Environments.List(r.Context())
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
	name := r.URL.Query().Get("name")
	res := map[string]any{"ok": true, "folderExists": false}
	if err := environments.ValidNewName(name); err != nil {
		res["ok"], res["error"] = false, err.Error()
	} else if _, err := s.Environments.Get(r.Context(), name); err == nil {
		res["ok"], res["error"] = false, "an environment with that name already exists"
	} else {
		res["folderExists"] = s.Environments.FolderExists(name)
		res["folder"] = s.Environments.Layout.ToHost(s.Environments.Layout.HomeOf(name))
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	var req struct {
		VPNConnectionID optionalVPN                `json:"vpnConnectionId"`
		LANExceptions   []vpn.Exception            `json:"lanExceptions"`
		Name            string                     `json:"name"`
		Image           string                     `json:"image"`
		MemoryBytes     int64                      `json:"memoryBytes"`
		NanoCPUs        int64                      `json:"nanoCpus"`
		Access          []accessGrant              `json:"access"`
		Mounts          json.RawMessage            `json:"mounts"`
		DockerAccess    bool                       `json:"dockerAccess"`
		Volumes         []environments.VolumeGrant `json:"volumes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !refuseHostFolders(w, req.Mounts) || !s.checkVolumeGrants(w, nil, req.Volumes) {
		return
	}
	if err := environments.ValidNewName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Environments.Get(r.Context(), req.Name); err == nil {
		writeError(w, http.StatusConflict, "an environment with that name already exists")
		return
	}
	if req.Image == "" {
		req.Image = s.images.defaultRef()
	}
	if req.MemoryBytes < 0 || req.NanoCPUs < 0 {
		writeError(w, http.StatusBadRequest, "limits can't be negative")
		return
	}
	access, ok := s.accessLevels(w, req.Access, "it can only have the environment it was shared for")
	if !ok {
		return
	}
	set, err := s.Environments.ValidateNetworkSettings(environments.Settings{Image: req.Image, MemoryBytes: req.MemoryBytes, NanoCPUs: req.NanoCPUs, VPNConnectionID: req.VPNConnectionID.ID, LANExceptions: req.LANExceptions, DockerAccess: req.DockerAccess, Volumes: req.Volumes})
	if err != nil {
		vpnError(w, err)
		return
	}
	release, err := s.Environments.Reserve(req.Name, "creating")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	// Grants a removal of this name failed to purge must not
	// carry over to the new environment. Checked again under the
	// reservation: one created meanwhile keeps its own.
	if _, ok := s.Environments.Defs.Get(req.Name); ok {
		release()
		writeError(w, http.StatusConflict, "an environment with that name already exists")
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
			s.publishEnvironment(ctx, req.Name)
		}()
		_, err := s.Environments.Create(ctx, req.Name, set, progress)
		if err != nil {
			s.Log.Warn("create failed", "environment", req.Name, "err", err)
			// A failure that leaves the environment defined (one not ready
			// in time, or a VPN creation kept for recovery) can still come
			// up: it gets its requested access.
			if _, ok := s.Environments.Defs.Get(req.Name); ok {
				return errors.Join(err, s.Accounts.SetAccess(req.Name, access))
			}
			return err
		}
		s.Log.Info("created", "environment", req.Name, "by", a.Username)
		return s.Accounts.SetAccess(req.Name, access)
	})
	// After the job starts, so the event carries its id.
	s.publishEnvironment(context.Background(), req.Name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		VPNConnectionID optionalVPN                 `json:"vpnConnectionId"`
		LANExceptions   *[]vpn.Exception            `json:"lanExceptions"`
		Image           *string                     `json:"image"`
		MemoryBytes     *int64                      `json:"memoryBytes"`
		NanoCPUs        *int64                      `json:"nanoCpus"`
		Mounts          json.RawMessage             `json:"mounts"`
		DockerAccess    *bool                       `json:"dockerAccess"`
		Volumes         *[]environments.VolumeGrant `json:"volumes"`
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
	set, err = s.Environments.ValidateNetworkSettings(set)
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
func (s *Server) startRecreate(w http.ResponseWriter, a accounts.Account, name string, set environments.Settings, state string) {
	release, err := s.Environments.Reserve(name, state)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	id := s.jobs.start(a.Username, name, func(ctx context.Context, progress func(string, float64)) error {
		defer func() {
			release()
			s.publishEnvironment(ctx, name)
		}()
		s.conns.closeAll(name)
		_, err := s.Environments.Recreate(ctx, name, set, progress)
		if err == nil {
			s.Log.Info("recreated", "environment", name, "image", set.Image, "by", a.Username)
		}
		return err
	})
	s.publishEnvironment(context.Background(), name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

// environmentRemoved purges what the server keeps of an environment whose
// definition a removal deleted (Service.Removed): its grants (which closes
// the connections they allowed) and activity, and tells clients it is gone.
func (s *Server) environmentRemoved(name string) error {
	if err := s.storage.ItemGone(storage.Item{Kind: storage.KindEnvironment, Name: name}); err != nil {
		s.Log.Error("dropping removed environment's backup policy", "environment", name, "err", err)
	}
	err := s.forgetAccess(name)
	if err != nil {
		s.Log.Error("persisting removed environment access cleanup", "environment", name, "err", err)
	}
	s.sweep() // the environment's shares end with it
	s.activity.ForgetDesktop(name)
	s.davLocks.forget(name)
	s.events.publish(event{kind: "removed", environment: name, data: map[string]string{"name": name}})
	return err
}

// forgetAccess purges the grants of the named environment.
func (s *Server) forgetAccess(name string) error {
	return s.Accounts.Forget(name)
}

func (s *Server) handleRemoveEnvironment(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.environmentFor(w, r, auth.Manage)
	if !ok {
		return
	}
	release, err := s.Environments.Reserve(c.Name, "removing")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	s.conns.closeAll(c.Name)
	ctx, cancel := removalContext(r)
	defer cancel()
	if err := s.Environments.Remove(ctx, c.Name, r.URL.Query().Get("files") == "1"); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info("removed", "environment", c.Name, "files", r.URL.Query().Get("files") == "1", "by", a.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "keys":
		s.handleKeys(w, r)
		return
	case "upgrade":
		c, a, ok := s.environmentFor(w, r, auth.Manage)
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
	c, a, ok := s.environmentFor(w, r, auth.Power)
	if !ok {
		return
	}
	release, err := s.Environments.Reserve(c.Name, "")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer release()
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	switch action {
	case "start":
		err = s.Environments.Start(ctx, c.Name)
	case "stop":
		s.conns.closeAll(c.Name)
		err = s.Environments.Stop(ctx, c.Name)
	case "restart":
		s.conns.closeAll(c.Name)
		err = s.Environments.Restart(ctx, c.Name)
	}
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info(action, "environment", c.Name, "by", a.Username)
	s.publishEnvironment(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// accessGrant is an environment's grant to an account.
type accessGrant struct {
	Username string     `json:"username"`
	Level    auth.Level `json:"level"`
}

// accessLevels checks an environment's grants to regular accounts and maps
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
	c, _, ok := s.environmentFor(w, r, auth.ManageAccounts)
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
	// Unsaved, what it revokes holds all the same (accounts.Store).
	if err := s.Accounts.SetAccess(c.Name, access); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.refresh()
	s.publishEnvironment(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// execOutput runs a command as the environment's user and returns its stdout.
func (s *Server) execOutput(ctx context.Context, c environments.Environment, cmd ...string) ([]byte, error) {
	// Queries are small; allow image-sized binary output only for screenshots.
	limit := int64(1 << 20)
	if len(cmd) == 3 && cmd[0] == "deskyard" && cmd[1] == "screenshot" && cmd[2] == "-" {
		limit = 32 << 20
	}
	result, err := engine.Run(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: cmd, User: "user", DesktopSession: true, WorkDir: "/workspace", Env: execEnv}, engine.RunOptions{StdoutLimit: limit, StderrLimit: 4096})
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
	c, _, ok := s.environmentFor(w, r, auth.Power)
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
		writeError(w, http.StatusConflict, environments.ErrStopped.Error())
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
	c, _, ok := s.environmentFor(w, r, auth.View)
	if !ok {
		return
	}
	titles := []string{"PID", "USER", "%CPU", "RSS", "ELAPSED", "COMMAND"}
	rows := [][]string{}
	if c.Running() {
		// ps inside the environment: the engine's own "top" runs the host's ps,
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
