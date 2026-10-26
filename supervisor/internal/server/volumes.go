package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// volumeUserJSON is an environment whose definition grants a volume.
type volumeUserJSON struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
}

type volumeJSON struct {
	Name         string           `json:"name"`
	Created      time.Time        `json:"created"`
	Kind         string           `json:"kind"`
	Folder       string           `json:"folder"`
	Environments []volumeUserJSON `json:"environments"`
}

func (s *Server) volumeJSON(v environments.Volume) volumeJSON {
	j := volumeJSON{Name: v.Name, Created: v.Created, Kind: v.Kind, Folder: s.Environments.Layout.VolumeOf(v.Name), Environments: []volumeUserJSON{}}
	for _, u := range s.Environments.VolumeUsers(v.Name) {
		j.Environments = append(j.Environments, volumeUserJSON{Name: u.Environment, ReadOnly: u.ReadOnly})
	}
	return j
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	a, _ := s.account(r)
	if !requireAdmin(w, a) {
		return
	}
	out := []volumeJSON{}
	for _, v := range s.Environments.Vols.List() {
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
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := environments.ValidName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	v, adopted, err := s.Environments.CreateVolume(ctx, req.Name)
	switch {
	case errors.Is(err, environments.ErrExists):
		writeError(w, http.StatusConflict, "a volume with that name already exists")
		return
	case errors.Is(err, environments.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("created volume", "volume", v.Name, "adopted", adopted, "by", a.Username)
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
	ctx, cancel := timeout(r, 5*time.Minute)
	defer cancel()
	err := s.Environments.DeleteVolume(ctx, name, deleteFiles)
	var inUse *environments.VolumeInUseError
	switch {
	case errors.As(err, &inUse):
		// The environments shape of a VPN conflict, which the UI's ApiError reads.
		refs := make([]map[string]string, len(inUse.Environments))
		for i, env := range inUse.Environments {
			refs[i] = map[string]string{"id": env, "name": env}
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        fmt.Sprintf("%s is used by %s", name, strings.Join(inUse.Environments, ", ")),
			"environments": refs,
		})
		return
	case errors.Is(err, environments.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such volume")
		return
	case errors.Is(err, environments.ErrConflict):
		// A container still mounts it while its grant's removal is applied.
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		// Deleting the files failed; the volume itself may be gone already.
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Log.Info("deleted volume", "volume", name, "files", deleteFiles, "by", a.Username)
	w.WriteHeader(http.StatusNoContent)
}

// checkVolumeGrants validates an environment's grants, writing 400 unless
// every volume granted that cur doesn't already grant exists. A grant of a
// volume since deleted is reported in the environment's error instead, so
// it doesn't block other changes.
func (s *Server) checkVolumeGrants(w http.ResponseWriter, cur, grants []environments.VolumeGrant) bool {
	if err := environments.ValidateVolumeGrants(grants); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	for _, g := range grants {
		if slices.ContainsFunc(cur, func(c environments.VolumeGrant) bool { return c.Name == g.Name }) {
			continue
		}
		if _, ok := s.Environments.Vols.Get(g.Name); !ok {
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
	if _, ok := s.Environments.Vols.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such volume")
		return
	}
	bytes, complete := s.Environments.VolumeSize(name)
	out := map[string]any{"bytes": bytes, "complete": complete}
	if free, size, ok := environments.DiskSpace(s.Environments.Layout.VolumeOf(name)); ok {
		out["diskFree"], out["diskSize"] = free, size
	}
	writeJSON(w, http.StatusOK, out)
}
