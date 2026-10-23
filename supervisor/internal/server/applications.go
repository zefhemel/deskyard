package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
)

var windowID = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

type applicationEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type applicationWindow struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	App       string  `json:"app"`
	Parent    *string `json:"parent"`
	Active    bool    `json:"active"`
	Minimized bool    `json:"minimized"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
}
type applicationInventory struct {
	Apps    []applicationEntry  `json:"apps"`
	Windows []applicationWindow `json:"windows"`
}

var applicationID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}\.desktop$`)

// The helper is in the image, runs as user, and returns data, never HTML.
// Inventory is bounded before decoding because the desktop is untrusted.
func (s *Server) handleApplications(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.desktopFor(w, r, auth.View)
	if !ok {
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, desktops.ErrStopped.Error())
		return
	}
	args := []string{"/usr/local/bin/deskyard-apps"}
	if r.Method == http.MethodGet {
		action := "list"
		if r.URL.Query().Get("windows") == "1" {
			action = "windows"
		}
		args = append(args, action)
	} else {
		var req struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}
		if err := readJSON(r, &req); err != nil ||
			!(req.Action == "activate" && windowID.MatchString(req.ID) || req.Action == "launch" && applicationID.MatchString(req.ID)) {
			writeError(w, http.StatusBadRequest, "Choose an installed application or an open window")
			return
		}
		args = append(args, req.Action, req.ID)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := s.execStream(ctx, c, 0, args, func(src io.Reader) error {
		_, err := io.Copy(&out, io.LimitReader(src, (1<<20)+1))
		if out.Len() > 1<<20 {
			return errors.New("application inventory is too large")
		}
		return err
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "Application helper unavailable; this prototype needs its updated desktop image")
		return
	}
	var data any
	if r.Method == http.MethodGet {
		var inventory applicationInventory
		if json.Unmarshal(out.Bytes(), &inventory) != nil || inventory.Apps == nil || inventory.Windows == nil || len(inventory.Apps) > 1024 || len(inventory.Windows) > 256 {
			writeError(w, http.StatusBadGateway, "Invalid application inventory")
			return
		}
		for _, a := range inventory.Apps {
			if !applicationID.MatchString(a.ID) || len(a.Name) > 1024 {
				writeError(w, http.StatusBadGateway, "Invalid application entry")
				return
			}
		}
		for _, v := range inventory.Windows {
			if !windowID.MatchString(v.ID) || (v.Parent != nil && !windowID.MatchString(*v.Parent)) || len(v.Title) > 2048 || len(v.App) > 1024 || v.Width <= 0 || v.Height <= 0 || v.Width > 65535 || v.Height > 65535 || v.X < -65535 || v.X > 65535 || v.Y < -65535 || v.Y > 65535 {
				writeError(w, http.StatusBadGateway, "Invalid application window")
				return
			}
		}
		data = inventory
	} else {
		var action struct {
			ID *string `json:"id"`
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(out.Bytes(), &raw) != nil || raw["id"] == nil || json.Unmarshal(out.Bytes(), &action) != nil || action.ID != nil && !windowID.MatchString(*action.ID) {
			writeError(w, http.StatusBadGateway, "Invalid application action response")
			return
		}
		data = action
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, data)
}
