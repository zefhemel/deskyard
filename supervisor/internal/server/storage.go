package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/storage"
)

// Storage remotes and backups (admin): the storage package does the work;
// these handlers check, translate and run long operations as jobs.

// storageError writes err with the status it means. Conflicts carry a code
// the UI acts on.
func storageError(w http.ResponseWriter, err error) {
	code := ""
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, storage.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, storage.ErrNeedsKey):
		status, code = http.StatusConflict, "needsKey"
	case errors.Is(err, storage.ErrWrongKey):
		status, code = http.StatusConflict, "wrongKey"
	case errors.Is(err, storage.ErrInUse):
		status, code = http.StatusConflict, "inUse"
	case errors.Is(err, storage.ErrBusy):
		status, code = http.StatusConflict, "busy"
	case errors.Is(err, storage.ErrExists):
		status = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	body := map[string]string{"error": err.Error()}
	if code != "" {
		body["code"] = code
	}
	writeJSON(w, status, body)
}

// remoteJSON is a remote with which secrets it has and what uses it.
type remoteJSON struct {
	storage.Remote
	SecretsSet   []string `json:"secretsSet"`
	Repositories []string `json:"repositories"`
}

func (s *Server) remoteJSON(r storage.Remote) remoteJSON {
	j := remoteJSON{Remote: r, SecretsSet: s.storage.Remotes.SecretsSet(r.Name), Repositories: s.storage.Store.RepositoriesOnRemote(r.Name)}
	if j.SecretsSet == nil {
		j.SecretsSet = []string{}
	}
	if j.Repositories == nil {
		j.Repositories = []string{}
	}
	return j
}

type remoteRequest struct {
	Remote  storage.Remote  `json:"remote"`
	Secrets storage.Secrets `json:"secrets"`
}

// admin reports whether the request is an administrator's, writing 403 if not.
func (s *Server) admin(w http.ResponseWriter, r *http.Request) (string, bool) {
	a, _ := s.account(r)
	return a.Username, requireAdmin(w, a)
}

func (s *Server) handleListRemotes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	out := []remoteJSON{}
	for _, rem := range s.storage.Remotes.List() {
		out = append(out, s.remoteJSON(rem))
	}
	writeJSON(w, http.StatusOK, map[string]any{"remotes": out, "s3Providers": storage.S3Providers, "webdavVendors": storage.WebDAVVendors})
}

func (s *Server) handleGetRemote(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	rem, ok := s.storage.Remotes.Get(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such remote")
		return
	}
	writeJSON(w, http.StatusOK, s.remoteJSON(rem))
}

func (s *Server) handleCreateRemote(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	var req remoteRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	if err := s.storage.CreateRemote(ctx, req.Remote, req.Secrets); err != nil {
		storageError(w, err)
		return
	}
	rem, _ := s.storage.Remotes.Get(req.Remote.Name)
	s.Log.Info("created storage remote", "remote", rem.Name, "type", rem.Type, "by", user)
	writeJSON(w, http.StatusCreated, s.remoteJSON(rem))
}

func (s *Server) handleUpdateRemote(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var req remoteRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if _, ok := s.storage.Remotes.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such remote")
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	if err := s.storage.UpdateRemote(ctx, name, req.Remote, req.Secrets); err != nil {
		storageError(w, err)
		return
	}
	rem, _ := s.storage.Remotes.Get(name)
	s.Log.Info("changed storage remote", "remote", name, "by", user)
	writeJSON(w, http.StatusOK, s.remoteJSON(rem))
}

func (s *Server) handleDeleteRemote(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := s.storage.DeleteRemote(name); err != nil {
		storageError(w, err)
		return
	}
	s.Log.Info("deleted storage remote", "remote", name, "by", user)
	w.WriteHeader(http.StatusNoContent)
}

// handleTestRemote tests an unsaved remote, or with a name, a saved one as
// edited (secrets left out are the stored ones).
func (s *Server) handleTestRemote(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	var req remoteRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	var err error
	if name := r.PathValue("name"); name != "" {
		if _, ok := s.storage.Remotes.Get(name); !ok {
			writeError(w, http.StatusNotFound, "no such remote")
			return
		}
		err = s.storage.TestSavedRemote(ctx, name, req.Remote, req.Secrets)
	} else {
		err = s.storage.TestRemote(ctx, req.Remote, req.Secrets)
	}
	if err != nil {
		storageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- repositories ---

func (s *Server) handleCreateRepository(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Remote      string `json:"remote"`
		Folder      string `json:"folder"`
		RecoveryKey string `json:"recoveryKey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	ctx, cancel := timeout(r, 3*time.Minute)
	defer cancel()
	key, err := s.storage.CreateRepository(ctx, req.Name, req.Remote, req.Folder, req.RecoveryKey)
	if err != nil {
		storageError(w, err)
		return
	}
	repo, _ := s.storage.Store.Repository(req.Name)
	s.Log.Info("added backup repository", "repository", repo.Name, "remote", repo.Remote, "new", key != "", "by", user)
	out := map[string]any{"repository": repo}
	if key != "" {
		out["recoveryKey"] = key // shown once
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleRemoveRepository(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if err := s.storage.RemoveRepository(name); err != nil {
		storageError(w, err)
		return
	}
	s.Log.Info("removed backup repository", "repository", name, "by", user)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRecoveryKey(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	key, err := s.storage.RecoveryKey(name)
	if err != nil {
		storageError(w, err)
		return
	}
	s.Log.Info("revealed recovery key", "repository", name, "by", user)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"recoveryKey": key})
}

// --- policies and backups ---

// itemJSON is an item's backup policy (null without one), run state and
// what it is doing.
type itemJSON struct {
	Item     storage.Item     `json:"item"`
	Policy   *storage.Policy  `json:"policy"`
	State    storage.RunState `json:"state"`
	Activity string           `json:"activity"`
	NextRun  *time.Time       `json:"nextRun"`
}

func (s *Server) itemJSON(it storage.Item) itemJSON {
	j := itemJSON{Item: it, State: s.storage.State.Get(it), Activity: s.storage.Activity(it)}
	if p, ok := s.storage.Store.Policy(it); ok {
		j.Policy = &p
		if p.Enabled {
			next := storage.NextRun(p, j.State, s.storage.Loc)
			j.NextRun = &next
		}
	}
	return j
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	items := []itemJSON{}
	for _, p := range s.storage.Store.Policies() {
		items = append(items, s.itemJSON(p.Item))
	}
	out := map[string]any{"repositories": s.storage.Store.Repositories(), "items": items, "host": s.storage.Tool.Host}
	if r.URL.Query().Get("catalog") == "1" {
		ctx, cancel := timeout(r, 2*time.Minute)
		defer cancel()
		catalog, errs := s.storage.Catalog(ctx)
		if catalog == nil {
			catalog = []storage.CatalogItem{}
		}
		out["catalog"], out["catalogErrors"] = catalog, errs
	}
	writeJSON(w, http.StatusOK, out)
}

// item reads the request's {kind}/{name}, writing 400 when invalid.
func item(w http.ResponseWriter, r *http.Request) (storage.Item, bool) {
	it, err := storage.ParseItem(r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return storage.Item{}, false
	}
	return it, true
}

func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	if it, ok := item(w, r); ok {
		writeJSON(w, http.StatusOK, s.itemJSON(it))
	}
}

func (s *Server) handlePutBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	var p storage.Policy
	if err := readJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	p.Item = it
	if err := s.storage.PutPolicy(p); err != nil {
		storageError(w, err)
		return
	}
	s.Log.Info("saved backup policy", "item", it, "repository", p.Repository, "enabled", p.Enabled, "by", user)
	writeJSON(w, http.StatusOK, s.itemJSON(it))
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	if err := s.storage.Store.DeletePolicy(it); err != nil {
		storageError(w, err)
		return
	}
	s.Log.Info("deleted backup policy", "item", it, "by", user)
	w.WriteHeader(http.StatusNoContent)
}

// jobEnvironment is the environment a job about it shows on, if any.
func jobEnvironment(it storage.Item) string {
	if it.Kind == storage.KindEnvironment {
		return it.Name
	}
	return ""
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	if _, ok := s.storage.Store.Policy(it); !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s has no backup policy", it))
		return
	}
	if err := s.storage.Busy(it.Kind, it.Name); err != nil {
		storageError(w, err)
		return
	}
	id := s.jobs.start(user, jobEnvironment(it), func(ctx context.Context, progress func(string, float64)) error {
		return s.storage.BackUp(ctx, it, storage.ReasonManual, progress)
	})
	s.Log.Info("backing up", "item", it, "by", user)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	snaps, err := s.storage.Snapshots(ctx, it)
	if err != nil {
		storageError(w, err)
		return
	}
	if snaps == nil {
		snaps = []storage.ItemSnapshot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) handleSnapshotFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.admin(w, r); !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	ctx, cancel := timeout(r, 2*time.Minute)
	defer cancel()
	q := r.URL.Query()
	entries, err := s.storage.Browse(ctx, it, q.Get("repository"), r.PathValue("id"), q.Get("path"))
	if err != nil {
		storageError(w, err)
		return
	}
	if entries == nil {
		entries = []storage.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleSnapshotDownload streams a file of a snapshot, or a folder as a zip
// (dir=1). An error once it has started can only cut the download short.
func (s *Server) handleSnapshotDownload(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	started := false
	err := s.storage.Download(r.Context(), it, q.Get("repository"), r.PathValue("id"), q.Get("path"), q.Get("dir") == "1", func(name string) io.Writer {
		started = true
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("Cache-Control", "no-store")
		return w
	})
	switch {
	case err != nil && !started:
		storageError(w, err)
	case err != nil:
		s.Log.Warn("snapshot download cut short", "item", it, "err", err)
	default:
		s.Log.Info("downloaded from a snapshot", "item", it, "snapshot", r.PathValue("id"), "path", q.Get("path"), "by", user)
	}
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	user, ok := s.admin(w, r)
	if !ok {
		return
	}
	it, ok := item(w, r)
	if !ok {
		return
	}
	var req storage.RestoreRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !slices.Contains([]string{"here", "new", "folder"}, req.Mode) {
		writeError(w, http.StatusBadRequest, "mode is here, new or folder")
		return
	}
	if req.Snapshot == "" {
		writeError(w, http.StatusBadRequest, "no snapshot")
		return
	}
	if err := storage.CheckRestorePaths(req.Paths); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	env := jobEnvironment(it)
	if req.Mode == "new" {
		env = ""
	}
	id := s.jobs.runResult(user, env, false, func(ctx context.Context, progress func(string, float64)) (any, error) {
		res, err := s.storage.Restore(ctx, it, req, progress)
		if err == nil {
			s.events.refresh()
		}
		return res, err
	})
	s.Log.Info("restoring", "item", it, "snapshot", req.Snapshot, "mode", req.Mode, "by", user)
	writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
}
