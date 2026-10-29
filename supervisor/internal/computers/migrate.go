package computers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

// This file is removable once every install has started on this version; see
// the Computer terminology feature page, "Compatibility code".

// legacyDefinitionsName is the definitions file of supervisors from before
// computers were called computers.
const legacyDefinitionsName = "environments.json"

// MigrateDataFiles moves the data folder's files from before computers were
// called computers to their current names. The supervisor runs it at start,
// before any store loads; it is a no-op once done. Accounts and backups read
// their older keys when they load, so only the definitions file moves here.
func MigrateDataFiles(l config.Layout) error {
	return migrateDefinitions(filepath.Join(l.Data, legacyDefinitionsName), definitionsPath(l))
}

// migrateDefinitions rewrites old as path, with its "environments" list under
// "computers", and then removes old. path is written and its directory synced
// before old is removed, so a crash at any point leaves old, path or both.
// Both means an earlier run stopped after writing path, which is then
// complete: it is kept and old removed.
func migrateDefinitions(old, path string) error {
	b, err := os.ReadFile(old)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return removeSynced(old)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var f map[string]json.RawMessage
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("%s: %w", old, err)
	}
	if list, ok := f["environments"]; ok {
		if _, clash := f["computers"]; clash {
			return fmt.Errorf("%s: has both \"environments\" and \"computers\"", old)
		}
		f["computers"] = list
		delete(f, "environments")
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := store.WriteFile(path, append(out, '\n')); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return removeSynced(old)
}

// removeSynced removes path and syncs its directory.
func removeSynced(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
