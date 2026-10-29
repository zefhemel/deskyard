package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The computer's folder reports its size and the disk holding it, for the
// overview's Disk row.
func TestFolderReportsSizeAndDisk(t *testing.T) {
	s, ts := newTestServer(t)
	home := s.Computers.Layout.HomeOf("alice")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body := get(t, login(t, ts, "admin"), ts.URL+"/api/computers/alice/folder")
	var folder struct {
		Path     string `json:"path"`
		Bytes    int64  `json:"bytes"`
		Complete bool   `json:"complete"`
		DiskFree uint64 `json:"diskFree"`
		DiskSize uint64 `json:"diskSize"`
	}
	if err := json.Unmarshal([]byte(body), &folder); resp.StatusCode != 200 || err != nil {
		t.Fatalf("folder: %d %s", resp.StatusCode, body)
	}
	if folder.Bytes < 5000 || !folder.Complete || folder.DiskSize == 0 || folder.DiskFree > folder.DiskSize {
		t.Fatalf("folder: %s", body)
	}
}
