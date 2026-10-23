package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

type settingsJSON struct {
	MemoryBytes int64  `json:"memoryBytes"`
	NanoCPUs    int64  `json:"nanoCpus"`
	Camera      string `json:"camera"` // the host device, "" for none
}

type statsJSON struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
	MemLimit   uint64  `json:"memLimit"`
}

type desktopJSON struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	State           string              `json:"state"`
	StartedAt       *time.Time          `json:"startedAt"`
	Created         *time.Time          `json:"created"`
	Job             *string             `json:"job"`
	Folder          string              `json:"folder"`
	Image           string              `json:"image"`
	ImageTitle      string              `json:"imageTitle"`
	ImageVersion    string              `json:"imageVersion"`
	ImageSecrets    bool                `json:"imageSecrets"`
	UpdateAvailable *string             `json:"updateAvailable"`
	Settings        settingsJSON        `json:"settings"`
	Access          []string            `json:"access"`
	Viewers         []string            `json:"viewers"`
	Mics            []string            `json:"mics"`
	Cams            []string            `json:"cams"`
	Webcam          bool                `json:"webcam"`
	Forwards        []forwardJSON       `json:"forwards"`
	Agents          []activity.Presence `json:"agents"`
	Tokens          int                 `json:"tokens"`
	Stats           *statsJSON          `json:"stats"`
}

func (s *Server) desktopJSON(ctx context.Context, c desktops.Desktop, a accounts.Account) desktopJSON {
	j := desktopJSON{
		ID: c.ID, Name: c.Name, State: c.State, Folder: c.Folder, Image: c.Image,
		ImageTitle: c.ImageLabels["org.opencontainers.image.title"], ImageVersion: c.ImageLabels["org.opencontainers.image.version"],
		ImageSecrets: c.ImageLabels[labelSecrets] == "true",
		Settings:     settingsJSON{c.Settings.MemoryBytes, c.Settings.NanoCPUs, c.Settings.Camera},
		Access:       []string{}, Viewers: s.viewers.names(c.Name),
		Mics: s.viewers.mics(c.Name), Cams: s.viewers.cams(c.Name), Webcam: c.Webcam(), Forwards: s.forwards.list(c.Name), Agents: s.activity.Present(c.Name),
	}
	if !c.Created.IsZero() {
		j.Created = &c.Created
	}
	if id, ok := s.jobs.forDesktop(c.Name); ok {
		j.Job = &id
	}
	if c.State == "running" {
		j.StartedAt = since(c.StartedAt)
		if st, ok := s.stats.latest(c.Name); ok {
			j.Stats = &st
		}
	}
	if a.Admin && c.ID != "" {
		j.Access = s.Accounts.WithAccess(c.ID)
		if j.Access == nil {
			j.Access = []string{}
		}
	}
	if c.ID != "" {
		j.Tokens = s.tokens.activeCount(c.ID)
		j.UpdateAvailable = s.images.updateFor(c)
	}
	return j
}

func (s *Server) handleListDesktops(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	list, err := s.Desktops.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []desktopJSON{}
	for _, c := range list {
		if a.HasAccess(c.ID) || (a.Admin && c.ID == "") {
			out = append(out, s.desktopJSON(r.Context(), c, a))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetDesktop(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.desktopJSON(r.Context(), c, a))
}

func (s *Server) handleFolder(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	bytes, complete := s.Desktops.FolderSize(c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"path": c.Folder, "bytes": bytes, "complete": complete})
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	info, _ := s.Eng.Info(r.Context())
	list, _ := s.Desktops.List(r.Context())
	var running, total int
	var memUsed uint64
	for _, c := range list {
		if !a.HasAccess(c.ID) {
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
		"cameras": s.hostCameras(list, a),
	})
}

// cameraJSON is one of the host's virtual cameras and the desktop that has it
// (named only to accounts that may see that desktop; taken either way).
type cameraJSON struct {
	Device  string `json:"device"`
	Taken   bool   `json:"taken"`
	Desktop string `json:"desktop,omitempty"`
}

// hostCameras is null when the engine isn't on this machine: its host's
// cameras can't be seen from here.
func (s *Server) hostCameras(list []desktops.Desktop, a accounts.Account) []cameraJSON {
	if !s.Desktops.Cfg.LocalEngine {
		return nil
	}
	out := []cameraJSON{}
	for _, dev := range s.Desktops.HostCameras() {
		cam := cameraJSON{Device: dev}
		for _, c := range list {
			if c.Settings.Camera == dev {
				cam.Taken = true
				if a.HasAccess(c.ID) {
					cam.Desktop = c.Name
				}
			}
		}
		out = append(out, cam)
	}
	return out
}

func (s *Server) handleValidateName(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	name := r.URL.Query().Get("name")
	res := map[string]any{"ok": true, "folderExists": false}
	if err := desktops.ValidNewName(name); err != nil {
		res["ok"], res["error"] = false, err.Error()
	} else if _, err := s.Desktops.Get(r.Context(), name); err == nil {
		res["ok"], res["error"] = false, "a desktop with that name already exists"
	} else {
		res["folderExists"] = s.Desktops.FolderExists(name)
		res["folder"] = filepath.Join(s.Desktops.Cfg.DataDir, name)
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCreateDesktop(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Image       string   `json:"image"`
		MemoryBytes int64    `json:"memoryBytes"`
		NanoCPUs    int64    `json:"nanoCpus"`
		Access      []string `json:"access"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := desktops.ValidNewName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.Desktops.Get(r.Context(), req.Name); err == nil {
		writeError(w, http.StatusConflict, "a desktop with that name already exists")
		return
	}
	if req.Image == "" {
		req.Image = s.images.defaultRef()
	}
	if req.MemoryBytes < 0 || req.NanoCPUs < 0 {
		writeError(w, http.StatusBadRequest, "limits can't be negative")
		return
	}
	for _, u := range req.Access {
		if _, ok := s.Accounts.Get(u); !ok {
			writeError(w, http.StatusBadRequest, "no account "+u)
			return
		}
	}
	if err := s.Desktops.Begin(req.Name, "creating"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	id := s.jobs.start(a.Username, req.Name, func(ctx context.Context, progress func(string, float64)) error {
		defer func() {
			s.Desktops.Done(req.Name)
			s.publishDesktop(ctx, req.Name)
		}()
		c, err := s.Desktops.Create(ctx, req.Name, desktops.Settings{Image: req.Image, MemoryBytes: req.MemoryBytes, NanoCPUs: req.NanoCPUs}, progress)
		if err != nil {
			s.Log.Warn("create failed", "desktop", req.Name, "err", err)
			return err
		}
		s.Log.Info("created", "desktop", req.Name, "by", a.Username)
		return s.Accounts.SetAccess(c.ID, req.Access)
	})
	// After the job starts, so the event carries its id.
	s.events.publish(event{kind: "desktop", data: desktops.Desktop{Name: req.Name, State: "creating"}})
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleUpdateDesktop(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.desktopFor(w, r, auth.Manage)
	if !ok {
		return
	}
	var req struct {
		Image          *string `json:"image"`
		MemoryBytes    *int64  `json:"memoryBytes"`
		NanoCPUs       *int64  `json:"nanoCpus"`
		Camera         *string `json:"camera"`
		ConfirmSecrets bool    `json:"confirmSecrets"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	set := c.Settings
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
	if req.Camera != nil {
		set.Camera = *req.Camera
		if set.Camera != "" && set.Camera != c.Settings.Camera {
			// Checked again when the container is made, against a race.
			if err := s.Desktops.CheckCamera(r.Context(), c.Name, set.Camera); err != nil {
				writeError(w, errorStatus(err), err.Error())
				return
			}
		}
	}
	if set.Image != c.Image && !s.secretsConfirmed(w, r, set.Image, []desktops.Desktop{c}, req.ConfirmSecrets) {
		return
	}
	s.startRecreate(w, a, c.Name, set, "upgrading")
}

// secretsConfirmed answers 409 unless confirmed is set when moving these
// desktops onto ref would let accounts use secrets: ref's image contains
// secrets (its starting home holds someone's sign-ins) and some of the
// desktops have accounts other than admins. The UI asks first, as it does
// when giving access to such a desktop.
func (s *Server) secretsConfirmed(w http.ResponseWriter, r *http.Request, ref string, cs []desktops.Desktop, confirmed bool) bool {
	if confirmed {
		return true
	}
	img, err := s.Eng.ImageInspect(r.Context(), ref)
	if err != nil || img.Labels[labelSecrets] != "true" {
		return true
	}
	var who []string
	for _, c := range cs {
		for _, u := range s.Accounts.WithAccess(c.ID) {
			if !slices.Contains(who, u) {
				who = append(who, u)
			}
		}
	}
	if len(who) == 0 {
		return true
	}
	writeError(w, http.StatusConflict, ref+" contains secrets and "+strings.Join(who, ", ")+
		" can use these desktops: confirm with confirmSecrets")
	return false
}

// startRecreate runs Recreate as a job (settings changes and upgrades).
func (s *Server) startRecreate(w http.ResponseWriter, a accounts.Account, name string, set desktops.Settings, state string) {
	if err := s.Desktops.Begin(name, state); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	id := s.jobs.start(a.Username, name, func(ctx context.Context, progress func(string, float64)) error {
		defer func() {
			s.Desktops.Done(name)
			s.publishDesktop(ctx, name)
		}()
		s.viewers.closeAll(name)
		_, err := s.Desktops.Recreate(ctx, name, set, progress)
		if err == nil {
			s.Log.Info("recreated", "desktop", name, "image", set.Image, "by", a.Username)
		}
		return err
	})
	s.publishDesktop(context.Background(), name)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleRemoveDesktop(w http.ResponseWriter, r *http.Request) {
	c, a, ok := s.desktopFor(w, r, auth.Manage)
	if !ok {
		return
	}
	if err := s.Desktops.Begin(c.Name, "removing"); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer s.Desktops.Done(c.Name)
	s.viewers.closeAll(c.Name)
	ctx, cancel := timeout(r, 5*time.Minute)
	defer cancel()
	if err := s.Desktops.Remove(ctx, c.Name, r.URL.Query().Get("files") == "1"); err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	_ = s.Accounts.Forget(func(id string) bool { return id == c.ID })
	s.tokens.forgetDesktop(c.ID)
	s.activity.ForgetDesktop(c.Name)
	s.Log.Info("removed", "desktop", c.Name, "files", r.URL.Query().Get("files") == "1", "by", a.Username)
	s.events.publish(event{kind: "removed", desktopID: c.ID, data: map[string]string{"name": c.Name}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "keys":
		s.handleKeys(w, r)
		return
	case "upgrade":
		c, a, ok := s.desktopFor(w, r, auth.Manage)
		if !ok {
			return
		}
		var req struct {
			ConfirmSecrets bool `json:"confirmSecrets"`
		}
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil {
				writeError(w, http.StatusBadRequest, "bad request")
				return
			}
		}
		if !s.secretsConfirmed(w, r, c.Image, []desktops.Desktop{c}, req.ConfirmSecrets) {
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
	c, a, ok := s.desktopFor(w, r, auth.Power)
	if !ok {
		return
	}
	if _, busy := s.Desktops.Busy()[c.Name]; busy {
		writeError(w, http.StatusConflict, desktops.ErrBusy.Error())
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	var err error
	switch action {
	case "start":
		err = s.Desktops.Start(ctx, c.Name)
	case "stop":
		s.viewers.closeAll(c.Name)
		err = s.Desktops.Stop(ctx, c.Name)
	case "restart":
		s.viewers.closeAll(c.Name)
		err = s.Desktops.Restart(ctx, c.Name)
	}
	if err != nil {
		writeError(w, errorStatus(err), err.Error())
		return
	}
	s.Log.Info(action, "desktop", c.Name, "by", a.Username)
	s.publishDesktop(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetAccess(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.ManageAccounts)
	if !ok {
		return
	}
	var req struct {
		Usernames []string `json:"usernames"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	for _, u := range req.Usernames {
		if _, ok := s.Accounts.Get(u); !ok {
			writeError(w, http.StatusBadRequest, "no account "+u)
			return
		}
	}
	if err := s.Accounts.SetAccess(c.ID, req.Usernames); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.publishDesktop(r.Context(), c.Name)
	w.WriteHeader(http.StatusNoContent)
}

// execOutput runs a command as the desktop's user and returns its stdout.
func (s *Server) execOutput(ctx context.Context, c desktops.Desktop, cmd ...string) ([]byte, error) {
	out, errOut, code, err := engine.Output(ctx, s.Eng, c.Container.ID, engine.ExecSpec{Cmd: cmd, User: "user", WorkDir: "/workspace", Env: execEnv})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &execError{code, strings.TrimSpace(string(errOut))}
	}
	return out, nil
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
	c, _, ok := s.desktopFor(w, r, auth.Power)
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
		writeError(w, http.StatusConflict, desktops.ErrStopped.Error())
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
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	titles := []string{"PID", "USER", "%CPU", "RSS", "ELAPSED", "COMMAND"}
	rows := [][]string{}
	if c.Running() {
		// ps inside the desktop: the engine's own "top" runs the host's ps,
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

// Tool is one mise tool installed in a desktop: by the desktop's user
// ("desktop") or in its image under /opt/mise ("image").
type Tool struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

const (
	maxToolsOutput = 1 << 20 // mise's output comes from the desktop: bound it
	maxTools       = 500
	maxToolText    = 100
	imageMiseDir   = "/opt/mise"
)

// handleTools lists the mise tools of a running desktop and where each one is
// installed. A desktop that is stopped, has no mise, or answers with anything
// odd has no tools to show: that is an empty list, not an error.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	tools := []Tool{}
	if c.Running() {
		ctx, cancel := timeout(r, 10*time.Second)
		defer cancel()
		if t, err := s.miseTools(ctx, c); err == nil {
			tools = t
		}
	}
	writeJSON(w, http.StatusOK, tools)
}

func (s *Server) miseTools(ctx context.Context, c desktops.Desktop) ([]Tool, error) {
	// The image's tools live in /opt/mise, apart from the desktop's own
	// MISE_DATA_DIR; mise lists them as well when told to look there.
	env := append(append([]string{}, execEnv...), "MISE_SHARED_INSTALL_DIRS="+imageMiseDir+"/installs")
	p, err := s.Eng.Exec(ctx, c.Container.ID, engine.ExecSpec{
		Cmd:  []string{"timeout", "-k", "1", "8", "mise", "ls", "--json"},
		User: "user", WorkDir: "/workspace", Env: env,
	})
	if err != nil {
		return nil, err
	}
	defer p.Close()
	go func() { // a hung exec must not outlive the request
		<-ctx.Done()
		p.Close()
	}()
	if p.Stderr != nil {
		go io.Copy(io.Discard, io.LimitReader(p.Stderr, maxToolsOutput)) //nolint:errcheck
	}
	out, err := io.ReadAll(io.LimitReader(p.Stdout, maxToolsOutput+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxToolsOutput {
		return nil, errors.New("mise output too large")
	}
	if code, err := p.Wait(); err != nil || code != 0 {
		return nil, errors.New("mise failed")
	}
	return parseMiseTools(out)
}

// parseMiseTools reads `mise ls --json`: an object of tool name to the list of
// its installed versions. Entries that don't look right are skipped.
func parseMiseTools(out []byte) ([]Tool, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	tools := []Tool{}
	for _, n := range names {
		var entries []json.RawMessage
		if json.Unmarshal(all[n], &entries) != nil {
			continue
		}
		for _, raw := range entries {
			var v struct {
				Version     string `json:"version"`
				InstallPath string `json:"install_path"`
				Installed   *bool  `json:"installed"`
			}
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			if v.Version == "" || v.InstallPath == "" || (v.Installed != nil && !*v.Installed) {
				continue
			}
			source := "desktop"
			if p := path.Clean(v.InstallPath); p == imageMiseDir || strings.HasPrefix(p, imageMiseDir+"/") {
				source = "image"
			}
			tools = append(tools, Tool{clip(n), clip(v.Version), source})
			if len(tools) >= maxTools {
				return tools, nil
			}
		}
	}
	return tools, nil
}

func clip(s string) string {
	if len(s) > maxToolText {
		return strings.ToValidUTF8(s[:maxToolText], "")
	}
	return s
}
