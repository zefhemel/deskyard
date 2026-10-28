package environments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/helper"
)

// validChild refuses anything but one folder name.
func validChild(child string) error {
	if child == "" || child == "." || child == ".." || strings.ContainsRune(child, '/') {
		return fmt.Errorf("not a folder name: %q", child)
	}
	return nil
}

// RemoveTree removes the folder child of root, such as a workspace folder
// in home/, with everything in it. Its files belong to UID 1000 (and Flatpak
// leaves read-only directories) or to root, so what the supervisor can't
// remove itself is removed by a root helper.
func (s *Service) RemoveTree(ctx context.Context, root, child string) error {
	if err := validChild(child); err != nil {
		return err
	}
	folder := filepath.Join(root, child)
	if err := os.RemoveAll(folder); err == nil {
		return nil
	}
	res, err := s.Helpers().Run(ctx, helper.Spec{
		Purpose: helper.Folders, Network: "none",
		Mounts: []engine.Mount{{Source: root, Target: "/parent", MustExist: true}},
		Cmd:    []string{"rm", "-rf", "--", "/parent/" + child},
	})
	if err != nil {
		return fmt.Errorf("deleting %s: %w", s.Layout.ToHost(folder), err)
	}
	if err := res.Err("deleting " + s.Layout.ToHost(folder)); err != nil {
		return err
	}
	if _, err := os.Stat(folder); err == nil {
		return fmt.Errorf("could not delete %s", s.Layout.ToHost(folder))
	}
	return nil
}

// removeTree is RemoveTree.
func (s *Service) removeTree(ctx context.Context, root, child string) error {
	return s.RemoveTree(ctx, root, child)
}

// MakeUserFolder makes the folder child of parent, a folder on the engine
// host the supervisor may not be able to write in, owned by the desktop
// user (UID and GID 1000) with mode 0755, through a root helper.
func (s *Service) MakeUserFolder(ctx context.Context, parent, child string) error {
	if err := validChild(child); err != nil {
		return err
	}
	res, err := s.Helpers().Run(ctx, helper.Spec{
		Purpose: helper.Folders, Network: "none",
		Mounts: []engine.Mount{{Source: parent, Target: "/parent", MustExist: true}},
		Cmd:    []string{"install", "-d", "-o", "1000", "-g", "1000", "-m", "0755", "/parent/" + child},
	})
	if err != nil {
		return fmt.Errorf("making %s: %w", child, err)
	}
	return res.Err("making " + child)
}
