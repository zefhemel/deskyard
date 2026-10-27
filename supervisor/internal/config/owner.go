package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// UID and GID are who the supervisor runs as, and who owns its data folder.
// They are fixed: no setting changes them.
const UID, GID = 1000, 1000

const defaultSocket = "/var/run/docker.sock"

// ReownOnce gives the data folder to UID/GID if an earlier supervisor, which
// ran as root, made it. It does nothing once state/owner exists. It chowns
// the supervisor's own files recursively (the top-level JSON stores, secret/,
// state/, cache/) and the data folder, home/, volume/ and each direct child
// of home/ and volume/ without recursing: the contents of homes and volumes
// are left alone, since an environment re-owns its home when it starts.
// Nothing is followed through a symlink. The marker is written last, so a
// run that dies midway repeats harmlessly. chown is injected so tests run
// without root.
func ReownOnce(l Layout, chown func(path string, uid, gid int) error) error {
	marker := filepath.Join(l.State, "owner")
	if _, err := os.Lstat(marker); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(l.State, 0o700); err != nil {
		return err
	}
	own := func(path string) error { return chown(path, UID, GID) }
	if err := own(l.Data); err != nil {
		return err
	}
	top, err := os.ReadDir(l.Data)
	if err != nil {
		return err
	}
	for _, e := range top {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			if err := own(filepath.Join(l.Data, e.Name())); err != nil {
				return err
			}
		}
	}
	for _, dir := range []string{l.Secret, l.State, l.Cache} {
		err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return own(path)
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for _, dir := range []string{l.Home, l.Volume} {
		children, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := own(dir); err != nil {
			return err
		}
		for _, c := range children {
			if err := own(filepath.Join(dir, c.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d\n", UID)), 0o600); err != nil {
		return err
	}
	return own(marker)
}

// SocketGroup is the group of the engine's unix socket, which the supervisor
// joins to use it. engineHost is a DOCKER_HOST-style address: "" is the
// default socket. For a TCP (or other) host there is no group and ok is
// false.
func SocketGroup(engineHost string) (gid int, ok bool, err error) {
	path := defaultSocket
	if engineHost != "" {
		p, isUnix := strings.CutPrefix(engineHost, "unix://")
		if !isUnix {
			return 0, false, nil
		}
		path = p
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false, fmt.Errorf("container engine socket: %w", err)
	}
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false, fmt.Errorf("container engine socket %s: no ownership information", path)
	}
	return int(st.Gid), true, nil
}
