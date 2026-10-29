package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/computers"
	"github.com/zefhemel/deskyard/supervisor/internal/storage"
)

// volumeUserJSON is a computer whose definition grants a volume.
type volumeUserJSON struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
}

type volumeJSON struct {
	Name      string           `json:"name"`
	Created   time.Time        `json:"created"`
	Kind      string           `json:"kind"`
	HostPath  string           `json:"hostPath,omitempty"`
	Folder    string           `json:"folder"`
	Computers []volumeUserJSON `json:"computers"`
}

func (s *Server) volumeJSON(v computers.Volume) volumeJSON {
	j := volumeJSON{Name: v.Name, Created: v.Created, Kind: v.Kind, HostPath: v.HostPath, Folder: s.Computers.Layout.ToHost(s.Computers.VolumeFolder(v)), Computers: []volumeUserJSON{}}
	for _, u := range s.Computers.VolumeUsers(v.Name) {
		j.Computers = append(j.Computers, volumeUserJSON{Name: u.Computer, ReadOnly: u.ReadOnly})
	}
	return j
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	out := []volumeJSON{}
	for _, v := range s.Computers.Vols.List() {
		out = append(out, s.volumeJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": out})
}

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	var req struct {
		Name     string `json:"name"`
		HostPath string `json:"hostPath"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := computers.ValidName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.HostPath != "" {
		if err := computers.ValidHostPath(s.Computers.Layout, req.HostPath); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	v, adopted, err := s.Computers.CreateVolume(ctx, req.Name, req.HostPath)
	switch {
	case errors.Is(err, computers.ErrExists):
		writeError(w, http.StatusConflict, "a volume with that name already exists")
		return
	case errors.Is(err, computers.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil && req.HostPath != "":
		// The folder check failed: the path is the administrator's to fix.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("created volume", "volume", v.Name, "kind", v.Kind, "adopted", adopted, "by", a.Username)
	writeJSON(w, http.StatusCreated, map[string]any{"volume": s.volumeJSON(v), "adopted": adopted})
}

func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	name := r.PathValue("name")
	deleteFiles := false
	if q := r.URL.Query().Get("deleteFiles"); q != "" {
		var err error
		if deleteFiles, err = strconv.ParseBool(q); err != nil {
			writeError(w, http.StatusBadRequest, "deleteFiles is true or false")
			return
		}
	}
	ctx, cancel := removalContext(r)
	defer cancel()
	err := s.Computers.DeleteVolume(ctx, name, deleteFiles)
	var inUse *computers.VolumeInUseError
	switch {
	case errors.As(err, &inUse):
		// The computers shape of a VPN conflict, which the UI's ApiError reads.
		refs := make([]map[string]string, len(inUse.Computers))
		for i, env := range inUse.Computers {
			refs[i] = map[string]string{"id": env, "name": env}
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        fmt.Sprintf("%s is used by %s", name, strings.Join(inUse.Computers, ", ")),
			"computers": refs,
		})
		return
	case errors.Is(err, computers.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such volume")
		return
	case errors.Is(err, computers.ErrConflict), errors.Is(err, storage.ErrBusy):
		// A container still mounts it while its grant's removal is applied,
		// or a backup or restore uses it.
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		// Deleting the files failed; the volume itself may be gone already.
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.storage.ItemGone(storage.Item{Kind: storage.KindVolume, Name: name}); err != nil {
		s.Log.Error("dropping deleted volume's backup policy", "volume", name, "err", err)
	}
	s.Log.Info("deleted volume", "volume", name, "files", deleteFiles, "by", a.Username)
	w.WriteHeader(http.StatusNoContent)
}

// refuseHostFolders writes 400 for a request that still has host folders
// ("mounts"), which host volumes replaced; it reports whether there were none.
func refuseHostFolders(w http.ResponseWriter, raw json.RawMessage) bool {
	var mounts []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &mounts) == nil && len(mounts) == 0 {
		return true
	}
	writeError(w, http.StatusBadRequest, "host folders were replaced by host volumes: create a host volume on the Storage page and grant it instead")
	return false
}

// checkVolumeGrants validates a computer's grants, writing 400 unless
// every volume granted that cur doesn't already grant exists. A grant of a
// volume since deleted is reported in the computer's error instead, so
// it doesn't block other changes.
func (s *Server) checkVolumeGrants(w http.ResponseWriter, cur, grants []computers.VolumeGrant) bool {
	if err := computers.ValidateVolumeGrants(grants); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	for _, g := range grants {
		if slices.ContainsFunc(cur, func(c computers.VolumeGrant) bool { return c.Name == g.Name }) {
			continue
		}
		if _, ok := s.Computers.Vols.Get(g.Name); !ok {
			writeError(w, http.StatusBadRequest, "no volume "+g.Name)
			return false
		}
	}
	return true
}

// handleVolumeUsage reports the size of a volume's files, which takes a walk
// of its folder, and the space on the disk holding it. Free and size are
// omitted when the disk can't be read.
func (s *Server) handleVolumeUsage(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	name := r.PathValue("name")
	if _, ok := s.Computers.Vols.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such volume")
		return
	}
	bytes, complete := s.Computers.VolumeSize(name)
	out := map[string]any{"bytes": bytes, "complete": complete}
	v, _ := s.Computers.Vols.Get(name)
	if free, size, ok := computers.DiskSpace(s.Computers.VolumeFolder(v)); ok {
		out["diskFree"], out["diskSize"] = free, size
	}
	writeJSON(w, http.StatusOK, out)
}
